package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func loadMigrationMetadata(ctx context.Context, db *sql.DB) (*migrationMetadata, error) {
	m := &migrationMetadata{
		schemas: make(map[string]bool), objectTypes: make(map[objectName]string),
		dependencies: make(map[objectName][]migrationDependency), unsafeObjects: make(map[objectName]string),
	}
	if err := db.QueryRowContext(ctx, `SELECT DB_NAME(), CONVERT(nvarchar(128), SERVERPROPERTY('ServerName'))`).Scan(&m.databaseName, &m.serverName); err != nil {
		return nil, fmt.Errorf("read migration target identity: %w", err)
	}
	if err := loadMigrationInventory(ctx, db, m); err != nil {
		return nil, err
	}
	if err := loadMigrationReferences(ctx, db, m); err != nil {
		return nil, err
	}
	if err := loadSynonymReferences(ctx, db, m); err != nil {
		return nil, err
	}
	if err := loadMigrationSafety(ctx, db, m); err != nil {
		return nil, err
	}
	return m, nil
}

func loadMigrationInventory(ctx context.Context, db *sql.DB, m *migrationMetadata) error {
	rows, err := db.QueryContext(ctx, `SELECT s.name, o.name, o.type
FROM sys.schemas AS s LEFT JOIN sys.all_objects AS o ON o.schema_id = s.schema_id`)
	if err != nil {
		return fmt.Errorf("read migration object inventory: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var schemaName string
		var name, kind sql.NullString
		if err := rows.Scan(&schemaName, &name, &kind); err != nil {
			return fmt.Errorf("decode migration object inventory: %w", err)
		}
		m.schemas[schemaName] = true
		if name.Valid && kind.Valid {
			m.objectTypes[objectName{schemaName, name.String}] = strings.TrimSpace(kind.String)
		}
	}
	return rows.Err()
}

func loadMigrationReferences(ctx context.Context, db *sql.DB, m *migrationMetadata) error {
	rows, err := db.QueryContext(ctx, `
SELECT s.name, o.name, d.referenced_class, d.referenced_class_desc,
       d.referenced_server_name, d.referenced_database_name,
       d.referenced_schema_name, d.referenced_entity_name,
       rs.name, ro.name, d.is_schema_bound_reference, d.is_caller_dependent, d.is_ambiguous
FROM sys.sql_expression_dependencies AS d
JOIN sys.objects AS o ON o.object_id = d.referencing_id AND d.referencing_class = 1
JOIN sys.schemas AS s ON s.schema_id = o.schema_id
LEFT JOIN sys.all_objects AS ro ON ro.object_id = d.referenced_id AND d.referenced_class = 1
LEFT JOIN sys.schemas AS rs ON rs.schema_id = ro.schema_id
WHERE o.is_ms_shipped = 0`)
	if err != nil {
		return fmt.Errorf("read migration dependencies (requires SELECT on sys.sql_expression_dependencies): %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var owner objectName
		var class int
		var className, entity string
		var server, database, schemaName, resolvedSchema, resolvedName sql.NullString
		var bound, callerDependent, ambiguous bool
		if err := rows.Scan(&owner.schema, &owner.name, &class, &className,
			&server, &database, &schemaName, &entity, &resolvedSchema, &resolvedName,
			&bound, &callerDependent, &ambiguous); err != nil {
			return fmt.Errorf("decode migration dependency: %w", err)
		}
		dep := migrationDependency{schemaBound: bound}
		switch {
		case server.Valid || database.Valid:
			dep.issue = fmt.Sprintf("external dependency %q.%q.%q.%q requires manual validation", server.String, database.String, schemaName.String, entity)
		case class != 1:
			dep.issue = fmt.Sprintf("%s dependency %q.%q requires manual migration", className, schemaName.String, entity)
		// SQL Server can flag a scalar-function call as ambiguous even when its
		// catalog object ID is resolved. That binding is still a prerequisite.
		case callerDependent || (ambiguous && (!resolvedSchema.Valid || !resolvedName.Valid)):
			dep.issue = fmt.Sprintf("runtime/ambiguous dependency %q.%q cannot be ordered reliably", schemaName.String, entity)
		case !resolvedSchema.Valid || !resolvedName.Valid:
			dep.issue = fmt.Sprintf("unresolved dependency %q.%q", schemaName.String, entity)
		default:
			dep.name = objectName{resolvedSchema.String, resolvedName.String}
		}
		m.dependencies[owner] = append(m.dependencies[owner], dep)
	}
	return rows.Err()
}

// Synonyms do not appear as referencing entities in sql_expression_dependencies.
// Their target is an explicit prerequisite of CREATE/REPLACE and of consumers.
func loadSynonymReferences(ctx context.Context, db *sql.DB, m *migrationMetadata) error {
	rows, err := db.QueryContext(ctx, `
SELECT s.name, sn.name, sn.base_object_name,
       PARSENAME(sn.base_object_name, 4), PARSENAME(sn.base_object_name, 3),
       OBJECT_SCHEMA_NAME(OBJECT_ID(sn.base_object_name)), OBJECT_NAME(OBJECT_ID(sn.base_object_name))
FROM sys.synonyms AS sn JOIN sys.schemas AS s ON s.schema_id = sn.schema_id`)
	if err != nil {
		return fmt.Errorf("read synonym dependencies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var owner objectName
		var target string
		var server, database, targetSchema, targetName sql.NullString
		if err := rows.Scan(&owner.schema, &owner.name, &target, &server, &database, &targetSchema, &targetName); err != nil {
			return fmt.Errorf("decode synonym dependency: %w", err)
		}
		var dep migrationDependency
		switch {
		case server.Valid || database.Valid:
			dep.issue = fmt.Sprintf("external synonym target %q requires manual validation", target)
		case !targetSchema.Valid || !targetName.Valid:
			dep.issue = fmt.Sprintf("unresolved synonym target %q", target)
		default:
			dep.name = objectName{targetSchema.String, targetName.String}
		}
		m.dependencies[owner] = append(m.dependencies[owner], dep)
	}
	return rows.Err()
}

func loadMigrationSafety(ctx context.Context, db *sql.DB, m *migrationMetadata) error {
	rows, err := db.QueryContext(ctx, `
SELECT s.name, o.name,
       CASE
       WHEN o.type = 'V' AND EXISTS (SELECT 1 FROM sys.indexes AS i WHERE i.object_id = o.object_id AND i.index_id > 0)
           THEN 'indexed view: ALTER can discard indexes'
       WHEN o.type = 'V' AND EXISTS (SELECT 1 FROM sys.triggers AS tr WHERE tr.parent_id = o.object_id)
           THEN 'view triggers require manual migration'
       WHEN o.type = 'SN' AND EXISTS (SELECT 1 FROM sys.database_permissions AS p WHERE p.class = 1 AND p.major_id = o.object_id)
           THEN 'synonym permissions require manual migration: DROP removes grants and denials'
       WHEN o.type = 'SN' AND EXISTS (SELECT 1 FROM sys.extended_properties AS ep WHERE ep.class = 1 AND ep.major_id = o.object_id)
           THEN 'synonym extended properties require manual migration: DROP removes them'
       WHEN EXISTS (SELECT 1 FROM sys.crypt_properties AS cp WHERE cp.class = 1 AND cp.major_id = o.object_id)
           THEN 'signed module: ALTER can discard signatures'
       WHEN sm.uses_native_compilation = 1 THEN 'natively compiled module requires manual migration'
       WHEN o.principal_id IS NOT NULL THEN 'explicit object ownership requires manual migration'
       WHEN sm.execute_as_principal_id >= 0 THEN 'EXECUTE AS user/SELF requires manual migration'
       WHEN EXISTS (SELECT 1 FROM sys.numbered_procedures AS np WHERE np.object_id = o.object_id AND np.procedure_number > 1)
           THEN 'numbered procedures require manual migration'
       END
FROM sys.objects AS o JOIN sys.schemas AS s ON s.schema_id = o.schema_id
LEFT JOIN sys.sql_modules AS sm ON sm.object_id = o.object_id
WHERE o.is_ms_shipped = 0 AND o.type IN ('P', 'RF', 'V', 'FN', 'IF', 'TF', 'SN')`)
	if err != nil {
		return fmt.Errorf("read migration safety metadata: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name objectName
		var reason sql.NullString
		if err := rows.Scan(&name.schema, &name.name, &reason); err != nil {
			return fmt.Errorf("decode migration safety metadata: %w", err)
		}
		if reason.Valid {
			m.unsafeObjects[name] = reason.String
		}
	}
	return rows.Err()
}
