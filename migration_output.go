package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func validateMigrationOutput(migrationPath, reportPath string) error {
	for _, pair := range [][2]string{{migrationPath, ".env"}, {reportPath, ".env"}, {migrationPath, reportPath}} {
		if pair[0] == "" || pair[1] == "" {
			continue
		}
		same, err := sameOutputPath(pair[0], pair[1])
		if err != nil {
			return err
		}
		if same {
			return fmt.Errorf("migration, report and .env must use different files")
		}
	}
	return nil
}

func sameOutputPath(a, b string) (bool, error) {
	x, err := filepath.Abs(a)
	if err != nil {
		return false, fmt.Errorf("resolve output path: %w", err)
	}
	y, err := filepath.Abs(b)
	if err != nil {
		return false, fmt.Errorf("resolve output path: %w", err)
	}
	if x == y || runtime.GOOS == "windows" && strings.EqualFold(x, y) {
		return true, nil
	}
	xInfo, xErr := os.Stat(x)
	yInfo, yErr := os.Stat(y)
	if xErr != nil && !os.IsNotExist(xErr) {
		return false, fmt.Errorf("inspect output path: %w", xErr)
	}
	if yErr != nil && !os.IsNotExist(yErr) {
		return false, fmt.Errorf("inspect output path: %w", yErr)
	}
	return xErr == nil && yErr == nil && os.SameFile(xInfo, yInfo), nil
}

// Write to a sibling temporary file first: planning, rendering or write errors
// must not truncate a previously reviewed migration script.
func writeMigrationFile(path, script string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".migration-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := io.WriteString(file, script); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
