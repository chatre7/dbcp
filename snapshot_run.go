package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type snapshotReportContext struct {
	Server     string
	Database   string
	PreviousAt string
	CurrentAt  string
	Baseline   bool
	Objects    []objectTimestamp
}

func (o objectTimestamp) Object() string {
	return (objectName{schema: o.Schema, name: o.Name}).String()
}

func runSnapshot(ctx context.Context, dsn, dataDir, output, format string, mode sqlCompareMode, stdout, stderr io.Writer) (int, error) {
	// An optional convenience copy must not overwrite the protected history.
	if output != "" {
		root, err := filepath.Abs(dataDir)
		if err != nil {
			return 2, fmt.Errorf("resolve snapshot directory: %w", err)
		}
		path, err := filepath.Abs(output)
		if err != nil {
			return 2, fmt.Errorf("resolve report path: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		} else if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
			path = filepath.Join(parent, filepath.Base(path))
		}
		if relative, err := filepath.Rel(root, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return 2, fmt.Errorf("-out must be outside -data-dir; a report is already saved inside each snapshot folder")
		}
	}
	current, err := loadSchema(ctx, dsn, false, true)
	if err != nil {
		return 2, fmt.Errorf("capture source schema: %w", err)
	}
	captured, err := makeSnapshot(current, time.Now().UTC())
	if err != nil {
		return 2, err
	}
	previous, previousPath, err := findPreviousSnapshot(dataDir, captured.Identity)
	if err != nil {
		return 2, err
	}
	history := &snapshotReportContext{
		Server: captured.Identity.Server, Database: captured.Identity.Database,
		CurrentAt: captured.CapturedAt.UTC().Format(time.RFC3339Nano), Baseline: previous == nil,
		Objects: slices.Clone(current.snapshot.Objects),
	}
	slices.SortFunc(history.Objects, func(a, b objectTimestamp) int {
		if order := strings.Compare(b.ModifiedAt, a.ModifiedAt); order != 0 {
			return order
		}
		if order := strings.Compare(a.Schema, b.Schema); order != 0 {
			return order
		}
		return strings.Compare(a.Name, b.Name)
	})
	before := &schema{}
	var diffs []difference
	if previous != nil {
		before, err = previous.restore()
		if err != nil {
			return 2, fmt.Errorf("restore previous snapshot: %w", err)
		}
		history.PreviousAt = previous.CapturedAt.UTC().Format(time.RFC3339Nano)
		diffs = compareSchemas(before, current, mode)
		for i := range diffs {
			diffs[i].kind = strings.ReplaceAll(diffs[i].kind, "_ONLY_IN_SOURCE", "_REMOVED")
			diffs[i].kind = strings.ReplaceAll(diffs[i].kind, "_ONLY_IN_DESTINATION", "_ADDED")
		}
	}
	var report string
	if format == "html" {
		report, err = renderHTMLReport(before, current, diffs, mode, history)
		if err != nil {
			return 2, fmt.Errorf("render snapshot report: %w", err)
		}
	} else {
		report = renderReport(before, current, diffs, mode, history)
	}
	if output != "" {
		if err := os.WriteFile(output, []byte(report), 0600); err != nil {
			return 2, fmt.Errorf("write report copy: %w", err)
		}
	}
	reportPath, err := saveSnapshot(dataDir, captured, report, format)
	if err != nil {
		return 2, err
	}
	if previousPath == "" {
		fmt.Fprintln(stderr, "First snapshot: baseline saved; no historical comparison was performed.")
	} else {
		fmt.Fprintf(stderr, "Previous snapshot: %s\n", previousPath)
	}
	fmt.Fprintf(stderr, "Snapshot report: %s\n", reportPath)
	if _, err := io.WriteString(stdout, report); err != nil {
		return 2, fmt.Errorf("write stdout (snapshot already saved): %w", err)
	}
	if len(diffs) != 0 {
		return 1, nil
	}
	return 0, nil
}
