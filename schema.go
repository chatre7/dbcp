package main

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
	_ "github.com/microsoft/go-mssqldb/namedpipe"
)

type objectName struct {
	schema string
	name   string
}

func quoteIdentifier(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

func (n objectName) String() string {
	return quoteIdentifier(n.schema) + "." + quoteIdentifier(n.name)
}

type column struct {
	dataType string
	nullable bool
}

func (c column) String() string {
	if c.nullable {
		return c.dataType + " NULL"
	}
	return c.dataType + " NOT NULL"
}

type keyColumn struct {
	name       string
	ordinal    int
	descending bool
}

type table struct {
	columns map[string]column
	keyType string
	key     []keyColumn
}

func (t *table) primaryKey() string {
	if len(t.key) == 0 {
		return "<none>"
	}
	parts := make([]string, 0, len(t.key))
	for _, k := range t.key {
		direction := " ASC"
		if k.descending {
			direction = " DESC"
		}
		parts = append(parts, quoteIdentifier(k.name)+direction)
	}
	return t.keyType + " (" + strings.Join(parts, ", ") + ")"
}

type schema struct {
	tables    map[objectName]*table
	objects   map[objectName]schemaObject
	migration *migrationMetadata
}

// One catalog query captures columns and ordered primary-key membership without
// querying table data. Object IDs are deliberately not compared across databases.
const schemaQuery = `
SELECT s.name, t.name, c.name,
       ts.name, ty.name, COALESCE(bt.name, ty.name), ty.is_user_defined,
       c.max_length, c.precision, c.scale, c.is_nullable,
       COALESCE(xs.name, ''), COALESCE(xc.name, ''), c.is_xml_document,
       COALESCE(pk.type_desc, ''), COALESCE(pk.key_ordinal, 0),
       COALESCE(pk.is_descending_key, CAST(0 AS bit))
FROM sys.tables AS t
JOIN sys.schemas AS s ON s.schema_id = t.schema_id
JOIN sys.columns AS c ON c.object_id = t.object_id
JOIN sys.types AS ty ON ty.user_type_id = c.user_type_id
JOIN sys.schemas AS ts ON ts.schema_id = ty.schema_id
LEFT JOIN sys.types AS bt
    ON bt.user_type_id = c.system_type_id AND bt.system_type_id = bt.user_type_id
LEFT JOIN sys.xml_schema_collections AS xc
    ON xc.xml_collection_id = c.xml_collection_id AND c.xml_collection_id <> 0
LEFT JOIN sys.schemas AS xs ON xs.schema_id = xc.schema_id
LEFT JOIN (
    SELECT i.object_id, ic.column_id, i.type_desc,
           ic.key_ordinal, ic.is_descending_key
    FROM sys.indexes AS i
    JOIN sys.index_columns AS ic
        ON ic.object_id = i.object_id AND ic.index_id = i.index_id
    WHERE i.is_primary_key = 1 AND ic.key_ordinal > 0
) AS pk ON pk.object_id = c.object_id AND pk.column_id = c.column_id
WHERE t.is_ms_shipped = 0`

func loadSchema(ctx context.Context, dsn string, withMigration bool) (*schema, error) {
	config, err := msdsn.Parse(dsn)
	if err != nil {
		// DSN parser errors can contain credentials; do not echo them.
		return nil, fmt.Errorf("invalid connection string; use a SQL Server URL or key=value pairs")
	}
	if strings.TrimSpace(config.Database) == "" {
		return nil, fmt.Errorf("connection string must explicitly specify database")
	}
	db := sql.OpenDB(mssql.NewConnectorConfig(config))
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Catalog views silently hide objects without permission. Fail rather than
	// accidentally reporting two partially visible schemas as identical.
	var canView int
	err = db.QueryRowContext(ctx,
		`SELECT COALESCE(HAS_PERMS_BY_NAME(DB_NAME(), 'DATABASE', 'VIEW DEFINITION'), 0)`).Scan(&canView)
	if err != nil {
		return nil, fmt.Errorf("connect/read metadata permission: %w", err)
	}
	if canView != 1 {
		return nil, fmt.Errorf("database-level VIEW DEFINITION permission is required")
	}

	rows, err := db.QueryContext(ctx, schemaQuery)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	defer rows.Close()

	result := &schema{tables: make(map[objectName]*table)}
	for rows.Next() {
		var name objectName
		var columnName, typeSchema, typeName, baseType, xmlSchema, xmlCollection, keyType string
		var userDefined, nullable, xmlDocument, descending bool
		var length, precision, scale, ordinal int
		if err := rows.Scan(&name.schema, &name.name, &columnName,
			&typeSchema, &typeName, &baseType, &userDefined,
			&length, &precision, &scale, &nullable,
			&xmlSchema, &xmlCollection, &xmlDocument,
			&keyType, &ordinal, &descending); err != nil {
			return nil, fmt.Errorf("decode schema: %w", err)
		}
		dataType := formatType(baseType, length, precision, scale)
		if userDefined {
			dataType = quoteIdentifier(typeSchema) + "." + quoteIdentifier(typeName) + " (base: " + dataType + ")"
		} else if typeName != baseType {
			dataType = typeName + " (base: " + dataType + ")"
		}
		if xmlCollection != "" {
			kind := "CONTENT"
			if xmlDocument {
				kind = "DOCUMENT"
			}
			dataType += "(" + kind + " " + quoteIdentifier(xmlSchema) + "." + quoteIdentifier(xmlCollection) + ")"
		}
		t := result.tables[name]
		if t == nil {
			t = &table{columns: make(map[string]column)}
			result.tables[name] = t
		}
		t.columns[columnName] = column{dataType: dataType, nullable: nullable}
		if ordinal > 0 {
			t.keyType = keyType
			t.key = append(t.key, keyColumn{name: columnName, ordinal: ordinal, descending: descending})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema rows: %w", err)
	}
	for _, t := range result.tables {
		slices.SortFunc(t.key, func(a, b keyColumn) int { return a.ordinal - b.ordinal })
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close schema rows: %w", err)
	}
	result.objects, err = loadObjects(ctx, db)
	if err != nil {
		return nil, err
	}
	if withMigration {
		result.migration, err = loadMigrationMetadata(ctx, db)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func formatType(name string, length, precision, scale int) string {
	switch name {
	case "varchar", "char", "nvarchar", "nchar", "varbinary", "binary":
		if length == -1 {
			return name + "(max)"
		}
		if name == "nvarchar" || name == "nchar" {
			length /= 2 // SQL Server reports Unicode lengths in bytes.
		}
		return fmt.Sprintf("%s(%d)", name, length)
	case "decimal", "numeric":
		return fmt.Sprintf("%s(%d,%d)", name, precision, scale)
	case "datetime2", "datetimeoffset", "time":
		return fmt.Sprintf("%s(%d)", name, scale)
	case "float":
		return fmt.Sprintf("%s(%d)", name, precision)
	default:
		return name
	}
}
