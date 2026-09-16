package main

import (
	"fmt"
	"strings"
	"testing"
)

func sqlReportForTest(a, b string, mode sqlCompareMode) string {
	name := objectName{"dbo", "Example"}
	source := &schema{objects: map[objectName]schemaObject{name: {typeCode: "P", definition: a}}}
	destination := &schema{objects: map[objectName]schemaObject{name: {typeCode: "P", definition: b}}}
	return renderReport(source, destination, compareSchemas(source, destination, mode), mode, nil)
}

func TestDefinitionReportUsesSeparatedContextHunks(t *testing.T) {
	var source strings.Builder
	source.WriteString("CREATE PROC dbo.Example AS\nBEGIN\n")
	for i := 1; i <= 24; i++ {
		fmt.Fprintf(&source, " SELECT %d;\n", i)
	}
	source.WriteString("END;\n")
	destination := strings.Replace(source.String(), " SELECT 4;\n", " SELECT 40;\n", 1)
	destination = strings.Replace(destination, " SELECT 22;\n", " SELECT 220;\n SELECT 221;\n", 1)
	report := sqlReportForTest(source.String(), destination, sqlModeStrict)
	for _, fragment := range []string{
		"@@ -3,7 +3,7 @@\n", "@@ -21,7 +21,8 @@\n",
		"- SELECT 4;\n+ SELECT 40;\n", "- SELECT 22;\n+ SELECT 220;\n+ SELECT 221;\n",
	} {
		if !strings.Contains(report, fragment) {
			t.Fatalf("missing diff range or changed lines %q:\n%s", fragment, report)
		}
	}
	if strings.Contains(report, "SELECT 12;") {
		t.Fatalf("unchanged SQL far from changes must not flood the report:\n%s", report)
	}
}

func TestDefinitionReportPreservesUnicodeAndMissingFinalNewline(t *testing.T) {
	report := sqlReportForTest(
		"CREATE PROC dbo.Example AS\nSELECT N'ข้อมูลเก่า';",
		"CREATE PROC dbo.Example AS\nSELECT N'ข้อมูลใหม่';",
		sqlModeStrict,
	)
	if !strings.Contains(report, "-SELECT N'ข้อมูลเก่า';\n") ||
		!strings.Contains(report, "+SELECT N'ข้อมูลใหม่';\n") ||
		strings.Count(report, "\\ No newline at end of file") != 2 {
		t.Fatalf("diff must preserve UTF-8 and distinguish missing final newlines:\n%s", report)
	}
}

func TestNormalizedReportKeepsOriginalLineNumbers(t *testing.T) {
	report := sqlReportForTest(
		"CREATE\nOR\nALTER PROC dbo.Example AS\nSELECT 1;\n",
		"CREATE PROC dbo.Example AS\nSELECT 2;\n",
		sqlModeNormalized,
	)
	if !strings.Contains(report, "@@ -1,4 +1,2 @@\n") ||
		!strings.Contains(report, "-CREATE\n-OR\n-ALTER PROC dbo.Example AS\n") {
		t.Fatalf("normalized comparison must still report original SQL line positions:\n%s", report)
	}
}
