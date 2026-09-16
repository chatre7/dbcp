package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func snapshotFixture() *schema {
	fixture := &schema{
		tables: map[objectName]*table{{"dbo", "Orders"}: {
			columns: map[string]column{
				"TenantID": {"int", false}, "ID": {"bigint", false},
				"Payload": {"[types].[Payload] (base: nvarchar(100))", true},
			},
			keyType: "NONCLUSTERED",
			key:     []keyColumn{{"TenantID", 1, false}, {"ID", 2, true}},
		}},
		objects: map[objectName]schemaObject{
			{"dbo", "Procedure"}:     {typeCode: "P", definition: "CREATE PROCEDURE dbo.Procedure AS SELECT 1", ansiNulls: true, quotedIdentifier: true},
			{"dbo", "View"}:          {typeCode: "V", definition: "CREATE VIEW dbo.View AS SELECT 1 AS Value"},
			{"dbo", "Function"}:      {typeCode: "FN", definition: "CREATE FUNCTION dbo.Function() RETURNS int AS BEGIN RETURN 1 END"},
			{"dbo", "Inline"}:        {typeCode: "IF", definition: "CREATE FUNCTION dbo.Inline() RETURNS TABLE AS RETURN SELECT 1 AS Value"},
			{"dbo", "TableFunction"}: {typeCode: "TF", definition: "CREATE FUNCTION dbo.TableFunction() RETURNS @t TABLE(Value int) AS BEGIN RETURN END"},
			{"dbo", "Filter"}:        {typeCode: "RF", definition: "CREATE PROCEDURE dbo.Filter AS SELECT 1"},
			{"dbo", "Synonym"}:       {typeCode: "SN", target: "[Remote].[dbo].[Orders]"},
			{"dbo", "CLR"}: {typeCode: "FS", clr: &clrFunction{
				assemblyName: "Library", assemblyIdentity: "Library, Version=1.0.0.0",
				assemblySHA256: strings.Repeat("ab", 32), permissionSet: "SAFE_ACCESS",
				className: "Functions", methodName: "Run", signature: "() RETURNS int",
				executeAs: "CALLER", nullOnNullInput: true,
			}},
			{"dbo", "CLRTable"}: {typeCode: "FT", clr: &clrFunction{
				assemblyName: "Library", assemblyIdentity: "Library, Version=1.0.0.0",
				assemblySHA256: strings.Repeat("ab", 32), permissionSet: "SAFE_ACCESS",
				className: "Functions", methodName: "Rows", signature: "() RETURNS TABLE ([ID] int NOT NULL)",
				executeAs: "CALLER",
			}},
		},
		snapshot: &snapshotMetadata{Identity: databaseIdentity{"server\\instance", "Database"}, Objects: []objectTimestamp{
			{Schema: "dbo", Name: "Orders", Type: "U", CreatedAt: "2025-01-01 12:13:14.123", ModifiedAt: "2025-02-03 15:16:17.890"},
		}},
	}
	for name, object := range fixture.objects {
		fixture.snapshot.Objects = append(fixture.snapshot.Objects, objectTimestamp{
			Schema: name.schema, Name: name.name, Type: object.typeCode,
			CreatedAt: "2025-01-01 12:13:14.123", ModifiedAt: "2025-02-03 15:16:17.890",
		})
	}
	slices.SortFunc(fixture.snapshot.Objects, func(a, b objectTimestamp) int {
		return compareSnapshotNames(a.Schema, a.Name, b.Schema, b.Name)
	})
	return fixture
}

func mustMakeSnapshot(t *testing.T, s *schema, at time.Time) *schemaSnapshot {
	t.Helper()
	result, err := makeSnapshot(s, at)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSnapshotRoundTripRetainsComparisonBehavior(t *testing.T) {
	original := snapshotFixture()
	// Memory-optimized hash PKs are catalog metadata too, not just B-tree PKs.
	original.tables[objectName{"dbo", "Orders"}].keyType = "NONCLUSTERED HASH"
	original.tables[objectName{"dbo", "Orders"}].key[1].descending = false
	captured := time.Date(2026, 9, 16, 17, 30, 0, 123, time.FixedZone("capture", 3*60*60))
	snapshot := mustMakeSnapshot(t, original, captured)
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded schemaSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	restored, err := decoded.restore()
	if err != nil {
		t.Fatal(err)
	}
	if diffs := compareSchemas(original, restored, sqlModeStrict); len(diffs) != 0 {
		t.Fatalf("round trip changed the compared schema: %+v", diffs)
	}
	if !decoded.CapturedAt.Equal(captured) || decoded.CapturedAt.Location() != time.UTC {
		t.Fatalf("capture instant must be serialized in UTC: %s", decoded.CapturedAt)
	}
	if !reflect.DeepEqual(original.snapshot.Objects, restored.snapshot.Objects) {
		t.Fatal("SQL Server local timestamps changed in storage")
	}
	// Catalog timestamps are informational, even when a SQL object was modified.
	restored.snapshot.Objects[0].ModifiedAt = "2026-09-16 23:59:59.999"
	if diffs := compareSchemas(original, restored, sqlModeStrict); len(diffs) != 0 {
		t.Fatalf("informational timestamps produced differences: %+v", diffs)
	}
	table := restored.tables[objectName{"dbo", "Orders"}]
	table.columns["Payload"] = column{"xml(CONTENT [types].[Document])", false}
	table.key = []keyColumn{{"ID", 1, false}, {"TenantID", 2, true}}
	table.keyType = "CLUSTERED"
	procedure := restored.objects[objectName{"dbo", "Procedure"}]
	procedure.definition = "ALTER PROCEDURE dbo.Procedure AS SELECT 2"
	procedure.ansiNulls, procedure.quotedIdentifier = false, false
	restored.objects[objectName{"dbo", "Procedure"}] = procedure
	restored.objects[objectName{"dbo", "Synonym"}] = schemaObject{typeCode: "SN", target: "[Other].[dbo].[Orders]"}
	clr := restored.objects[objectName{"dbo", "CLR"}].clr
	clr.assemblyName, clr.assemblyIdentity, clr.assemblySHA256 = "NewLibrary", "NewLibrary, Version=2.0.0.0", strings.Repeat("cd", 32)
	clr.permissionSet, clr.className, clr.methodName = "EXTERNAL_ACCESS", "OtherFunctions", "OtherRun"
	clr.signature, clr.executeAs, clr.nullOnNullInput = "() RETURNS bigint", "OWNER", false
	var got []string
	for _, diff := range compareSchemas(original, restored, sqlModeStrict) {
		got = append(got, diff.kind)
	}
	want := []string{"COLUMN_TYPE", "COLUMN_NULLABILITY", "PRIMARY_KEY",
		"STORED_PROCEDURE_DEFINITION", "STORED_PROCEDURE_ANSI_NULLS", "STORED_PROCEDURE_QUOTED_IDENTIFIER", "SYNONYM_TARGET",
		"CLR_FUNCTION_ASSEMBLY_NAME", "CLR_FUNCTION_ASSEMBLY_IDENTITY", "CLR_FUNCTION_ASSEMBLY_SHA256",
		"CLR_FUNCTION_PERMISSION_SET", "CLR_FUNCTION_CLASS", "CLR_FUNCTION_METHOD", "CLR_FUNCTION_SIGNATURE",
		"CLR_FUNCTION_EXECUTE_AS", "CLR_FUNCTION_NULL_ON_NULL_INPUT"}
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored schema lost observable differences: got %v, want %v", got, want)
	}
}

func TestSnapshotLatestUsesCaptureTimeAndDatabaseIdentity(t *testing.T) {
	dir := t.TempDir()
	fixture := snapshotFixture()
	var expected *schemaSnapshot
	var expectedPath string
	for _, at := range []time.Time{
		time.Date(2025, 12, 31, 12, 0, 0, 0, time.Local),
		time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local),
		time.Date(2026, 1, 31, 12, 0, 0, 0, time.Local),
		time.Date(2026, 2, 1, 12, 0, 0, 0, time.Local),
	} {
		expected = mustMakeSnapshot(t, fixture, at)
		report, err := saveSnapshot(dir, expected, "report", "text")
		if err != nil {
			t.Fatal(err)
		}
		expectedPath = filepath.Join(filepath.Dir(report), "snapshot.json")
	}
	other := snapshotFixture()
	other.snapshot.Identity.Database = "OtherDatabase"
	foreign := mustMakeSnapshot(t, other, expected.CapturedAt.Add(24*time.Hour))
	foreignReport, err := saveSnapshot(dir, foreign, "foreign", "html")
	if err != nil {
		t.Fatal(err)
	}
	// Corruption in a different database must not block this database's baseline.
	if err := os.WriteFile(filepath.Join(filepath.Dir(foreignReport), "snapshot.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	unfinished := filepath.Join(dir, expected.CapturedAt.Add(48*time.Hour).In(time.Local).Format("020106150405"), snapshotDatabaseID(expected.Identity))
	if err := os.MkdirAll(unfinished, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unfinished, "report.txt"), []byte("unfinished"), 0600); err != nil {
		t.Fatal(err)
	}
	found, path, err := findPreviousSnapshot(dir, fixture.snapshot.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if found == nil || !found.CapturedAt.Equal(expected.CapturedAt) || path != expectedPath {
		t.Fatalf("wrong baseline across date rollover: snapshot=%+v path=%s", found, path)
	}
	missing, path, err := findPreviousSnapshot(dir, databaseIdentity{"another-server", fixture.snapshot.Identity.Database})
	if err != nil || missing != nil || path != "" {
		t.Fatalf("another server must have no baseline: %v %s %v", missing, path, err)
	}
}

func TestSnapshotCollisionPreservesCommittedBaseline(t *testing.T) {
	dir := t.TempDir()
	snapshot := mustMakeSnapshot(t, snapshotFixture(), time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	reportPath, err := saveSnapshot(dir, snapshot, "original report", "text")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(reportPath), "snapshot.json")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saveSnapshot(dir, snapshot, "replacement", "html"); err == nil {
		t.Fatal("same-second capture must not replace a committed snapshot")
	}
	after, err := os.ReadFile(marker)
	if err != nil || string(before) != string(after) {
		t.Fatalf("collision changed the baseline: %v", err)
	}
	report, err := os.ReadFile(reportPath)
	if err != nil || string(report) != "original report" {
		t.Fatalf("collision changed the report: %v", err)
	}
	found, path, err := findPreviousSnapshot(dir, snapshot.Identity)
	if err != nil || found == nil || path != marker {
		t.Fatalf("collision lost the prior baseline: %v %s %v", found, path, err)
	}
}

func TestSnapshotCorruptionFailsClosedWithoutLosingBaseline(t *testing.T) {
	for _, corruption := range []struct {
		name string
		edit func(*schemaSnapshot)
	}{
		{"incompatible version", func(s *schemaSnapshot) { s.Version++ }},
		{"missing tables", func(s *schemaSnapshot) { s.Tables = nil }},
		{"missing timestamp inventory", func(s *schemaSnapshot) { s.Timestamps = []objectTimestamp{} }},
		{"timestamp type mismatch", func(s *schemaSnapshot) { s.Timestamps[0].Type = "U" }},
		{"duplicate object", func(s *schemaSnapshot) { s.Objects = append(s.Objects, s.Objects[0]) }},
		{"missing nullability", func(s *schemaSnapshot) { s.Tables[0].Columns[0].Nullable = nil }},
		{"invalid primary key", func(s *schemaSnapshot) { s.Tables[0].Key[0].Ordinal = 2 }},
		{"missing CLR", func(s *schemaSnapshot) { s.Objects[0].CLR = nil }},
	} {
		t.Run(corruption.name, func(t *testing.T) {
			dir := t.TempDir()
			baseline := mustMakeSnapshot(t, snapshotFixture(), time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
			reportPath, err := saveSnapshot(dir, baseline, "original", "text")
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(filepath.Dir(reportPath), "snapshot.json")
			before, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			broken := mustMakeSnapshot(t, snapshotFixture(), baseline.CapturedAt.Add(time.Second))
			corruption.edit(broken)
			data, err := json.Marshal(broken)
			if err != nil {
				t.Fatal(err)
			}
			brokenDir := filepath.Join(dir, broken.CapturedAt.In(time.Local).Format("020106150405"), snapshotDatabaseID(broken.Identity))
			if err := os.MkdirAll(brokenDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(brokenDir, "snapshot.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if found, _, err := findPreviousSnapshot(dir, baseline.Identity); err == nil || found != nil {
				t.Fatal("corrupt matching snapshot must not silently reset or fall back to an older baseline")
			}
			after, err := os.ReadFile(marker)
			if err != nil || string(before) != string(after) {
				t.Fatalf("corruption handling changed the previous baseline: %v", err)
			}
		})
	}
}
