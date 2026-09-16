package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aymanbagabas/go-udiff"
)

type difference struct {
	kind        string
	object      string
	source      string
	destination string
}

func unionKeys[K comparable, V any](a, b map[K]V) []K {
	keys := make([]K, 0, len(a)+len(b))
	for key := range a {
		keys = append(keys, key)
	}
	for key := range b {
		if _, exists := a[key]; !exists {
			keys = append(keys, key)
		}
	}
	return keys
}

func compareSchemas(source, destination *schema, mode sqlCompareMode) []difference {
	var diffs []difference
	tables := unionKeys(source.tables, destination.tables)
	slices.SortFunc(tables, func(a, b objectName) int {
		if c := strings.Compare(a.schema, b.schema); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	for _, name := range tables {
		src, inSource := source.tables[name]
		dst, inDestination := destination.tables[name]
		switch {
		case !inDestination:
			diffs = append(diffs, difference{"TABLE_ONLY_IN_SOURCE", name.String(), "present", "<missing>"})
			continue
		case !inSource:
			diffs = append(diffs, difference{"TABLE_ONLY_IN_DESTINATION", name.String(), "<missing>", "present"})
			continue
		}
		columns := unionKeys(src.columns, dst.columns)
		slices.Sort(columns)
		for _, columnName := range columns {
			a, hasA := src.columns[columnName]
			b, hasB := dst.columns[columnName]
			object := name.String() + "." + quoteIdentifier(columnName)
			switch {
			case !hasB:
				diffs = append(diffs, difference{"COLUMN_ONLY_IN_SOURCE", object, a.String(), "<missing>"})
			case !hasA:
				diffs = append(diffs, difference{"COLUMN_ONLY_IN_DESTINATION", object, "<missing>", b.String()})
			default:
				if a.dataType != b.dataType {
					diffs = append(diffs, difference{"COLUMN_TYPE", object, a.dataType, b.dataType})
				}
				if a.nullable != b.nullable {
					diffs = append(diffs, difference{"COLUMN_NULLABILITY", object, nullability(a.nullable), nullability(b.nullable)})
				}
			}
		}
		if a, b := src.primaryKey(), dst.primaryKey(); a != b {
			diffs = append(diffs, difference{"PRIMARY_KEY", name.String(), a, b})
		}
	}
	return append(diffs, compareObjects(source.objects, destination.objects, mode)...)
}

func nullability(nullable bool) string {
	if nullable {
		return "NULL"
	}
	return "NOT NULL"
}

func renderReport(source, destination *schema, diffs []difference, mode sqlCompareMode) string {
	var out strings.Builder
	fmt.Fprintln(&out, "MSSQL SCHEMA DIFF")
	fmt.Fprintln(&out, "Scope: user tables, column names/types/nullability, primary-key columns/order/direction/clustering,")
	fmt.Fprintln(&out, "       SQL stored procedures/views/functions (definition, ANSI_NULLS, QUOTED_IDENTIFIER), synonym targets.")
	fmt.Fprintf(&out, "SQL comparison mode: %s (CRLF normalized to LF).\n", mode)
	if mode == sqlModeNormalized {
		fmt.Fprintln(&out, "Declaration verbs CREATE/ALTER/CREATE OR ALTER and spacing before the module type are normalized.")
	}
	fmt.Fprintln(&out, "Other formatting, comments, literals and identifier case remain significant.")
	fmt.Fprintln(&out, "SQL hunks show original definitions with 3 context lines; ignored header changes may appear alongside real changes.")
	fmt.Fprintln(&out, "Encrypted/unreadable definitions and CLR/extended modules cause an error, not a no-diff result.")
	fmt.Fprintln(&out, "Not compared: row data, column order, defaults, identity, computed expressions, collation,")
	fmt.Fprintln(&out, "              non-PK indexes, FK/CHECK/UNIQUE constraints, constraint names, triggers, permissions,")
	fmt.Fprintln(&out, "              type/XML schema definitions and other advanced table/column properties.")
	fmt.Fprintln(&out, "Names are case-sensitive. Avoid schema changes during comparison.")
	fmt.Fprintf(&out, "Tables: source=%d destination=%d\n", len(source.tables), len(destination.tables))
	fmt.Fprintf(&out, "Procedures/views/functions/synonyms: source=%d destination=%d\n\n", len(source.objects), len(destination.objects))
	for _, d := range diffs {
		if strings.HasSuffix(d.kind, "_DEFINITION") {
			fmt.Fprintf(&out, "[%s] %q\n", d.kind, d.object)
			out.WriteString(definitionDiff(d))
			fmt.Fprintln(&out)
			continue
		}
		// Quoting also keeps identifiers containing newlines on a single line.
		fmt.Fprintf(&out, "[%s] %q\n  source:      %q\n  destination: %q\n\n",
			d.kind, d.object, d.source, d.destination)
	}
	if len(diffs) == 0 {
		fmt.Fprintln(&out, "No differences found within the comparison scope.")
	}
	fmt.Fprintf(&out, "Summary: %d difference(s).\n", len(diffs))
	return out.String()
}

func definitionDiff(d difference) string {
	return udiff.Unified(
		fmt.Sprintf("source %q", d.object),
		fmt.Sprintf("destination %q", d.object),
		d.source, d.destination,
	)
}
