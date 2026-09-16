package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

func planMigration(source, destination *schema, mode sqlCompareMode) ([]migrationStep, []string, error) {
	for _, input := range []struct {
		label string
		value *schema
	}{{"source", source}, {"destination", destination}} {
		if input.value == nil || input.value.migration == nil || input.value.migration.schemas == nil || input.value.migration.objectTypes == nil || input.value.migration.dependencies == nil {
			return nil, nil, fmt.Errorf("%s migration metadata is missing; reload the schema with migration enabled", input.label)
		}
	}

	changed := make(map[string]bool)
	for _, diff := range compareObjects(source.objects, destination.objects, mode) {
		changed[diff.object] = true
	}
	selected := make(map[objectName]migrationStep)
	var notes []string
	names := unionKeys(source.objects, destination.objects)
	slices.SortFunc(names, migrationNameCompare)
	for _, name := range names {
		src, inSource := source.objects[name]
		dst, inDestination := destination.objects[name]
		if !inSource {
			notes = append(notes, fmt.Sprintf("Destination-only object %s (%s) is retained unchanged; no DROP is generated.", name, dst.typeCode))
			continue
		}
		if !changed[name.String()] {
			continue
		}
		if isCLRFunctionType(src.typeCode) {
			return nil, nil, fmt.Errorf("cannot migrate CLR function %s: apply assembly and CLR function changes manually before generating SQL migration", name)
		}
		if !migrationSupportedType(src.typeCode) {
			return nil, nil, fmt.Errorf("cannot migrate %s: unsupported source object type %s", name, src.typeCode)
		}
		if inDestination && src.typeCode != dst.typeCode {
			return nil, nil, fmt.Errorf("cannot migrate %s: unsupported object type transition from destination %s to source %s; migrate it manually", name, dst.typeCode, src.typeCode)
		}
		if actual, ok := source.migration.objectTypes[name]; !ok || actual != src.typeCode {
			return nil, nil, fmt.Errorf("source object-type metadata for %s is missing or inconsistent", name)
		}
		if actual, ok := destination.migration.objectTypes[name]; ok {
			if actual != src.typeCode {
				return nil, nil, fmt.Errorf("cannot migrate %s: destination name is occupied by object type %s, not %s", name, actual, src.typeCode)
			}
			if !inDestination {
				return nil, nil, fmt.Errorf("destination definition metadata for existing %s (%s) is missing", name, actual)
			}
		} else if inDestination {
			return nil, nil, fmt.Errorf("destination object-type metadata for %s is missing", name)
		}
		if !destination.migration.schemas[name.schema] {
			return nil, nil, fmt.Errorf("cannot migrate %s: destination schema %s is missing; create it manually first", name, quoteIdentifier(name.schema))
		}
		if reason := source.migration.unsafeObjects[name]; reason != "" {
			return nil, nil, fmt.Errorf("cannot migrate source object %s: %s", name, reason)
		}
		if reason := destination.migration.unsafeObjects[name]; reason != "" {
			return nil, nil, fmt.Errorf("cannot migrate destination object %s: %s", name, reason)
		}
		action := "CREATE"
		if inDestination {
			action = "ALTER"
			if src.typeCode == "SN" {
				action = "REPLACE"
			}
		}
		selected[name] = migrationStep{name: name, object: src, action: action}
	}

	tableNames := unionKeys(source.tables, destination.tables)
	slices.SortFunc(tableNames, migrationNameCompare)
	for _, name := range tableNames {
		src, dst := source.tables[name], destination.tables[name]
		if src == nil || dst == nil || !maps.Equal(src.columns, dst.columns) || src.primaryKey() != dst.primaryKey() {
			notes = append(notes, fmt.Sprintf("Table %s differs or is missing in one database; table changes are unsupported and require manual migration. No table DDL is generated.", name))
		}
	}

	// Even an ALTER of a selected dependent cannot safely remove an existing
	// schema-bound reference first without a separate drop/recreate strategy.
	dependents := slices.Collect(maps.Keys(destination.migration.dependencies))
	slices.SortFunc(dependents, migrationNameCompare)
	for _, dependent := range dependents {
		for _, dependency := range migrationSortedDependencies(destination.migration.dependencies[dependent]) {
			step, changes := selected[dependency.name]
			if changes && step.action != "CREATE" && dependency.schemaBound {
				return nil, nil, fmt.Errorf("cannot %s %s: destination schema-bound dependent %s blocks the change; migrate its bindings manually while preserving permissions and indexes", step.action, step.name, dependent)
			}
		}
	}

	// Walk the full reachable source graph, not just the selected subgraph:
	// unchanged intermediates still constrain the order of selected objects.
	states := make(map[objectName]uint8)
	var stack []objectName
	var steps []migrationStep
	var visit func(objectName) error
	visit = func(name objectName) error {
		switch states[name] {
		case 1:
			start := slices.Index(stack, name)
			cycle := make([]string, 0, len(stack)-start+1)
			for _, member := range stack[start:] {
				cycle = append(cycle, member.String())
			}
			cycle = append(cycle, name.String())
			return fmt.Errorf("migration dependency cycle: %s; resolve it manually", strings.Join(cycle, " -> "))
		case 2:
			return nil
		}
		typeCode, known := source.migration.objectTypes[name]
		if !known {
			return fmt.Errorf("source dependency metadata for prerequisite %s is missing", name)
		}
		if typeCode == "U" {
			src, dst := source.tables[name], destination.tables[name]
			if src == nil {
				return fmt.Errorf("source table metadata for prerequisite %s is missing", name)
			}
			if dst == nil {
				return fmt.Errorf("table prerequisite %s is missing in destination; manual table migration is required", name)
			}
			if actual, ok := destination.migration.objectTypes[name]; !ok || actual != "U" {
				return fmt.Errorf("destination object-type metadata for table prerequisite %s is missing or incompatible", name)
			}
			if !maps.Equal(src.columns, dst.columns) || src.primaryKey() != dst.primaryKey() {
				return fmt.Errorf("table prerequisite %s differs between source and destination; manual table migration is required before migrating dependent objects", name)
			}
		} else {
			if !migrationSupportedType(typeCode) && !isCLRFunctionType(typeCode) {
				return fmt.Errorf("prerequisite %s has unsupported object type %s; migrate it manually", name, typeCode)
			}
			if src, ok := source.objects[name]; !ok || src.typeCode != typeCode {
				return fmt.Errorf("source definition metadata for prerequisite %s is missing or inconsistent", name)
			}
			if _, scheduled := selected[name]; !scheduled {
				if actual, ok := destination.migration.objectTypes[name]; !ok {
					return fmt.Errorf("prerequisite %s is missing in destination and is not scheduled for creation", name)
				} else if actual != typeCode {
					return fmt.Errorf("prerequisite %s has incompatible destination type %s (source %s)", name, actual, typeCode)
				}
				if dst, ok := destination.objects[name]; !ok || dst.typeCode != typeCode {
					return fmt.Errorf("destination definition metadata for prerequisite %s is missing or inconsistent", name)
				}
			}
		}
		states[name] = 1
		stack = append(stack, name)
		for _, dependency := range migrationSortedDependencies(source.migration.dependencies[name]) {
			if dependency.issue != "" {
				return fmt.Errorf("cannot validate dependency of %s: %s", name, dependency.issue)
			}
			if err := visit(dependency.name); err != nil {
				return fmt.Errorf("dependency of %s: %w", name, err)
			}
		}
		stack = stack[:len(stack)-1]
		states[name] = 2
		if step, scheduled := selected[name]; scheduled {
			steps = append(steps, step)
		}
		return nil
	}
	for _, name := range names {
		if _, scheduled := selected[name]; scheduled {
			if err := visit(name); err != nil {
				return nil, nil, err
			}
		}
	}
	return steps, notes, nil
}

func migrationSupportedType(typeCode string) bool {
	switch typeCode {
	case "P", "V", "FN", "IF", "TF", "SN":
		return true
	default:
		return false
	}
}

func migrationNameCompare(a, b objectName) int {
	if c := strings.Compare(a.schema, b.schema); c != 0 {
		return c
	}
	return strings.Compare(a.name, b.name)
}

func migrationSortedDependencies(dependencies []migrationDependency) []migrationDependency {
	ordered := slices.Clone(dependencies)
	slices.SortFunc(ordered, func(a, b migrationDependency) int {
		if c := migrationNameCompare(a.name, b.name); c != 0 {
			return c
		}
		return strings.Compare(a.issue, b.issue)
	})
	return ordered
}
