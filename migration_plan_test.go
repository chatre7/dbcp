package main

import (
	"slices"
	"strings"
	"testing"
)

func migrationPlanSchema(objects map[objectName]schemaObject) *schema {
	result := &schema{
		objects: objects,
		tables:  make(map[objectName]*table),
		migration: &migrationMetadata{
			schemas:       map[string]bool{"dbo": true},
			objectTypes:   make(map[objectName]string),
			dependencies:  make(map[objectName][]migrationDependency),
			unsafeObjects: make(map[objectName]string),
		},
	}
	for name, object := range objects {
		result.migration.objectTypes[name] = object.typeCode
	}
	return result
}

func TestMigrationOrdersThroughUnchangedIntermediate(t *testing.T) {
	first, middle, last := objectName{"dbo", "z_first"}, objectName{"dbo", "m_middle"}, objectName{"dbo", "a_last"}
	old := schemaObject{typeCode: "V", definition: "CREATE VIEW dbo.example AS SELECT 1 AS value"}
	updated := old
	updated.definition = "CREATE VIEW dbo.example AS SELECT 2 AS value"
	source := migrationPlanSchema(map[objectName]schemaObject{first: updated, middle: old, last: updated})
	destination := migrationPlanSchema(map[objectName]schemaObject{first: old, middle: old, last: old})
	source.migration.dependencies[last] = []migrationDependency{{name: middle}}
	source.migration.dependencies[middle] = []migrationDependency{{name: first}}
	steps, _, err := planMigration(source, destination, sqlModeStrict)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[0].name != first || steps[1].name != last || steps[0].action != "ALTER" || steps[1].action != "ALTER" {
		t.Fatalf("selected prerequisites must precede dependents through unchanged objects: %+v", steps)
	}
}

func TestMigrationRejectsReachableCycles(t *testing.T) {
	a, z := objectName{"dbo", "a"}, objectName{"dbo", "z"}
	for _, selfCycle := range []bool{false, true} {
		t.Run(map[bool]string{false: "through unchanged object", true: "self cycle"}[selfCycle], func(t *testing.T) {
			object := schemaObject{typeCode: "P", definition: "CREATE PROCEDURE dbo.a AS SELECT 1"}
			source := migrationPlanSchema(map[objectName]schemaObject{a: object, z: object})
			destination := migrationPlanSchema(map[objectName]schemaObject{z: object})
			want := a.String() + " -> " + z.String() + " -> " + a.String()
			if selfCycle {
				source.migration.dependencies[a] = []migrationDependency{{name: a}}
				want = a.String() + " -> " + a.String()
			} else {
				source.migration.dependencies[a] = []migrationDependency{{name: z}}
				source.migration.dependencies[z] = []migrationDependency{{name: a}}
			}
			_, _, err := planMigration(source, destination, sqlModeStrict)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("expected named dependency cycle %q, got %v", want, err)
			}
		})
	}
}

func TestMigrationOrdersSynonymPrerequisite(t *testing.T) {
	view, synonym, procedure := objectName{"dbo", "z_view"}, objectName{"dbo", "m_synonym"}, objectName{"dbo", "a_procedure"}
	source := migrationPlanSchema(map[objectName]schemaObject{
		view:      {typeCode: "V", definition: "CREATE VIEW dbo.z_view AS SELECT 1 AS value"},
		synonym:   {typeCode: "SN", target: "[dbo].[z_view]"},
		procedure: {typeCode: "P", definition: "CREATE PROCEDURE dbo.a_procedure AS SELECT * FROM dbo.m_synonym"},
	})
	destination := migrationPlanSchema(map[objectName]schemaObject{synonym: {typeCode: "SN", target: "[dbo].[old_view]"}})
	source.migration.dependencies[procedure] = []migrationDependency{{name: synonym}}
	source.migration.dependencies[synonym] = []migrationDependency{{name: view}}
	steps, _, err := planMigration(source, destination, sqlModeStrict)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || steps[0].name != view || steps[1].name != synonym || steps[2].name != procedure || steps[1].action != "REPLACE" {
		t.Fatalf("view must be created before synonym replacement and consuming procedure: %+v", steps)
	}
}

func TestMigrationTablePrerequisites(t *testing.T) {
	module, tableName := objectName{"dbo", "reader"}, objectName{"dbo", "data"}
	for _, scenario := range []string{"missing", "columns changed", "primary key changed", "compatible", "unrelated change"} {
		t.Run(scenario, func(t *testing.T) {
			source := migrationPlanSchema(map[objectName]schemaObject{module: {typeCode: "P", definition: "CREATE PROCEDURE dbo.reader AS SELECT id FROM dbo.data"}})
			destination := migrationPlanSchema(nil)
			source.tables[tableName] = &table{columns: map[string]column{"id": {dataType: "int"}}, keyType: "CLUSTERED", key: []keyColumn{{name: "id", ordinal: 1}}}
			destination.tables[tableName] = &table{columns: map[string]column{"id": {dataType: "int"}}, keyType: "CLUSTERED", key: []keyColumn{{name: "id", ordinal: 1}}}
			source.migration.objectTypes[tableName] = "U"
			destination.migration.objectTypes[tableName] = "U"
			source.migration.dependencies[module] = []migrationDependency{{name: tableName}}
			switch scenario {
			case "missing":
				delete(destination.tables, tableName)
				delete(destination.migration.objectTypes, tableName)
			case "columns changed", "unrelated change":
				destination.tables[tableName].columns["id"] = column{dataType: "bigint"}
				if scenario == "unrelated change" {
					delete(source.migration.dependencies, module)
				}
			case "primary key changed":
				destination.tables[tableName].key[0].descending = true
			}
			steps, notes, err := planMigration(source, destination, sqlModeStrict)
			if scenario == "compatible" || scenario == "unrelated change" {
				if err != nil || len(steps) != 1 || steps[0].name != module {
					t.Fatalf("independent or compatible tables should permit module creation: %+v, %v", steps, err)
				}
				if scenario == "unrelated change" && !slices.ContainsFunc(notes, func(note string) bool {
					return strings.Contains(note, tableName.String()) && strings.Contains(note, "manual migration")
				}) {
					t.Fatalf("unsupported unrelated table change must be explicit: %v", notes)
				}
			} else if err == nil || !strings.Contains(err.Error(), tableName.String()) || !strings.Contains(err.Error(), "manual table migration") {
				t.Fatalf("incompatible table prerequisite must block with a manual-migration explanation: %v", err)
			}
		})
	}
}

func TestMigrationRejectsMissingOrUnverifiablePrerequisites(t *testing.T) {
	module, missing := objectName{"dbo", "reader"}, objectName{"dbo", "missing"}
	for _, issue := range []string{"", "cross-database dependency cannot be validated"} {
		source := migrationPlanSchema(map[objectName]schemaObject{module: {typeCode: "P", definition: "CREATE PROCEDURE dbo.reader AS SELECT 1"}})
		source.migration.dependencies[module] = []migrationDependency{{name: missing, issue: issue}}
		_, _, err := planMigration(source, migrationPlanSchema(nil), sqlModeStrict)
		if err == nil || !strings.Contains(err.Error(), module.String()) {
			t.Fatalf("missing or unverifiable prerequisite must identify its dependent: %v", err)
		}
		if issue != "" && !strings.Contains(err.Error(), issue) {
			t.Fatalf("dependency blocker must survive in the error: %v", err)
		}
	}
}

func TestMigrationPreservesDestinationDependents(t *testing.T) {
	changed, dependent := objectName{"dbo", "changed"}, objectName{"dbo", "destination_only"}
	for _, typeCode := range []string{"V", "SN"} {
		for _, schemaBound := range []bool{false, true} {
			old := schemaObject{typeCode: typeCode, definition: "CREATE VIEW dbo.changed AS SELECT 1 AS value", target: "[dbo].[old]"}
			updated := old
			updated.definition = "CREATE VIEW dbo.changed AS SELECT 2 AS value"
			updated.target = "[dbo].[new]"
			source := migrationPlanSchema(map[objectName]schemaObject{changed: updated})
			destination := migrationPlanSchema(map[objectName]schemaObject{changed: old, dependent: {typeCode: "V", definition: "CREATE VIEW dbo.destination_only AS SELECT * FROM dbo.changed"}})
			destination.migration.dependencies[dependent] = []migrationDependency{{name: changed, schemaBound: schemaBound}}
			steps, notes, err := planMigration(source, destination, sqlModeStrict)
			if schemaBound {
				if err == nil || !strings.Contains(err.Error(), dependent.String()) || !strings.Contains(err.Error(), "schema-bound") {
					t.Fatalf("schema-bound dependent must block %s: %v", typeCode, err)
				}
			} else {
				if err != nil || len(steps) != 1 || steps[0].name != changed {
					t.Fatalf("unbound destination-only dependent must not be modified: %+v, %v", steps, err)
				}
				if !slices.ContainsFunc(notes, func(note string) bool {
					return strings.Contains(note, dependent.String()) && strings.Contains(note, "retained unchanged")
				}) {
					t.Fatalf("retained destination-only dependent must be reported: %v", notes)
				}
			}
		}
	}
}

func TestMigrationRejectsUnsupportedTypeChangesAndCollisions(t *testing.T) {
	name := objectName{"dbo", "occupied"}
	for _, scenario := range []struct{ sourceType, destinationType string }{{"IF", "FN"}, {"SN", "V"}, {"V", "SN"}, {"RF", ""}, {"P", "U"}} {
		t.Run(scenario.sourceType+" over "+scenario.destinationType, func(t *testing.T) {
			source := migrationPlanSchema(map[objectName]schemaObject{name: {typeCode: scenario.sourceType, definition: "SELECT 1", target: "[dbo].[target]"}})
			destination := migrationPlanSchema(make(map[objectName]schemaObject))
			if scenario.destinationType != "" {
				destination.migration.objectTypes[name] = scenario.destinationType
				if scenario.destinationType != "U" {
					destination.objects[name] = schemaObject{typeCode: scenario.destinationType, definition: "SELECT 1", target: "[dbo].[target]"}
				}
			}
			_, _, err := planMigration(source, destination, sqlModeStrict)
			if err == nil || !strings.Contains(err.Error(), name.String()) {
				t.Fatalf("unsupported type transition or name collision must reject generation: %v", err)
			}
		})
	}
}

func TestMigrationComparisonModeAndSingleStep(t *testing.T) {
	name := objectName{"dbo", "module"}
	src := schemaObject{typeCode: "P", definition: "CREATE PROCEDURE dbo.module AS SELECT 1", ansiNulls: true, quotedIdentifier: true}
	dst := src
	dst.definition = "ALTER PROCEDURE dbo.module AS SELECT 1"
	source := migrationPlanSchema(map[objectName]schemaObject{name: src})
	destination := migrationPlanSchema(map[objectName]schemaObject{name: dst})
	steps, _, err := planMigration(source, destination, sqlModeNormalized)
	if err != nil || len(steps) != 0 {
		t.Fatalf("normalized declaration-only difference must not produce migration: %+v, %v", steps, err)
	}
	dst.ansiNulls = false
	dst.quotedIdentifier = false
	destination.objects[name] = dst
	steps, _, err = planMigration(source, destination, sqlModeStrict)
	if err != nil || len(steps) != 1 || steps[0].action != "ALTER" {
		t.Fatalf("multiple differences on one object must produce exactly one ALTER: %+v, %v", steps, err)
	}
}

func TestMigrationRequiresMetadataSchemaAndSafeObjects(t *testing.T) {
	name := objectName{"dbo", "module"}
	for _, scenario := range []string{"missing metadata", "missing schema", "unsafe source", "unsafe destination"} {
		t.Run(scenario, func(t *testing.T) {
			source := migrationPlanSchema(map[objectName]schemaObject{name: {typeCode: "P", definition: "CREATE PROCEDURE dbo.module AS SELECT 2"}})
			destination := migrationPlanSchema(map[objectName]schemaObject{name: {typeCode: "P", definition: "CREATE PROCEDURE dbo.module AS SELECT 1"}})
			want := "metadata"
			switch scenario {
			case "missing metadata":
				source.migration = nil
			case "missing schema":
				delete(destination.migration.schemas, "dbo")
				want = "schema"
			case "unsafe source":
				source.migration.unsafeObjects[name] = "signed module"
				want = "signed module"
			case "unsafe destination":
				destination.migration.unsafeObjects[name] = "indexed view"
				want = "indexed view"
			}
			_, _, err := planMigration(source, destination, sqlModeStrict)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("generation must reject %s: %v", scenario, err)
			}
		})
	}
}
