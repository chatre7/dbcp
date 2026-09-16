package main

import (
	"html"
	"strings"
	"testing"
)

func TestHTMLReportTreatsDatabaseContentAsText(t *testing.T) {
	object := `[dbo].[" onclick="alert(1)"><svg onload="alert(2)">]`
	payload := `</span></code></pre><script>alert("database text")</script>`
	diffs := []difference{
		{"STORED_PROCEDURE_DEFINITION", object, "SELECT N'" + payload + "';\n", "SELECT N'ข้อมูล';\n"},
		{"SYNONYM_TARGET", object, `<img src=x onerror="alert(3)">`, "<missing>"},
	}
	history := &snapshotReportContext{
		Server: payload, Database: object, Baseline: true,
		Objects: []objectTimestamp{{Schema: "dbo", Name: object, Type: "P", CreatedAt: payload, ModifiedAt: payload}},
	}
	report, err := renderHTMLReport(&schema{}, &schema{}, diffs, sqlModeStrict, history)
	if err != nil {
		t.Fatal(err)
	}
	for _, executable := range []string{"<script>", "<svg ", "<img "} {
		if strings.Contains(report, executable) {
			t.Fatalf("database content became markup: %s", executable)
		}
	}
	decoded := html.UnescapeString(report)
	for _, text := range []string{object, payload, "ข้อมูล", "<missing>"} {
		if !strings.Contains(decoded, text) {
			t.Fatalf("escaping must not discard report content: %q", text)
		}
	}
}
