package main

import "testing"

func TestCompositePrimaryKeyOrderIsSignificant(t *testing.T) {
	name := objectName{"dbo", "Orders"}
	columns := map[string]column{"TenantID": {"int", false}, "ID": {"int", false}}
	source := &schema{tables: map[objectName]*table{name: {columns: columns, keyType: "NONCLUSTERED", key: []keyColumn{
		{name: "TenantID", ordinal: 1}, {name: "ID", ordinal: 2, descending: true},
	}}}}
	destination := &schema{tables: map[objectName]*table{name: {columns: columns, keyType: "NONCLUSTERED", key: []keyColumn{
		{name: "ID", ordinal: 1, descending: true}, {name: "TenantID", ordinal: 2},
	}}}}
	diffs := compareSchemas(source, destination, sqlModeStrict)
	if len(diffs) != 1 || diffs[0].kind != "PRIMARY_KEY" {
		t.Fatalf("reordered composite key must be reported: %+v", diffs)
	}
	if diffs[0].source != "NONCLUSTERED ([TenantID] ASC, [ID] DESC)" ||
		diffs[0].destination != "NONCLUSTERED ([ID] DESC, [TenantID] ASC)" {
		t.Fatalf("report lost key order or direction: %+v", diffs[0])
	}
}

func TestQualifiedTableNamesDoNotCollide(t *testing.T) {
	// Joining schema and table with a dot would mistake these for the same table.
	source := &schema{tables: map[objectName]*table{{"a.b", "c"}: {}}}
	destination := &schema{tables: map[objectName]*table{{"a", "b.c"}: {}}}
	diffs := compareSchemas(source, destination, sqlModeStrict)
	if len(diffs) != 2 ||
		diffs[0].kind != "TABLE_ONLY_IN_DESTINATION" || diffs[0].object != "[a].[b.c]" ||
		diffs[1].kind != "TABLE_ONLY_IN_SOURCE" || diffs[1].object != "[a.b].[c]" {
		t.Fatalf("distinct qualified names must stay distinct: %+v", diffs)
	}
}
