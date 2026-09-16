package main

import "testing"

func TestModuleComparisonPreservesLiteralWhitespace(t *testing.T) {
	name := objectName{"dbo", "Message"}
	src := schemaObject{typeCode: "P", definition: "CREATE PROCEDURE dbo.Message AS\r\nSELECT N'a  b -- /* literal */';\r\n"}
	dst := src
	dst.definition = "CREATE PROCEDURE dbo.Message AS\nSELECT N'a  b -- /* literal */';\n"
	source := &schema{objects: map[objectName]schemaObject{name: src}}
	destination := &schema{objects: map[objectName]schemaObject{name: dst}}
	if diffs := compareSchemas(source, destination, sqlModeStrict); len(diffs) != 0 {
		t.Fatalf("line-ending-only changes should be ignored: %+v", diffs)
	}
	dst.definition = "CREATE PROCEDURE dbo.Message AS\nSELECT N'a b -- /* literal */';\n"
	destination.objects[name] = dst
	diffs := compareSchemas(source, destination, sqlModeStrict)
	if len(diffs) != 1 || diffs[0].kind != "STORED_PROCEDURE_DEFINITION" {
		t.Fatalf("whitespace inside a SQL string changes its value and must be reported: %+v", diffs)
	}
}

func TestModuleSettingsDifferEvenWithIdenticalSQL(t *testing.T) {
	name := objectName{"dbo", "Message"}
	src := schemaObject{typeCode: "P", definition: "CREATE PROCEDURE dbo.Message AS SELECT 1;", ansiNulls: true, quotedIdentifier: true}
	dst := src
	dst.ansiNulls = false
	dst.quotedIdentifier = false
	diffs := compareSchemas(
		&schema{objects: map[objectName]schemaObject{name: src}},
		&schema{objects: map[objectName]schemaObject{name: dst}},
		sqlModeStrict,
	)
	if len(diffs) != 2 || diffs[0].kind != "STORED_PROCEDURE_ANSI_NULLS" || diffs[1].kind != "STORED_PROCEDURE_QUOTED_IDENTIFIER" {
		t.Fatalf("captured SET options are independent of the SQL definition: %+v", diffs)
	}
}
