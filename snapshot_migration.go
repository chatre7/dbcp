package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Each inventory object carries explicit dependency and safety metadata so a
// truncated payload cannot turn an unknown object into a safe, independent one.
type snapshotMigration struct {
	Schemas []string                  `json:"schemas"`
	Objects []snapshotMigrationObject `json:"objects"`
}

type snapshotMigrationObject struct {
	Schema       string                        `json:"schema"`
	Name         string                        `json:"name"`
	TypeCode     string                        `json:"type"`
	Dependencies []snapshotMigrationDependency `json:"dependencies"`
	SafetyReason *string                       `json:"safety_reason"`
}

type snapshotMigrationDependency struct {
	Schema      string  `json:"schema"`
	Name        string  `json:"name"`
	SchemaBound *bool   `json:"schema_bound"`
	Issue       *string `json:"issue"`
}

func makeSnapshotMigration(m *migrationMetadata, identity databaseIdentity) (*snapshotMigration, error) {
	if m.serverName != identity.Server || m.databaseName != identity.Database {
		return nil, errors.New("snapshot migration metadata does not match the captured database identity")
	}
	if m.schemas == nil || m.objectTypes == nil || m.dependencies == nil || m.unsafeObjects == nil {
		return nil, errors.New("snapshot requires complete migration catalog metadata")
	}
	result := &snapshotMigration{
		Schemas: make([]string, 0, len(m.schemas)),
		Objects: make([]snapshotMigrationObject, 0, len(m.objectTypes)),
	}
	for name, exists := range m.schemas {
		if !exists {
			return nil, fmt.Errorf("snapshot migration schema %q is not present", name)
		}
		result.Schemas = append(result.Schemas, name)
	}
	slices.Sort(result.Schemas)
	for name := range m.dependencies {
		if _, exists := m.objectTypes[name]; !exists {
			return nil, fmt.Errorf("snapshot migration dependency owner %s is absent from the inventory", name)
		}
	}
	for name := range m.unsafeObjects {
		if _, exists := m.objectTypes[name]; !exists {
			return nil, fmt.Errorf("snapshot migration safety owner %s is absent from the inventory", name)
		}
	}
	for name, kind := range m.objectTypes {
		reason := m.unsafeObjects[name]
		item := snapshotMigrationObject{
			Schema: name.schema, Name: name.name, TypeCode: kind, SafetyReason: &reason,
			Dependencies: make([]snapshotMigrationDependency, 0, len(m.dependencies[name])),
		}
		for _, dependency := range m.dependencies[name] {
			issue := dependency.issue
			item.Dependencies = append(item.Dependencies, snapshotMigrationDependency{
				Schema: dependency.name.schema, Name: dependency.name.name,
				SchemaBound: snapshotBool(dependency.schemaBound), Issue: &issue,
			})
		}
		slices.SortFunc(item.Dependencies, func(a, b snapshotMigrationDependency) int {
			if order := compareSnapshotNames(a.Schema, a.Name, b.Schema, b.Name); order != 0 {
				return order
			}
			if *a.SchemaBound != *b.SchemaBound {
				if *a.SchemaBound {
					return 1
				}
				return -1
			}
			return strings.Compare(*a.Issue, *b.Issue)
		})
		result.Objects = append(result.Objects, item)
	}
	slices.SortFunc(result.Objects, func(a, b snapshotMigrationObject) int {
		return compareSnapshotNames(a.Schema, a.Name, b.Schema, b.Name)
	})
	return result, nil
}

func (s *snapshotMigration) restore(captured *schema) (*migrationMetadata, error) {
	if s.Schemas == nil || s.Objects == nil {
		return nil, errors.New("snapshot migration metadata is missing required schemas or object inventory")
	}
	m := &migrationMetadata{
		serverName: captured.snapshot.Identity.Server, databaseName: captured.snapshot.Identity.Database,
		schemas: make(map[string]bool, len(s.Schemas)), objectTypes: make(map[objectName]string, len(s.Objects)),
		dependencies:  make(map[objectName][]migrationDependency, len(s.Objects)),
		unsafeObjects: make(map[objectName]string),
	}
	for _, name := range s.Schemas {
		if name == "" || m.schemas[name] {
			return nil, errors.New("snapshot migration metadata contains an invalid or duplicate schema")
		}
		m.schemas[name] = true
	}
	for _, item := range s.Objects {
		name := objectName{item.Schema, item.Name}
		if _, exists := m.objectTypes[name]; exists || item.Name == "" || !m.schemas[item.Schema] ||
			strings.TrimSpace(item.TypeCode) == "" || item.Dependencies == nil || item.SafetyReason == nil {
			return nil, fmt.Errorf("snapshot migration object %s has invalid, duplicate, or incomplete metadata", name)
		}
		m.objectTypes[name] = item.TypeCode
		m.dependencies[name] = make([]migrationDependency, 0, len(item.Dependencies))
		if *item.SafetyReason != "" {
			m.unsafeObjects[name] = *item.SafetyReason
		}
	}
	for name := range captured.tables {
		if m.objectTypes[name] != "U" {
			return nil, fmt.Errorf("snapshot migration inventory is missing table %s or has a mismatched type", name)
		}
	}
	for name, object := range captured.objects {
		if m.objectTypes[name] != object.typeCode {
			return nil, fmt.Errorf("snapshot migration inventory is missing object %s or has a mismatched type", name)
		}
	}
	for _, item := range s.Objects {
		owner := objectName{item.Schema, item.Name}
		for _, reference := range item.Dependencies {
			name := objectName{reference.Schema, reference.Name}
			if reference.SchemaBound == nil || reference.Issue == nil {
				return nil, fmt.Errorf("snapshot migration dependency of %s is missing binding or issue metadata", owner)
			}
			if *reference.Issue == "" {
				if _, exists := m.objectTypes[name]; !exists {
					return nil, fmt.Errorf("snapshot migration dependency of %s has no inventory target %s or unresolved issue", owner, name)
				}
			}
			m.dependencies[owner] = append(m.dependencies[owner], migrationDependency{
				name: name, schemaBound: *reference.SchemaBound, issue: *reference.Issue,
			})
		}
	}
	return m, nil
}
