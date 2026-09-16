package main

import (
	_ "embed"
	"html/template"
	"strings"
)

//go:embed report_html.tmpl
var htmlReportTemplate string

var reportTemplate = template.Must(template.New("schema-report").Parse(htmlReportTemplate))

type htmlDiffLine struct {
	Class string
	Text  string
}

type htmlDifference struct {
	Index       int
	Kind        string
	Object      string
	Source      string
	Destination string
	SQL         bool
	Lines       []htmlDiffLine
}

type htmlReportData struct {
	Mode               sqlCompareMode
	Normalized         bool
	SourceTables       int
	DestinationTables  int
	SourceObjects      int
	DestinationObjects int
	Differences        []htmlDifference
	History            *snapshotReportContext
}

func renderHTMLReport(source, destination *schema, diffs []difference, mode sqlCompareMode, history *snapshotReportContext) (string, error) {
	data := htmlReportData{
		Mode: mode, Normalized: mode == sqlModeNormalized,
		History:      history,
		SourceTables: len(source.tables), DestinationTables: len(destination.tables),
		SourceObjects: len(source.objects), DestinationObjects: len(destination.objects),
		Differences: make([]htmlDifference, 0, len(diffs)),
	}
	for i, d := range diffs {
		item := htmlDifference{
			Index: i + 1, Kind: strings.ReplaceAll(d.kind, "_", " "), Object: d.object,
			Source: d.source, Destination: d.destination,
			SQL: strings.HasSuffix(d.kind, "_DEFINITION"),
		}
		if item.SQL {
			inHunk := false
			for line := range strings.SplitSeq(strings.TrimSuffix(definitionDiff(d), "\n"), "\n") {
				class := "context"
				switch {
				case strings.HasPrefix(line, "@@"):
					class = "hunk"
					inHunk = true
				case !inHunk:
					class = "file"
				case strings.HasPrefix(line, "-"):
					class = "removed"
				case strings.HasPrefix(line, "+"):
					class = "added"
				case strings.HasPrefix(line, "\\"):
					class = "note"
				}
				item.Lines = append(item.Lines, htmlDiffLine{Class: class, Text: line})
			}
		}
		data.Differences = append(data.Differences, item)
	}
	var out strings.Builder
	// All database text remains ordinary strings. html/template escapes it;
	// never convert object names, SQL or metadata values to template.HTML.
	if err := reportTemplate.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}
