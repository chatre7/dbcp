package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const snapshotVersion = 1

// Explicit DTOs keep credentials and migration-only metadata outside the format.
// Pointer booleans distinguish an intentional false from missing JSON metadata.
type schemaSnapshot struct {
	Version    int               `json:"version"`
	CapturedAt time.Time         `json:"captured_at"`
	Identity   databaseIdentity  `json:"identity"`
	Timestamps []objectTimestamp `json:"timestamps"`
	Tables     []snapshotTable   `json:"tables"`
	Objects    []snapshotObject  `json:"objects"`
}

type snapshotTable struct {
	Schema  string           `json:"schema"`
	Name    string           `json:"name"`
	Columns []snapshotColumn `json:"columns"`
	KeyType string           `json:"key_type"`
	Key     []snapshotKey    `json:"key"`
}

type snapshotColumn struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"`
	Nullable *bool  `json:"nullable"`
}

type snapshotKey struct {
	Name       string `json:"name"`
	Ordinal    int    `json:"ordinal"`
	Descending *bool  `json:"descending"`
}

type snapshotObject struct {
	Schema           string       `json:"schema"`
	Name             string       `json:"name"`
	TypeCode         string       `json:"type_code"`
	Definition       string       `json:"definition"`
	AnsiNulls        *bool        `json:"ansi_nulls"`
	QuotedIdentifier *bool        `json:"quoted_identifier"`
	Target           string       `json:"target"`
	CLR              *snapshotCLR `json:"clr"`
}

type snapshotCLR struct {
	AssemblyName     string `json:"assembly_name"`
	AssemblyIdentity string `json:"assembly_identity"`
	AssemblySHA256   string `json:"assembly_sha256"`
	PermissionSet    string `json:"permission_set"`
	ClassName        string `json:"class_name"`
	MethodName       string `json:"method_name"`
	Signature        string `json:"signature"`
	ExecuteAs        string `json:"execute_as"`
	NullOnNullInput  *bool  `json:"null_on_null_input"`
}

func snapshotBool(value bool) *bool { return &value }

func makeSnapshot(s *schema, at time.Time) (*schemaSnapshot, error) {
	if s == nil || s.snapshot == nil || s.tables == nil || s.objects == nil {
		return nil, errors.New("snapshot requires complete schema and database metadata")
	}
	result := &schemaSnapshot{
		Version: snapshotVersion, CapturedAt: at.UTC(), Identity: s.snapshot.Identity,
		Timestamps: append([]objectTimestamp{}, s.snapshot.Objects...),
		Tables:     make([]snapshotTable, 0, len(s.tables)),
		Objects:    make([]snapshotObject, 0, len(s.objects)),
	}
	for name, t := range s.tables {
		if t == nil || t.columns == nil {
			return nil, errors.New("snapshot contains incomplete table metadata")
		}
		item := snapshotTable{Schema: name.schema, Name: name.name, KeyType: t.keyType,
			Columns: make([]snapshotColumn, 0, len(t.columns)), Key: make([]snapshotKey, 0, len(t.key))}
		for name, c := range t.columns {
			item.Columns = append(item.Columns, snapshotColumn{name, c.dataType, snapshotBool(c.nullable)})
		}
		slices.SortFunc(item.Columns, func(a, b snapshotColumn) int { return strings.Compare(a.Name, b.Name) })
		for _, k := range t.key {
			item.Key = append(item.Key, snapshotKey{k.name, k.ordinal, snapshotBool(k.descending)})
		}
		result.Tables = append(result.Tables, item)
	}
	slices.SortFunc(result.Tables, func(a, b snapshotTable) int {
		return compareSnapshotNames(a.Schema, a.Name, b.Schema, b.Name)
	})
	for name, o := range s.objects {
		item := snapshotObject{Schema: name.schema, Name: name.name, TypeCode: o.typeCode,
			Definition: o.definition, AnsiNulls: snapshotBool(o.ansiNulls),
			QuotedIdentifier: snapshotBool(o.quotedIdentifier), Target: o.target}
		if f := o.clr; f != nil {
			item.CLR = &snapshotCLR{f.assemblyName, f.assemblyIdentity, f.assemblySHA256,
				f.permissionSet, f.className, f.methodName, f.signature, f.executeAs, snapshotBool(f.nullOnNullInput)}
		}
		result.Objects = append(result.Objects, item)
	}
	slices.SortFunc(result.Objects, func(a, b snapshotObject) int {
		return compareSnapshotNames(a.Schema, a.Name, b.Schema, b.Name)
	})
	slices.SortFunc(result.Timestamps, func(a, b objectTimestamp) int {
		return compareSnapshotNames(a.Schema, a.Name, b.Schema, b.Name)
	})
	if _, err := result.restore(); err != nil {
		return nil, err
	}
	return result, nil
}

func compareSnapshotNames(aSchema, aName, bSchema, bName string) int {
	if c := strings.Compare(aSchema, bSchema); c != 0 {
		return c
	}
	return strings.Compare(aName, bName)
}

func validSnapshotIdentity(identity databaseIdentity) bool {
	return strings.TrimSpace(identity.Server) != "" && strings.TrimSpace(identity.Database) != ""
}

func (s *schemaSnapshot) restore() (*schema, error) {
	if s == nil || s.Version != snapshotVersion {
		return nil, errors.New("unsupported snapshot version; use a compatible executable or a different data directory")
	}
	if !validSnapshotIdentity(s.Identity) || s.CapturedAt.IsZero() {
		return nil, errors.New("snapshot has missing database identity or capture timestamp")
	}
	if s.Tables == nil || s.Objects == nil || s.Timestamps == nil {
		return nil, errors.New("snapshot is missing required schema collections")
	}
	result := &schema{tables: make(map[objectName]*table, len(s.Tables)),
		objects:  make(map[objectName]schemaObject, len(s.Objects)),
		snapshot: &snapshotMetadata{Identity: s.Identity, Objects: append([]objectTimestamp{}, s.Timestamps...)}}
	names := make(map[objectName]bool, len(s.Tables)+len(s.Objects))
	for i, item := range s.Tables {
		name := objectName{item.Schema, item.Name}
		if item.Schema == "" || item.Name == "" || names[name] || len(item.Columns) == 0 || item.Key == nil {
			return nil, fmt.Errorf("snapshot table %d has invalid, duplicate, or incomplete structure", i+1)
		}
		names[name] = true
		t := &table{columns: make(map[string]column, len(item.Columns)), keyType: item.KeyType}
		for _, c := range item.Columns {
			if _, exists := t.columns[c.Name]; exists || c.Name == "" || c.DataType == "" || c.Nullable == nil {
				return nil, fmt.Errorf("snapshot table %d has invalid or duplicate columns", i+1)
			}
			t.columns[c.Name] = column{c.DataType, *c.Nullable}
		}
		if (len(item.Key) == 0 && item.KeyType != "") ||
			(len(item.Key) > 0 && strings.TrimSpace(item.KeyType) == "") {
			return nil, fmt.Errorf("snapshot table %d has invalid primary-key metadata", i+1)
		}
		keys := make(map[string]bool, len(item.Key))
		for j, k := range item.Key {
			_, exists := t.columns[k.Name]
			if !exists || keys[k.Name] || k.Ordinal != j+1 || k.Descending == nil {
				return nil, fmt.Errorf("snapshot table %d has invalid primary-key columns or ordering", i+1)
			}
			keys[k.Name] = true
			t.key = append(t.key, keyColumn{k.Name, k.Ordinal, *k.Descending})
		}
		result.tables[name] = t
	}
	for i, item := range s.Objects {
		name := objectName{item.Schema, item.Name}
		if item.Schema == "" || item.Name == "" || names[name] || item.AnsiNulls == nil || item.QuotedIdentifier == nil {
			return nil, fmt.Errorf("snapshot object %d has invalid, duplicate, or incomplete structure", i+1)
		}
		names[name] = true
		o := schemaObject{typeCode: item.TypeCode, definition: item.Definition,
			ansiNulls: *item.AnsiNulls, quotedIdentifier: *item.QuotedIdentifier, target: item.Target}
		switch item.TypeCode {
		case "P", "RF", "V", "FN", "IF", "TF":
			if item.Definition == "" || item.Target != "" || item.CLR != nil {
				return nil, fmt.Errorf("snapshot SQL object %d has incomplete or inconsistent metadata", i+1)
			}
		case "SN":
			if item.Target == "" || item.Definition != "" || item.CLR != nil {
				return nil, fmt.Errorf("snapshot synonym %d has incomplete or inconsistent metadata", i+1)
			}
		case "FS", "FT":
			f := item.CLR
			if f == nil || f.AssemblyName == "" || f.AssemblyIdentity == "" || f.PermissionSet == "" ||
				f.ClassName == "" || f.MethodName == "" || f.Signature == "" || f.ExecuteAs == "" ||
				f.NullOnNullInput == nil || item.Definition != "" || item.Target != "" {
				return nil, fmt.Errorf("snapshot CLR object %d has incomplete or inconsistent metadata", i+1)
			}
			hash, err := hex.DecodeString(f.AssemblySHA256)
			if err != nil || len(hash) != sha256.Size {
				return nil, fmt.Errorf("snapshot CLR object %d has invalid assembly fingerprint", i+1)
			}
			o.clr = &clrFunction{f.AssemblyName, f.AssemblyIdentity, f.AssemblySHA256, f.PermissionSet,
				f.ClassName, f.MethodName, f.Signature, f.ExecuteAs, *f.NullOnNullInput}
		default:
			return nil, fmt.Errorf("snapshot object %d has an unsupported type", i+1)
		}
		result.objects[name] = o
	}
	timestampNames := make(map[objectName]bool, len(s.Timestamps))
	for i, item := range s.Timestamps {
		name := objectName{item.Schema, item.Name}
		if item.Schema == "" || item.Name == "" || item.CreatedAt == "" || item.ModifiedAt == "" || timestampNames[name] {
			return nil, fmt.Errorf("snapshot timestamp %d has incomplete or duplicate metadata", i+1)
		}
		switch item.Type {
		case "U", "P", "RF", "V", "FN", "IF", "TF", "FS", "FT", "SN":
		default:
			return nil, fmt.Errorf("snapshot timestamp %d has an unsupported object type", i+1)
		}
		expectedType := "U"
		if object, exists := result.objects[name]; exists {
			expectedType = object.typeCode
		} else if _, exists := result.tables[name]; !exists {
			return nil, fmt.Errorf("snapshot timestamp %d has no matching schema object", i+1)
		}
		if item.Type != expectedType {
			return nil, fmt.Errorf("snapshot timestamp %d has a mismatched object type", i+1)
		}
		timestampNames[name] = true
	}
	if len(timestampNames) != len(names) {
		return nil, errors.New("snapshot timestamps do not cover the captured schema")
	}
	return result, nil
}

func snapshotDatabaseID(identity databaseIdentity) string {
	// A JSON array is an unambiguous tuple, even for names containing separators.
	encoded, _ := json.Marshal([2]string{identity.Server, identity.Database})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func findPreviousSnapshot(dataDir string, identity databaseIdentity) (*schemaSnapshot, string, error) {
	if !validSnapshotIdentity(identity) {
		return nil, "", errors.New("cannot find snapshot without database identity")
	}
	entries, err := os.ReadDir(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("list snapshot directory: %w", err)
	}
	key := snapshotDatabaseID(identity)
	var latest *schemaSnapshot
	var latestPath string
	for _, entry := range entries {
		if !entry.IsDir() || !validSnapshotDateFolder(entry.Name()) {
			continue
		}
		path := filepath.Join(dataDir, entry.Name(), key, "snapshot.json")
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // No commit marker: an unfinished capture is not a baseline.
		}
		if err != nil {
			return nil, "", fmt.Errorf("read snapshot %s: %w", path, err)
		}
		var candidate schemaSnapshot
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&candidate); err != nil {
			return nil, "", fmt.Errorf("snapshot %s contains invalid JSON or incompatible fields", path)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, "", fmt.Errorf("snapshot %s contains trailing or invalid JSON", path)
		}
		if candidate.Identity != identity {
			return nil, "", fmt.Errorf("snapshot %s does not match its database identity directory", path)
		}
		if _, err := candidate.restore(); err != nil {
			return nil, "", fmt.Errorf("snapshot %s is not a usable baseline: %w", path, err)
		}
		if latest == nil || candidate.CapturedAt.After(latest.CapturedAt) {
			latest, latestPath = &candidate, path
		}
	}
	return latest, latestPath, nil
}

func validSnapshotDateFolder(name string) bool {
	if len(name) != 12 {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	_, err := time.Parse("020106150405", name)
	return err == nil
}

func saveSnapshot(dataDir string, current *schemaSnapshot, report string, format string) (string, error) {
	if format != "html" && format != "text" {
		return "", errors.New("snapshot report format must be html or text")
	}
	if _, err := current.restore(); err != nil {
		return "", err
	}
	// Normalize a copy so callers cannot accidentally serialize a local offset.
	encoded := *current
	encoded.CapturedAt = encoded.CapturedAt.UTC()
	data, err := json.MarshalIndent(&encoded, "", "  ")
	if err != nil {
		return "", errors.New("cannot encode snapshot metadata")
	}
	parent := filepath.Join(dataDir, current.CapturedAt.In(time.Local).Format("020106150405"))
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", fmt.Errorf("create snapshot date directory: %w", err)
	}
	dir := filepath.Join(parent, snapshotDatabaseID(current.Identity))
	if err := os.Mkdir(dir, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", errors.New("snapshot directory already exists for this database and local second; capture again in a later second or choose another data directory")
		}
		return "", fmt.Errorf("reserve snapshot directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			// This directory was reserved by this invocation, never by another capture.
			_ = os.RemoveAll(dir)
		}
	}()
	extension := format
	if format == "text" {
		extension = "txt"
	}
	reportPath := filepath.Join(dir, "report."+extension)
	if err := writeSnapshotFile(reportPath, []byte(report)); err != nil {
		return "", fmt.Errorf("write snapshot report: %w", err)
	}
	temporary := filepath.Join(dir, "snapshot.json.tmp")
	if err := writeSnapshotFile(temporary, append(data, '\n')); err != nil {
		return "", fmt.Errorf("write snapshot metadata: %w", err)
	}
	if err := os.Rename(temporary, filepath.Join(dir, "snapshot.json")); err != nil {
		return "", fmt.Errorf("publish snapshot metadata: %w", err)
	}
	published = true
	return reportPath, nil
}

func writeSnapshotFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
