package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func offlineMigrationFixture(database string, withObjects bool) *schema {
	objects := make(map[objectName]schemaObject)
	if withObjects {
		objects[objectName{"dbo", "z_base"}] = schemaObject{typeCode: "V", definition: "CREATE VIEW dbo.z_base AS SELECT 1 AS ID", ansiNulls: true, quotedIdentifier: true}
		objects[objectName{"dbo", "m_alias"}] = schemaObject{typeCode: "SN", target: "dbo.z_base"}
		objects[objectName{"dbo", "a_reader"}] = schemaObject{typeCode: "P", definition: "CREATE PROCEDURE dbo.a_reader AS SELECT ID FROM dbo.m_alias", ansiNulls: true, quotedIdentifier: true}
	}
	result := migrationPlanSchema(objects)
	result.snapshot = &snapshotMetadata{Identity: databaseIdentity{"isolated-server", database}, Objects: []objectTimestamp{}}
	result.migration.serverName, result.migration.databaseName = "isolated-server", database
	for name, object := range objects {
		result.snapshot.Objects = append(result.snapshot.Objects, objectTimestamp{
			Schema: name.schema, Name: name.name, Type: object.typeCode,
			CreatedAt: "2026-09-16 10:00:00.000", ModifiedAt: "2026-09-16 10:00:00.000",
		})
	}
	if withObjects {
		result.migration.dependencies[objectName{"dbo", "a_reader"}] = []migrationDependency{{name: objectName{"dbo", "m_alias"}}}
		result.migration.dependencies[objectName{"dbo", "m_alias"}] = []migrationDependency{{name: objectName{"dbo", "z_base"}}}
	}
	return result
}

func TestOfflineMigrationRefusesInsufficientOrUnsafeSnapshotsWithoutReplacingOutputs(t *testing.T) {
	for _, scenario := range []string{"legacy source", "compare-only destination", "unresolved dependency", "unsafe source"} {
		t.Run(scenario, func(t *testing.T) {
			t.Chdir(t.TempDir())
			at := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
			source, destination := offlineMigrationFixture("SourceDB", true), offlineMigrationFixture("TargetDB", false)
			switch scenario {
			case "legacy source":
				source.migration = nil
			case "compare-only destination":
				destination.migration = nil
			case "unresolved dependency":
				source.migration.dependencies[objectName{"dbo", "z_base"}] = []migrationDependency{{issue: "unresolved external prerequisite"}}
			case "unsafe source":
				source.migration.unsafeObjects[objectName{"dbo", "z_base"}] = "signed module requires manual migration"
			}
			sourceSnapshot := mustMakeSnapshot(t, source, at)
			if scenario == "legacy source" {
				sourceSnapshot.Version = 1
			}
			writeOfflineSnapshot(t, "source.json", sourceSnapshot)
			writeOfflineSnapshot(t, "destination.json", mustMakeSnapshot(t, destination, at))
			for _, path := range []string{"migration.sql", "report.html"} {
				if err := os.WriteFile(path, []byte("previous reviewed output"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			args := []string{"-source-snapshot", "source.json", "-destination-snapshot", "destination.json", "-migration-out", "migration.sql", "-out", "report.html", "-format", "html"}
			if code := run(context.Background(), args, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
				t.Fatalf("unsafe or incomplete migration must fail: %d, %s", code, &stderr)
			}
			for _, path := range []string{"migration.sql", "report.html"} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "previous reviewed output" {
					t.Fatalf("failed planning replaced %s: %v", path, err)
				}
			}
		})
	}
}

func TestOfflineMigrationRejectsOutputAliases(t *testing.T) {
	t.Chdir(t.TempDir())
	at := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	writeOfflineSnapshot(t, "source.json", mustMakeSnapshot(t, offlineMigrationFixture("SourceDB", true), at))
	original := writeOfflineSnapshot(t, "destination.json", mustMakeSnapshot(t, offlineMigrationFixture("TargetDB", false), at))
	if err := os.Link("destination.json", "alias.sql"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"-source-snapshot", "source.json", "-destination-snapshot", "destination.json", "-migration-out", "alias.sql"}
	if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
		t.Fatalf("SQL output must not alias a snapshot: %d, %s", code, &stderr)
	}
	after, err := os.ReadFile("destination.json")
	if err != nil || !bytes.Equal(original, after) {
		t.Fatalf("SQL output changed destination input: %v", err)
	}
	if err := os.Mkdir("real", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", "linked"); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	args = []string{"-source-snapshot", "source.json", "-destination-snapshot", "destination.json", "-migration-out", filepath.Join("real", "new.out"), "-out", filepath.Join("linked", "new.out")}
	if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
		t.Fatalf("new report and SQL paths through aliased parents must fail: %d, %s", code, &stderr)
	}
	if _, err := os.Stat(filepath.Join("real", "new.out")); !os.IsNotExist(err) {
		t.Fatalf("conflicting outputs must not create a misleading file: %v", err)
	}
}
