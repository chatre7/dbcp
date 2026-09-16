package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

type offlineSnapshotReference struct {
	Server     string
	Database   string
	CapturedAt string
}

type offlineReportContext struct {
	Source             offlineSnapshotReference
	Destination        offlineSnapshotReference
	MigrationGenerated bool
}

func offlineReference(snapshot *schemaSnapshot) offlineSnapshotReference {
	return offlineSnapshotReference{
		Server: snapshot.Identity.Server, Database: snapshot.Identity.Database,
		CapturedAt: snapshot.CapturedAt.UTC().Format(time.RFC3339Nano),
	}
}

func runOfflineComparison(sourcePath, destinationPath, output, format, migrationOutput string, mode sqlCompareMode, stdout, stderr io.Writer) (int, error) {
	sourceSnapshot, source, err := readSnapshot(sourcePath)
	if err != nil {
		return 2, fmt.Errorf("source snapshot: %w", err)
	}
	destinationSnapshot, destination, err := readSnapshot(destinationPath)
	if err != nil {
		return 2, fmt.Errorf("destination snapshot: %w", err)
	}
	if output != "" {
		if err := validateOfflineOutput(output, sourcePath, destinationPath); err != nil {
			return 2, err
		}
	}
	var migrationSQL string
	var migrationSteps []migrationStep
	if migrationOutput != "" {
		if err := validateOfflineOutput(migrationOutput, sourcePath, destinationPath); err != nil {
			return 2, err
		}
		if err := validateMigrationOutput(migrationOutput, output); err != nil {
			return 2, err
		}
		if source.migration == nil || destination.migration == nil {
			return 2, errors.New("offline migration requires both snapshots exported with -snapshot -include-migration; legacy or compare-only snapshots must be re-exported")
		}
		var notes []string
		migrationSteps, notes, err = planMigration(source, destination, mode)
		if err != nil {
			return 2, fmt.Errorf("plan offline migration: %w", err)
		}
		notes = append(notes,
			"Generated offline. Source captured at "+sourceSnapshot.CapturedAt.UTC().Format(time.RFC3339Nano)+
				"; destination captured at "+destinationSnapshot.CapturedAt.UTC().Format(time.RFC3339Nano)+".",
			"Destination may have changed since capture. Re-export/review before executing; database/server guards do not detect schema drift.")
		migrationSQL, err = renderMigrationSQL(destination, migrationSteps, notes)
		if err != nil {
			return 2, fmt.Errorf("render offline migration: %w", err)
		}
	}
	// Different database identities are expected across isolated environments.
	// Identity and capture times annotate the report; only schemas are compared.
	diffs := compareSchemas(source, destination, mode)
	context := reportContext{Offline: &offlineReportContext{
		Source: offlineReference(sourceSnapshot), Destination: offlineReference(destinationSnapshot),
		MigrationGenerated: migrationOutput != "",
	}}
	var report string
	if format == "html" {
		report, err = renderHTMLReport(source, destination, diffs, mode, context)
		if err != nil {
			return 2, fmt.Errorf("render offline report: %w", err)
		}
	} else {
		report = renderReport(source, destination, diffs, mode, context)
	}
	if migrationOutput != "" {
		if err := writeMigrationFile(migrationOutput, migrationSQL); err != nil {
			return 2, fmt.Errorf("write offline migration: %w", err)
		}
		fmt.Fprintf(stderr, "Migration script: %s (%d operation(s)); generated offline, review capture-time drift before manual execution.\n", migrationOutput, len(migrationSteps))
	}
	if output != "" {
		if err := os.WriteFile(output, []byte(report), 0600); err != nil {
			return 2, fmt.Errorf("write offline report: %w", err)
		}
	}
	if _, err := io.WriteString(stdout, report); err != nil {
		return 2, fmt.Errorf("write stdout: %w", err)
	}
	if len(diffs) != 0 {
		return 1, nil
	}
	return 0, nil
}

func validateOfflineOutput(output string, inputs ...string) error {
	out, err := os.Stat(output)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect offline report path: %w", err)
	}
	for _, path := range inputs {
		input, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("inspect snapshot input: %w", err)
		}
		// SameFile resolves hard links as well as relative paths and symlinks.
		if os.SameFile(out, input) {
			return errors.New("report and migration outputs must not overwrite either input snapshot; choose different output files")
		}
	}
	return nil
}
