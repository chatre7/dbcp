package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type appConfig struct {
	sourceDSN         string
	destinationDSN    string
	generateMigration bool
	migrationOut      string
}

func loadConfig() (appConfig, error) {
	var values map[string]string
	content, err := os.ReadFile(".env")
	if err != nil {
		if !os.IsNotExist(err) {
			return appConfig{}, fmt.Errorf("read .env: %w", err)
		}
	} else {
		// Accept UTF-8 BOM files written by Windows editors/PowerShell.
		content = bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})
		values, err = godotenv.UnmarshalBytes(content)
		if err != nil {
			// Parser errors can include the entire offending line and password.
			return appConfig{}, fmt.Errorf("parse .env: invalid syntax; check KEY=value entries and matching quotes")
		}
	}
	get := func(name string) string {
		if value, exists := os.LookupEnv(name); exists {
			return value
		}
		return values[name]
	}
	config := appConfig{
		sourceDSN: get("MSSQL_SOURCE_DSN"), destinationDSN: get("MSSQL_DESTINATION_DSN"),
		migrationOut: strings.TrimSpace(get("MSSQL_MIGRATION_OUT")),
	}
	if enabled := strings.TrimSpace(get("MSSQL_GENERATE_MIGRATION")); enabled != "" {
		config.generateMigration, err = strconv.ParseBool(enabled)
		if err != nil {
			return appConfig{}, fmt.Errorf("MSSQL_GENERATE_MIGRATION must be a boolean, e.g. true or false")
		}
	}
	if config.migrationOut == "" {
		config.migrationOut = "migration.sql"
	}
	return config, nil
}
