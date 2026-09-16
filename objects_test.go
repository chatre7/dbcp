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

func TestCLRComparisonIgnoresSQLDefinitionAndSettings(t *testing.T) {
	name := objectName{"dbo", "ReadJSON"}
	for _, typeCode := range []string{"FS", "FT"} {
		for _, mode := range []sqlCompareMode{sqlModeStrict, sqlModeNormalized} {
			t.Run(typeCode+"/"+string(mode), func(t *testing.T) {
				metadata := clrFunction{
					assemblyName: "JSON", assemblyIdentity: "JSON, Version=1.0.0.0",
					assemblySHA256: "same DLL hash", permissionSet: "SAFE",
					className: "JSON.Functions", methodName: "Read", signature: "(@json nvarchar(max)) RETURNS nvarchar(max)",
					executeAs: "CALLER", nullOnNullInput: true,
				}
				destinationMetadata := metadata
				src := schemaObject{typeCode: typeCode, clr: &metadata, definition: "SQL source", ansiNulls: true, quotedIdentifier: true}
				dst := schemaObject{typeCode: typeCode, clr: &destinationMetadata, definition: "different SQL destination"}
				diffs := compareSchemas(
					&schema{objects: map[objectName]schemaObject{name: src}},
					&schema{objects: map[objectName]schemaObject{name: dst}},
					mode,
				)
				if len(diffs) != 0 {
					t.Fatalf("identical CLR metadata must compare equal regardless of SQL-only fields: %+v", diffs)
				}
			})
		}
	}
}

func TestCLRComparisonReportsMetadataChanges(t *testing.T) {
	name := objectName{"dbo", "ReadJSON"}
	original := clrFunction{
		assemblyName: "JSON", assemblyIdentity: "JSON, Version=1.0.0.0",
		assemblySHA256: "old DLL hash", permissionSet: "SAFE",
		className: "JSON.Functions", methodName: "Read", signature: "(@json nvarchar(max)) RETURNS nvarchar(max)",
		executeAs: "CALLER", nullOnNullInput: true,
	}
	tests := []struct {
		kind, source, destination string
		change                    func(*clrFunction)
	}{
		{"CLR_FUNCTION_ASSEMBLY_NAME", original.assemblyName, "OtherJSON", func(c *clrFunction) { c.assemblyName = "OtherJSON" }},
		{"CLR_FUNCTION_ASSEMBLY_IDENTITY", original.assemblyIdentity, "JSON, Version=2.0.0.0", func(c *clrFunction) { c.assemblyIdentity = "JSON, Version=2.0.0.0" }},
		{"CLR_FUNCTION_ASSEMBLY_SHA256", original.assemblySHA256, "new DLL hash", func(c *clrFunction) { c.assemblySHA256 = "new DLL hash" }},
		{"CLR_FUNCTION_PERMISSION_SET", original.permissionSet, "EXTERNAL_ACCESS", func(c *clrFunction) { c.permissionSet = "EXTERNAL_ACCESS" }},
		{"CLR_FUNCTION_CLASS", original.className, "JSON.OtherFunctions", func(c *clrFunction) { c.className = "JSON.OtherFunctions" }},
		{"CLR_FUNCTION_METHOD", original.methodName, "ReadOther", func(c *clrFunction) { c.methodName = "ReadOther" }},
		{"CLR_FUNCTION_SIGNATURE", original.signature, "(@json nvarchar(100)) RETURNS nvarchar(max)", func(c *clrFunction) { c.signature = "(@json nvarchar(100)) RETURNS nvarchar(max)" }},
		{"CLR_FUNCTION_EXECUTE_AS", original.executeAs, "OWNER", func(c *clrFunction) { c.executeAs = "OWNER" }},
		{"CLR_FUNCTION_NULL_ON_NULL_INPUT", "ON", "OFF", func(c *clrFunction) { c.nullOnNullInput = false }},
	}
	for _, typeCode := range []string{"FS", "FT"} {
		for _, tt := range tests {
			t.Run(typeCode+"/"+tt.kind, func(t *testing.T) {
				changed := original
				tt.change(&changed)
				diffs := compareSchemas(
					&schema{objects: map[objectName]schemaObject{name: {typeCode: typeCode, clr: &original}}},
					&schema{objects: map[objectName]schemaObject{name: {typeCode: typeCode, clr: &changed}}},
					sqlModeStrict,
				)
				want := difference{tt.kind, name.String(), tt.source, tt.destination}
				if len(diffs) != 1 || diffs[0] != want {
					t.Fatalf("want metadata difference %+v, got %+v", want, diffs)
				}
			})
		}
	}
}

func TestCLRComparisonPreservesPresenceAndTypePrecedence(t *testing.T) {
	name := objectName{"dbo", "ReadJSON"}
	tests := []struct {
		name                string
		source, destination map[objectName]schemaObject
		want                difference
	}{
		{"source only", map[objectName]schemaObject{name: {typeCode: "FS"}}, nil,
			difference{"FUNCTION_ONLY_IN_SOURCE", name.String(), "present", "<missing>"}},
		{"destination only", nil, map[objectName]schemaObject{name: {typeCode: "FT"}},
			difference{"FUNCTION_ONLY_IN_DESTINATION", name.String(), "<missing>", "present"}},
		{"SQL to CLR", map[objectName]schemaObject{name: {typeCode: "FN", definition: "SQL"}},
			map[objectName]schemaObject{name: {typeCode: "FS"}},
			difference{"OBJECT_TYPE", name.String(), "FN", "FS"}},
		{"CLR to SQL", map[objectName]schemaObject{name: {typeCode: "FT"}},
			map[objectName]schemaObject{name: {typeCode: "TF", definition: "SQL"}},
			difference{"OBJECT_TYPE", name.String(), "FT", "TF"}},
		{"CLR scalar to table", map[objectName]schemaObject{name: {typeCode: "FS"}},
			map[objectName]schemaObject{name: {typeCode: "FT"}},
			difference{"OBJECT_TYPE", name.String(), "FS", "FT"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diffs := compareSchemas(&schema{objects: tt.source}, &schema{objects: tt.destination}, sqlModeStrict)
			if len(diffs) != 1 || diffs[0] != tt.want {
				t.Fatalf("presence and type changes must take precedence over metadata: want %+v, got %+v", tt.want, diffs)
			}
		})
	}
}

func TestCLRComparisonDoesNotTreatUnavailableMetadataAsEqual(t *testing.T) {
	name := objectName{"dbo", "ReadJSON"}
	metadata := &clrFunction{assemblyName: "JSON"}
	for _, tt := range []struct {
		name                string
		source, destination *clrFunction
	}{
		{"source unavailable", nil, metadata},
		{"destination unavailable", metadata, nil},
		{"both unavailable", nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			diffs := compareSchemas(
				&schema{objects: map[objectName]schemaObject{name: {typeCode: "FS", clr: tt.source}}},
				&schema{objects: map[objectName]schemaObject{name: {typeCode: "FS", clr: tt.destination}}},
				sqlModeStrict,
			)
			if len(diffs) != 1 || diffs[0].kind != "CLR_FUNCTION_METADATA" {
				t.Fatalf("unavailable CLR metadata must not compare equal or panic: %+v", diffs)
			}
		})
	}
}
