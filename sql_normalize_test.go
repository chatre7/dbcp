package main

import "testing"

func compareSQLDefinitions(source, destination string, mode sqlCompareMode) []difference {
	name := objectName{"dbo", "Example"}
	return compareSchemas(
		&schema{objects: map[objectName]schemaObject{name: {typeCode: "P", definition: source}}},
		&schema{objects: map[objectName]schemaObject{name: {typeCode: "P", definition: destination}}},
		mode,
	)
}

func TestSQLModeNormalizesDeclarationNotBody(t *testing.T) {
	cases := []struct {
		name, source, destination string
	}{
		{"alter", "CREATE PROCEDURE dbo.Example AS SELECT 1;", "ALTER PROCEDURE dbo.Example AS SELECT 1;"},
		{"create-or-alter", "CREATE VIEW dbo.Example AS SELECT 1 AS Value;", "create\tOr\nAlter VIEW dbo.Example AS SELECT 1 AS Value;"},
		{"nested-leading-comment", "/* ALTER /* CREATE */ FUNCTION */\nCREATE FUNCTION dbo.Example() RETURNS int AS BEGIN RETURN 1; END;", "/* ALTER /* CREATE */ FUNCTION */\nALTER FUNCTION dbo.Example() RETURNS int AS BEGIN RETURN 1; END;"},
		{"comments-in-declaration", "CREATE /* keep /* nested */ */ -- keep too\nPROC dbo.Example AS SELECT 1;", "CREATE /* keep /* nested */ */ OR -- keep too\nALTER PROC dbo.Example AS SELECT 1;"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if diffs := compareSQLDefinitions(tc.source, tc.destination, sqlModeStrict); len(diffs) != 1 {
				t.Fatalf("strict mode must report declaration text changes: %+v", diffs)
			}
			if diffs := compareSQLDefinitions(tc.source, tc.destination, sqlModeNormalized); len(diffs) != 0 {
				t.Fatalf("normalized declaration should match: %+v", diffs)
			}
		})
	}
}

func TestNormalizedModePreservesMeaningfulText(t *testing.T) {
	cases := []struct {
		name, source, destination string
	}{
		{"literal-spaces", "CREATE PROC dbo.Example AS SELECT N'a  b';", "ALTER PROC dbo.Example AS SELECT N'a b';"},
		{"escaped-literal", "CREATE PROC dbo.Example AS SELECT 'it''s CREATE OR ALTER';", "ALTER PROC dbo.Example AS SELECT 'it''s CREATE';"},
		{"identifier", "CREATE PROC dbo.Example AS SELECT [CREATE OR ALTER] FROM dbo.T;", "ALTER PROC dbo.Example AS SELECT [CREATE] FROM dbo.T;"},
		{"header-comment", "CREATE /* original */ PROC dbo.Example AS SELECT 1;", "ALTER /* changed */ PROC dbo.Example AS SELECT 1;"},
		{"body-comment", "CREATE PROC dbo.Example AS SELECT 1; -- CREATE OR ALTER", "ALTER PROC dbo.Example AS SELECT 1; -- CREATE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diffs := compareSQLDefinitions(tc.source, tc.destination, sqlModeNormalized)
			if len(diffs) != 1 || diffs[0].kind != "STORED_PROCEDURE_DEFINITION" {
				t.Fatalf("normalization hid a change outside the declaration verbs: %+v", diffs)
			}
		})
	}
}

func TestUnrecognizedDeclarationIsNotRewritten(t *testing.T) {
	cases := []string{
		"SELECT N'ALTER PROC dbo.Example';",
		"[ALTER] PROC dbo.Example AS SELECT 1;",
		"CREATE OR [ALTER] PROC dbo.Example AS SELECT 1;",
		"ALTER PROCEDURE_name AS SELECT 1;",
		"ALTER TABLE dbo.Example ADD Value int;",
		"/* unfinished /* nested */ ALTER PROC dbo.Example AS SELECT 1;",
	}
	for _, sql := range cases {
		if got := normalizeSQLDeclaration(sql); got != sql {
			t.Fatalf("unrecognized input must remain strictly comparable:\ninput: %q\nactual: %q", sql, got)
		}
	}
}
