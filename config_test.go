package main

import (
	"os"
	"strings"
	"testing"
)

func clearDSNEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"MSSQL_SOURCE_DSN", "MSSQL_DESTINATION_DSN", "MSSQL_GENERATE_MIGRATION", "MSSQL_MIGRATION_OUT"} {
		t.Setenv(name, "") // Register restoration before removing the variable.
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDotEnvPreservesQuotedCredentialsAndUTF8BOM(t *testing.T) {
	t.Chdir(t.TempDir())
	clearDSNEnvironment(t)
	source := `server=HOST\SQL2019;database=ข้อมูล;password=p$a # x\y;encrypt=true`
	destination := `server=DEST;database=Target;password=other=value;encrypt=true`
	content := "\ufeff# Windows UTF-8 file\r\nexport MSSQL_SOURCE_DSN='" + source + "'\r\nMSSQL_DESTINATION_DSN='" + destination + "' # comment\r\n"
	if err := os.WriteFile(".env", []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.sourceDSN != source || config.destinationDSN != destination {
		t.Fatal("dotenv changed a quoted connection string")
	}
}

func TestEnvironmentOverridesDotEnvIncludingEmptyValues(t *testing.T) {
	t.Chdir(t.TempDir())
	clearDSNEnvironment(t)
	if err := os.WriteFile(".env", []byte("MSSQL_SOURCE_DSN='file-source'\nMSSQL_DESTINATION_DSN='file-destination'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MSSQL_SOURCE_DSN", "environment-source")
	config, err := loadConfig()
	if err != nil || config.sourceDSN != "environment-source" || config.destinationDSN != "file-destination" {
		t.Fatal("environment must override only the variables that are set")
	}
	t.Setenv("MSSQL_SOURCE_DSN", "")
	config, err = loadConfig()
	if err != nil || config.sourceDSN != "" || config.destinationDSN != "file-destination" {
		t.Fatal("an explicitly empty environment variable must not fall back to .env")
	}
}

func TestMalformedDotEnvDoesNotExposeCredentials(t *testing.T) {
	t.Chdir(t.TempDir())
	clearDSNEnvironment(t)
	secret := "NeverPrintThisPassword"
	if err := os.WriteFile(".env", []byte("MSSQL_SOURCE_DSN='server=host;password="+secret), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig()
	if err == nil {
		t.Fatal("unterminated quoted value must be rejected")
	}
	if strings.Contains(err.Error(), secret) || config.sourceDSN != "" || config.destinationDSN != "" {
		t.Fatal("parse failure must not expose credentials or partial configuration")
	}
}
