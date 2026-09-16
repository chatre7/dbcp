package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeOfflineSnapshot(t *testing.T, path string, snapshot *schemaSnapshot) []byte {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestOfflineComparisonIgnoresLiveConfigurationAndCaptureIdentity(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MSSQL_SOURCE_DSN", "not a SQL Server connection string")
	t.Setenv("MSSQL_DESTINATION_DSN", "also not a SQL Server connection string")
	t.Setenv("MSSQL_GENERATE_MIGRATION", "not-a-boolean")
	t.Setenv("MSSQL_MIGRATION_OUT", "must-not-create.sql")
	if err := os.WriteFile(".env", []byte("MSSQL_SOURCE_DSN='unterminated"), 0600); err != nil {
		t.Fatal(err)
	}
	source, destination := snapshotFixture(), snapshotFixture()
	source.snapshot.Identity = databaseIdentity{"isolated-source", "SourceDatabase"}
	destination.snapshot.Identity = databaseIdentity{"isolated-destination", "DestinationDatabase"}
	destination.snapshot.Objects[0].ModifiedAt = "2026-09-15 10:20:30.123"
	captured := time.Date(2026, 9, 16, 15, 30, 0, 0, time.FixedZone("capture", 3*60*60))
	sourceBytes := writeOfflineSnapshot(t, "source.json", mustMakeSnapshot(t, source, captured))
	destinationBytes := writeOfflineSnapshot(t, "destination.json", mustMakeSnapshot(t, destination, captured.Add(time.Hour)))

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"-source-snapshot", "source.json", "-destination-snapshot", "destination.json", "-out", "report.txt",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("equal schemas from unrelated captures must compare offline: exit %d, stderr %s", code, &stderr)
	}
	report := stdout.String()
	if !strings.Contains(strings.ToLower(report), "offline") {
		t.Fatalf("report must distinguish captured schemas from a live comparison:\n%s", report)
	}
	for _, value := range []string{
		"isolated-source", "SourceDatabase", "isolated-destination", "DestinationDatabase",
		captured.UTC().Format(time.RFC3339), captured.Add(time.Hour).UTC().Format(time.RFC3339),
	} {
		if !strings.Contains(report, value) {
			t.Fatalf("report omits capture provenance %q:\n%s", value, report)
		}
	}
	written, err := os.ReadFile("report.txt")
	if err != nil || !bytes.Equal(written, stdout.Bytes()) {
		t.Fatalf("-out must copy the stdout report: %v", err)
	}
	for _, path := range []string{"data", "must-not-create.sql", "migration.sql"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("offline comparison must not create history or migrations at %s: %v", path, err)
		}
	}
	for path, before := range map[string][]byte{"source.json": sourceBytes, "destination.json": destinationBytes} {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("offline comparison modified input %s: %v", path, err)
		}
	}
}

func TestOfflineComparisonPreservesSQLModesAndDirection(t *testing.T) {
	t.Chdir(t.TempDir())
	clearDSNEnvironment(t)
	captured := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	source, destination := snapshotFixture(), snapshotFixture()
	name := objectName{"dbo", "Procedure"}
	procedure := destination.objects[name]
	procedure.definition = "ALTER PROCEDURE dbo.Procedure AS SELECT 1"
	destination.objects[name] = procedure
	writeOfflineSnapshot(t, "source.json", mustMakeSnapshot(t, source, captured))
	writeOfflineSnapshot(t, "destination.json", mustMakeSnapshot(t, destination, captured))
	args := []string{"-source-snapshot", "source.json", "-destination-snapshot", "destination.json"}

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr); code != 1 || !strings.Contains(stdout.String(), "[STORED_PROCEDURE_DEFINITION]") {
		t.Fatalf("strict comparison must retain declaration changes: exit %d, stdout %s, stderr %s", code, &stdout, &stderr)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), append(args, "-sql-mode", "normalized"), &stdout, &stderr); code != 0 {
		t.Fatalf("normalized comparison must ignore declaration-only changes: exit %d, stderr %s", code, &stderr)
	}

	procedure.definition = "ALTER PROCEDURE dbo.Procedure AS SELECT 2"
	destination.objects[name] = procedure
	destination.tables[objectName{"dbo", "Orders"}].columns["DestinationOnly"] = column{"int", false}
	writeOfflineSnapshot(t, "destination.json", mustMakeSnapshot(t, destination, captured))
	for _, mode := range []string{"strict", "normalized"} {
		stdout.Reset()
		stderr.Reset()
		code := run(context.Background(), append(args, "-sql-mode", mode), &stdout, &stderr)
		if code != 1 {
			t.Fatalf("%s must report real SQL and table changes: exit %d, stderr %s", mode, code, &stderr)
		}
		for _, fragment := range []string{
			"[COLUMN_ONLY_IN_DESTINATION]", "[dbo].[Orders].[DestinationOnly]", "[STORED_PROCEDURE_DEFINITION]",
			"-CREATE PROCEDURE dbo.Procedure AS SELECT 1", "+ALTER PROCEDURE dbo.Procedure AS SELECT 2",
		} {
			if !strings.Contains(stdout.String(), fragment) {
				t.Fatalf("%s lost a difference or reversed source/destination (%q):\n%s", mode, fragment, &stdout)
			}
		}
	}
}

func TestOfflineInputFlagsMustBePairedAndExcludeCapture(t *testing.T) {
	for _, args := range [][]string{
		{"-source-snapshot", "source.json"},
		{"-destination-snapshot", "destination.json"},
		{"-source-snapshot", "source.json", "-destination-snapshot", "destination.json", "-snapshot"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			clearDSNEnvironment(t)
			snapshot := mustMakeSnapshot(t, snapshotFixture(), time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
			writeOfflineSnapshot(t, "source.json", snapshot)
			writeOfflineSnapshot(t, "destination.json", snapshot)
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
				t.Fatalf("invalid mode selection must fail: exit %d, stderr %s", code, &stderr)
			}
			if stdout.Len() != 0 {
				t.Fatalf("invalid mode selection emitted a comparison report: %s", &stdout)
			}
		})
	}
}

func TestOfflineInvalidSnapshotPreservesExistingReport(t *testing.T) {
	for _, corruption := range []string{"malformed JSON", "trailing JSON", "unsupported version", "missing metadata"} {
		t.Run(corruption, func(t *testing.T) {
			t.Chdir(t.TempDir())
			clearDSNEnvironment(t)
			snapshot := mustMakeSnapshot(t, snapshotFixture(), time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
			writeOfflineSnapshot(t, "source.json", snapshot)
			switch corruption {
			case "unsupported version":
				snapshot.Version++
			case "missing metadata":
				snapshot.Timestamps = nil
			}
			data := writeOfflineSnapshot(t, "destination.json", snapshot)
			switch corruption {
			case "malformed JSON":
				data = []byte("{\"version\":")
			case "trailing JSON":
				data = append(data, []byte("\n{}")...)
			}
			if err := os.WriteFile("destination.json", data, 0600); err != nil {
				t.Fatal(err)
			}
			before := []byte("previous report must survive")
			if err := os.WriteFile("report.txt", before, 0600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{
				"-source-snapshot", "source.json", "-destination-snapshot", "destination.json", "-out", "report.txt",
			}, &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 {
				t.Fatalf("invalid snapshot must fail without a report: exit %d, stdout %s, stderr %s", code, &stdout, &stderr)
			}
			after, err := os.ReadFile("report.txt")
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("invalid snapshot replaced the existing report: %v", err)
			}
		})
	}
}

func TestOfflineOutputCannotOverwriteEitherInput(t *testing.T) {
	for _, alias := range []string{"source", "destination", "relative", "hardlink", "symlink"} {
		t.Run(alias, func(t *testing.T) {
			t.Chdir(t.TempDir())
			clearDSNEnvironment(t)
			snapshot := mustMakeSnapshot(t, snapshotFixture(), time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
			sourceBytes := writeOfflineSnapshot(t, "source.json", snapshot)
			destinationBytes := writeOfflineSnapshot(t, "destination.json", snapshot)
			output := "source.json"
			switch alias {
			case "destination":
				output = "destination.json"
			case "relative":
				if err := os.Mkdir("subdir", 0700); err != nil {
					t.Fatal(err)
				}
				output = "subdir" + string(filepath.Separator) + ".." + string(filepath.Separator) + "source.json"
			case "hardlink":
				output = "alias.json"
				if err := os.Link("destination.json", output); err != nil {
					t.Skipf("hard links unavailable: %v", err)
				}
			case "symlink":
				output = "alias.json"
				if err := os.Symlink("source.json", output); err != nil {
					t.Skipf("symbolic links unavailable: %v", err)
				}
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{
				"-source-snapshot", "source.json", "-destination-snapshot", "destination.json", "-out", output,
			}, &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 {
				t.Fatalf("output alias must fail without a report: exit %d, stdout %s, stderr %s", code, &stdout, &stderr)
			}
			for path, before := range map[string][]byte{"source.json": sourceBytes, "destination.json": destinationBytes} {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("output collision modified input %s: %v", path, err)
				}
			}
		})
	}
}
