package main

import (
	"strings"
	"testing"
)

func TestMigrationModuleRewritesStaleNameWithoutChangingBody(t *testing.T) {
	name := objectName{"sch]ema", "new'名"}
	body := " @value nvarchar(100) = N'dbo.Old' AS\nBEGIN\n    SELECT N'CREATE PROC dbo.Old; it''s unchanged\nGO\n'; -- dbo.Old\nEND;"
	for _, declaration := range []string{
		"CREATE PROC dbo.Old",
		"ALTER PROCEDURE [dbo].[Old]",
		"CREATE OR ALTER PROC \"dbo\".\"Ol\"\"d\"",
		"/* leading /* nested */ */\nCREATE /* retain */ OR -- retain too\nALTER PROC [旧].[Old]]name]",
	} {
		t.Run(declaration, func(t *testing.T) {
			got, err := migrationModuleDefinition(migrationStep{name: name, action: "ALTER", object: schemaObject{typeCode: "P", definition: declaration + body}})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(got, "[sch]]ema].[new'名]"+body) {
				t.Fatalf("name was not safely replaced or body changed: %q", got)
			}
			start, end, ok := nextSQLHeaderWord(got, 0, nil)
			if !ok || got[start:end] != "ALTER" {
				t.Fatalf("existing module must use ALTER: %q", got)
			}
			if strings.Contains(declaration, "/* retain */") && (!strings.Contains(got, "/* retain */") || !strings.Contains(got, "-- retain too\n")) {
				t.Fatalf("header comments were lost: %q", got)
			}
		})
	}
}

func TestMigrationModulePreservesCorrectQualifiedSpelling(t *testing.T) {
	for _, name := range []string{"dbo.Current", "[dbo].[Current]", "\"dbo\".\"Current\"", "dbo /* name comment */ . Current"} {
		definition := "CREATE\t/* header */\nPROC " + name + " AS SELECT N'Current';"
		got, err := migrationModuleDefinition(migrationStep{name: objectName{"dbo", "Current"}, action: "ALTER", object: schemaObject{typeCode: "P", definition: definition}})
		if err != nil {
			t.Fatal(err)
		}
		want := "ALTER" + strings.TrimPrefix(definition, "CREATE")
		if got != want {
			t.Fatalf("correct qualified declaration should change only its verb:\ngot  %q\nwant %q", got, want)
		}
	}
}

func TestMigrationModuleKindsAndCreation(t *testing.T) {
	for _, tc := range []struct{ kind, definition string }{
		{"P", "ALTER PROC Legacy AS SELECT 1;"},
		{"V", "ALTER VIEW 旧名 (Value) AS SELECT 1;"},
		{"FN", "CREATE OR ALTER FUNCTION [Old]() RETURNS int AS BEGIN RETURN 1; END;"},
		{"IF", "ALTER FUNCTION dbo.Old() RETURNS TABLE AS RETURN (SELECT 1 AS Value);"},
		{"TF", "ALTER FUNCTION dbo.Old() RETURNS @T TABLE (Value int) AS BEGIN RETURN; END;"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			got, err := migrationModuleDefinition(migrationStep{name: objectName{"dbo", "New"}, action: "CREATE", object: schemaObject{typeCode: tc.kind, definition: tc.definition}})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(got, "CREATE ") || !strings.Contains(got, "[dbo].[New]") {
				t.Fatalf("missing module must be created under its catalog name: %q", got)
			}
		})
	}
}

func TestMigrationModuleRejectsUnsafeDeclarations(t *testing.T) {
	for _, definition := range []string{
		"SELECT N'CREATE PROC dbo.Old AS SELECT 1'",
		"CREATE TABLE dbo.Old (id int)",
		"CREATE VIEW dbo.Old AS SELECT 1 AS Value",
		"CREATE OR DROP PROC dbo.Old AS SELECT 1",
		"CREATE PROC otherdb.dbo.Old AS SELECT 1",
		"CREATE PROC dbo..Old AS SELECT 1",
		"CREATE PROC dbo.Old;2 AS SELECT 1",
		"CREATE PROC dbo.Old /* comment */ ; 2 AS SELECT 1",
		"CREATE PROC dbo.Old; DROP TABLE dbo.T",
		"CREATE PROC dbo.Old ! AS SELECT 1",
		"CREATE PROC [unterminated AS SELECT 1",
		"CREATE PROC \"unterminated AS SELECT 1",
		"CREATE PROC [] AS SELECT 1",
		"CREATE PROC 123 AS SELECT 1",
		"CREATE PROC dbo.Old /* unterminated",
		"/* unterminated CREATE PROC dbo.Old AS SELECT 1",
		"CREATE PROC dbo.Old",
	} {
		t.Run(definition, func(t *testing.T) {
			if got, err := migrationModuleDefinition(migrationStep{name: objectName{"dbo", "New"}, action: "CREATE", object: schemaObject{typeCode: "P", definition: definition}}); err == nil {
				t.Fatalf("unsafe declaration rendered without error: %q", got)
			}
		})
	}
}

func TestMigrationSynonymTargetQuotesIdentifiers(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"dbo.Target", "[dbo].[Target]"},
		{"[server's].[db]]name].[名].\"table\"\"name\"", "[server's].[db]]name].[名].[table\"name]"},
		{"\"name.with.dots\"", "[name.with.dots]"},
		{"[dbo].[x]]; DROP TABLE dbo.T;--]", "[dbo].[x]]; DROP TABLE dbo.T;--]"},
		{" /* safe */ dbo /* nested /* safe */ */ . Target -- safe", "[dbo].[Target]"},
	} {
		got, err := migrationSynonymTarget(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("target %q: got %q, error %v; want %q", tc.input, got, err, tc.want)
		}
	}
	for _, target := range []string{
		"", "dbo.", "db..Target", "a.b.c.d.e", "dbo.Target; DROP TABLE dbo.T", "dbo.Target extra", "[bad", "\"bad", "[]", "dbo.123", "dbo.[a\x00b]", "dbo.Target /* unfinished", "'dbo.Target'",
	} {
		if got, err := migrationSynonymTarget(target); err == nil {
			t.Errorf("unsafe synonym target %q accepted as %q", target, got)
		}
	}
}

func TestMigrationSQLRejectsUnknownDestinationAndActions(t *testing.T) {
	for _, destination := range []*schema{nil, {}, {migration: &migrationMetadata{databaseName: "db"}}, {migration: &migrationMetadata{serverName: "server"}}} {
		if sql, err := renderMigrationSQL(destination, nil, nil); err == nil || sql != "" {
			t.Fatalf("unknown destination identity must not produce a script: %q, %v", sql, err)
		}
	}
	destination := &schema{migration: &migrationMetadata{databaseName: "db", serverName: "server"}}
	for _, object := range []schemaObject{{typeCode: "SN", target: "dbo.T"}, {typeCode: "P", definition: "CREATE PROC dbo.P AS SELECT 1"}} {
		if sql, err := renderMigrationSQL(destination, []migrationStep{{name: objectName{"dbo", "P"}, object: object, action: "DROP"}}, nil); err == nil || sql != "" {
			t.Fatalf("destructive unsupported action must not produce a script: %q, %v", sql, err)
		}
	}
}
