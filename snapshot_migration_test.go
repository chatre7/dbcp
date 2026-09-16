package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func captureMigrationFixture(t *testing.T, s *schema) *schemaSnapshot {
	t.Helper()
	identity := databaseIdentity{Server: "captured-server", Database: "captured-database"}
	s.snapshot = &snapshotMetadata{Identity: identity, Objects: []objectTimestamp{}}
	s.migration.serverName, s.migration.databaseName = identity.Server, identity.Database
	for name := range s.tables {
		s.snapshot.Objects = append(s.snapshot.Objects, objectTimestamp{
			Schema: name.schema, Name: name.name, Type: "U", CreatedAt: "2026-09-16", ModifiedAt: "2026-09-16",
		})
	}
	for name, object := range s.objects {
		s.snapshot.Objects = append(s.snapshot.Objects, objectTimestamp{
			Schema: name.schema, Name: name.name, Type: object.typeCode, CreatedAt: "2026-09-16", ModifiedAt: "2026-09-16",
		})
	}
	return mustMakeSnapshot(t, s, time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
}

func storeMigrationFixture(t *testing.T, snapshot *schemaSnapshot) (*schema, error) {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	_, restored, err := readSnapshot(path)
	return restored, err
}

func TestSnapshotMigrationOrdersThroughUnchangedObjects(t *testing.T) {
	first, middle, last := objectName{"dbo", "z_first"}, objectName{"dbo", "m_middle"}, objectName{"dbo", "a_last"}
	old := schemaObject{typeCode: "V", definition: "CREATE VIEW dbo.example AS SELECT 1 AS value"}
	updated := old
	updated.definition = "CREATE VIEW dbo.example AS SELECT 2 AS value"
	source := migrationPlanSchema(map[objectName]schemaObject{first: updated, middle: old, last: updated})
	destination := migrationPlanSchema(map[objectName]schemaObject{first: old, middle: old, last: old})
	source.migration.dependencies[last] = []migrationDependency{{name: middle}}
	source.migration.dependencies[middle] = []migrationDependency{{name: first}}
	// Inventory includes schemas and objects outside the comparison surface.
	source.migration.schemas["unused"] = true
	source.migration.schemas["sys"] = true
	source.migration.objectTypes[objectName{"sys", "catalog"}] = "S"
	original, originalNotes, err := planMigration(source, destination, sqlModeStrict)
	if err != nil {
		t.Fatal(err)
	}
	captured := captureMigrationFixture(t, source)
	restoredSource, err := storeMigrationFixture(t, captured)
	if err != nil {
		t.Fatal(err)
	}
	restoredDestination, err := storeMigrationFixture(t, captureMigrationFixture(t, destination))
	if err != nil {
		t.Fatal(err)
	}
	steps, notes, err := planMigration(restoredSource, restoredDestination, sqlModeStrict)
	if err != nil || !reflect.DeepEqual(steps, original) || !reflect.DeepEqual(notes, originalNotes) ||
		len(steps) != 2 || steps[0].name != first || steps[1].name != last {
		t.Fatalf("stored catalog changed dependency ordering: %+v, %v, %v", steps, notes, err)
	}
	originalGuards, err := renderMigrationSQL(destination, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restoredGuards, err := renderMigrationSQL(restoredDestination, nil, nil)
	if err != nil || restoredGuards != originalGuards {
		t.Fatalf("stored destination changed executable identity guards: %v", err)
	}
	before, _ := json.Marshal(captured)
	after, _ := json.Marshal(mustMakeSnapshot(t, restoredSource, captured.CapturedAt))
	if string(before) != string(after) {
		t.Fatal("restoring and recapturing changed the deterministic snapshot")
	}
}

func TestSnapshotMigrationRetainsPlannerBlockers(t *testing.T) {
	changed, dependent := objectName{"dbo", "changed"}, objectName{"dbo", "dependent"}
	for _, scenario := range []struct {
		name string
		edit func(source, destination *schema)
		want string
	}{
		{"unsafe source", func(s, d *schema) { s.migration.unsafeObjects[changed] = "signed module" }, "signed module"},
		{"unsafe destination", func(s, d *schema) { d.migration.unsafeObjects[changed] = "indexed view" }, "indexed view"},
		{"schema-bound dependent", func(s, d *schema) {
			d.migration.dependencies[dependent] = []migrationDependency{{name: changed, schemaBound: true}}
		}, "schema-bound"},
		{"unresolved reference", func(s, d *schema) {
			s.migration.dependencies[changed] = []migrationDependency{{issue: "unresolved dependency dbo.missing"}}
		}, "unresolved dependency dbo.missing"},
		{"external reference", func(s, d *schema) {
			s.migration.dependencies[changed] = []migrationDependency{{issue: "external dependency Other.dbo.table requires manual validation"}}
		}, "external dependency Other.dbo.table"},
		{"cycle through unchanged object", func(s, d *schema) {
			s.migration.dependencies[changed] = []migrationDependency{{name: dependent}}
			s.migration.dependencies[dependent] = []migrationDependency{{name: changed}}
		}, changed.String() + " -> " + dependent.String() + " -> " + changed.String()},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			old := schemaObject{typeCode: "V", definition: "CREATE VIEW dbo.changed AS SELECT 1 AS value"}
			updated := old
			updated.definition = "CREATE VIEW dbo.changed AS SELECT 2 AS value"
			source := migrationPlanSchema(map[objectName]schemaObject{changed: updated, dependent: old})
			destination := migrationPlanSchema(map[objectName]schemaObject{changed: old, dependent: old})
			scenario.edit(source, destination)
			_, _, originalErr := planMigration(source, destination, sqlModeStrict)
			if originalErr == nil || !strings.Contains(originalErr.Error(), scenario.want) {
				t.Fatalf("fixture did not exercise expected blocker %q: %v", scenario.want, originalErr)
			}
			restoredSource, err := storeMigrationFixture(t, captureMigrationFixture(t, source))
			if err != nil {
				t.Fatal(err)
			}
			restoredDestination, err := storeMigrationFixture(t, captureMigrationFixture(t, destination))
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = planMigration(restoredSource, restoredDestination, sqlModeStrict)
			if err == nil || err.Error() != originalErr.Error() {
				t.Fatalf("snapshot weakened blocker: got %v, want %v", err, originalErr)
			}
		})
	}
}

func richSnapshotFixture() *schema {
	s := snapshotFixture()
	s.migration = &migrationMetadata{
		serverName: s.snapshot.Identity.Server, databaseName: s.snapshot.Identity.Database,
		schemas: map[string]bool{"dbo": true}, objectTypes: make(map[objectName]string),
		dependencies: make(map[objectName][]migrationDependency), unsafeObjects: make(map[objectName]string),
	}
	for name := range s.tables {
		s.migration.objectTypes[name] = "U"
	}
	for name, object := range s.objects {
		s.migration.objectTypes[name] = object.typeCode
	}
	return s
}

func TestSnapshotMigrationRejectsIncompletePayload(t *testing.T) {
	for _, scenario := range []struct {
		name string
		edit func(*schemaSnapshot)
	}{
		{"missing schemas", func(s *schemaSnapshot) { s.Migration.Schemas = nil }},
		{"missing inventory", func(s *schemaSnapshot) { s.Migration.Objects = nil }},
		{"missing dependencies", func(s *schemaSnapshot) { s.Migration.Objects[0].Dependencies = nil }},
		{"missing safety", func(s *schemaSnapshot) { s.Migration.Objects[0].SafetyReason = nil }},
		{"missing binding flag", func(s *schemaSnapshot) { s.Migration.Objects[0].Dependencies[0].SchemaBound = nil }},
		{"missing issue", func(s *schemaSnapshot) { s.Migration.Objects[0].Dependencies[0].Issue = nil }},
		{"missing reference target", func(s *schemaSnapshot) { s.Migration.Objects[0].Dependencies[0].Name = "missing" }},
		{"missing captured module", func(s *schemaSnapshot) { s.Migration.Objects = s.Migration.Objects[1:] }},
		{"missing captured table", func(s *schemaSnapshot) {
			for i, object := range s.Migration.Objects {
				if object.TypeCode == "U" {
					s.Migration.Objects = append(s.Migration.Objects[:i], s.Migration.Objects[i+1:]...)
					break
				}
			}
		}},
		{"mismatched type", func(s *schemaSnapshot) { s.Migration.Objects[0].TypeCode = "V" }},
		{"unknown schema", func(s *schemaSnapshot) { s.Migration.Objects[0].Schema = "unknown" }},
		{"duplicate object", func(s *schemaSnapshot) { s.Migration.Objects = append(s.Migration.Objects, s.Migration.Objects[0]) }},
		{"legacy migration payload", func(s *schemaSnapshot) { s.Version = 1 }},
		{"future version", func(s *schemaSnapshot) { s.Version = snapshotVersion + 1 }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			source := richSnapshotFixture()
			source.migration.dependencies[objectName{"dbo", "CLR"}] = []migrationDependency{{name: objectName{"dbo", "Orders"}}}
			snapshot := mustMakeSnapshot(t, source, time.Now())
			scenario.edit(snapshot)
			if restored, err := storeMigrationFixture(t, snapshot); err == nil || restored != nil {
				t.Fatal("partial migration payload must fail even a comparison-only read")
			}
		})
	}
}

func TestSnapshotMigrationRejectsDroppedCatalogMetadata(t *testing.T) {
	for _, scenario := range []struct {
		name string
		edit func(*migrationMetadata)
	}{
		{"wrong server", func(m *migrationMetadata) { m.serverName += "-other" }},
		{"wrong database", func(m *migrationMetadata) { m.databaseName += "-other" }},
		{"missing dependency catalog", func(m *migrationMetadata) { m.dependencies = nil }},
		{"missing safety catalog", func(m *migrationMetadata) { m.unsafeObjects = nil }},
		{"orphan dependency owner", func(m *migrationMetadata) {
			m.dependencies[objectName{"dbo", "missing"}] = []migrationDependency{{issue: "unresolved dependency"}}
		}},
		{"orphan safety owner", func(m *migrationMetadata) { m.unsafeObjects[objectName{"dbo", "missing"}] = "signed module" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			source := richSnapshotFixture()
			scenario.edit(source.migration)
			if snapshot, err := makeSnapshot(source, time.Now()); err == nil || snapshot != nil {
				t.Fatal("incomplete or mismatched catalog must not produce a migration snapshot")
			}
		})
	}
}

func TestSnapshotCompareOnlyVersionsRemainComparable(t *testing.T) {
	for _, version := range []int{1, snapshotVersion} {
		source := snapshotFixture()
		snapshot := mustMakeSnapshot(t, source, time.Now())
		snapshot.Version = version
		restored, err := storeMigrationFixture(t, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if restored.migration != nil {
			t.Fatalf("version %d compare-only snapshot acquired migration metadata", version)
		}
		if diffs := compareSchemas(source, restored, sqlModeStrict); len(diffs) != 0 {
			t.Fatalf("version %d snapshot changed comparison: %+v", version, diffs)
		}
		if _, _, err := planMigration(restored, restored, sqlModeStrict); err == nil {
			t.Fatalf("version %d compare-only snapshot must not authorize migration", version)
		}
	}
}
