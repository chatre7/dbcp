package main

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

type schemaObject struct {
	typeCode         string
	definition       string
	ansiNulls        bool
	quotedIdentifier bool
	target           string
	clr              *clrFunction
}

func (o schemaObject) category() string {
	switch o.typeCode {
	case "P", "RF":
		return "STORED_PROCEDURE"
	case "V":
		return "VIEW"
	case "FN", "IF", "TF", "FS", "FT":
		return "FUNCTION"
	case "SN":
		return "SYNONYM"
	default:
		return "OBJECT"
	}
}

// Start with sys.objects, not sys.sql_modules: encrypted or non-SQL modules
// must not disappear from the comparison because their definition is NULL.
const objectsQuery = `
SELECT s.name, o.name, o.type, m.definition,
       m.uses_ansi_nulls, m.uses_quoted_identifier, sn.base_object_name
FROM sys.objects AS o
JOIN sys.schemas AS s ON s.schema_id = o.schema_id
LEFT JOIN sys.sql_modules AS m ON m.object_id = o.object_id
LEFT JOIN sys.synonyms AS sn ON sn.object_id = o.object_id
WHERE o.is_ms_shipped = 0
  AND o.type IN ('P', 'RF', 'V', 'FN', 'IF', 'TF', 'SN', 'PC', 'FS', 'FT', 'AF', 'X')
ORDER BY s.name, o.name`

func loadObjects(ctx context.Context, db *sql.DB) (map[objectName]schemaObject, error) {
	rows, err := db.QueryContext(ctx, objectsQuery)
	if err != nil {
		return nil, fmt.Errorf("read procedures/views/functions/synonyms: %w", err)
	}
	defer rows.Close()

	objects := make(map[objectName]schemaObject)
	hasCLRFunctions := false
	for rows.Next() {
		var name objectName
		var object schemaObject
		var definition, target sql.NullString
		var ansiNulls, quotedIdentifier sql.NullBool
		if err := rows.Scan(&name.schema, &name.name, &object.typeCode, &definition,
			&ansiNulls, &quotedIdentifier, &target); err != nil {
			return nil, fmt.Errorf("decode database object: %w", err)
		}
		object.typeCode = strings.TrimSpace(object.typeCode)
		switch object.typeCode {
		case "SN":
			if !target.Valid {
				return nil, fmt.Errorf("cannot read synonym target for %q", name.String())
			}
			object.target = target.String
		case "FS", "FT":
			hasCLRFunctions = true
		case "P", "RF", "V", "FN", "IF", "TF":
			if !definition.Valid || !ansiNulls.Valid || !quotedIdentifier.Valid {
				return nil, fmt.Errorf("cannot read definition/settings for %s %q: encrypted or metadata access denied",
					object.category(), name.String())
			}
			object.definition = definition.String
			object.ansiNulls = ansiNulls.Bool
			object.quotedIdentifier = quotedIdentifier.Bool
		default:
			return nil, fmt.Errorf("cannot compare %q (type %s): unsupported CLR/extended module",
				name.String(), object.typeCode)
		}
		objects[name] = object
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read database object rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close database object rows: %w", err)
	}
	if hasCLRFunctions {
		functions, err := loadCLRFunctions(ctx, db)
		if err != nil {
			return nil, err
		}
		for name, object := range objects {
			if !isCLRFunctionType(object.typeCode) {
				continue
			}
			object.clr = functions[name]
			if object.clr == nil {
				return nil, fmt.Errorf("cannot read CLR function metadata for %q", name.String())
			}
			objects[name] = object
		}
	}
	return objects, nil
}

func compareObjects(source, destination map[objectName]schemaObject, mode sqlCompareMode) []difference {
	var diffs []difference
	names := unionKeys(source, destination)
	slices.SortFunc(names, func(a, b objectName) int {
		if c := strings.Compare(a.schema, b.schema); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	for _, name := range names {
		src, inSource := source[name]
		dst, inDestination := destination[name]
		switch {
		case !inDestination:
			diffs = append(diffs, difference{src.category() + "_ONLY_IN_SOURCE", name.String(), "present", "<missing>"})
			continue
		case !inSource:
			diffs = append(diffs, difference{dst.category() + "_ONLY_IN_DESTINATION", name.String(), "<missing>", "present"})
			continue
		case src.typeCode != dst.typeCode:
			diffs = append(diffs, difference{"OBJECT_TYPE", name.String(), src.typeCode, dst.typeCode})
			continue
		}
		if src.typeCode == "SN" {
			if src.target != dst.target {
				diffs = append(diffs, difference{"SYNONYM_TARGET", name.String(), src.target, dst.target})
			}
			continue
		}
		if isCLRFunctionType(src.typeCode) {
			if src.clr == nil || dst.clr == nil {
				a, b := "available", "available"
				if src.clr == nil {
					a = "<unavailable>"
				}
				if dst.clr == nil {
					b = "<unavailable>"
				}
				diffs = append(diffs, difference{"CLR_FUNCTION_METADATA", name.String(), a, b})
				continue
			}
			for _, field := range [...]struct{ kind, source, destination string }{
				{"CLR_FUNCTION_ASSEMBLY_NAME", src.clr.assemblyName, dst.clr.assemblyName},
				{"CLR_FUNCTION_ASSEMBLY_IDENTITY", src.clr.assemblyIdentity, dst.clr.assemblyIdentity},
				{"CLR_FUNCTION_ASSEMBLY_SHA256", src.clr.assemblySHA256, dst.clr.assemblySHA256},
				{"CLR_FUNCTION_PERMISSION_SET", src.clr.permissionSet, dst.clr.permissionSet},
				{"CLR_FUNCTION_CLASS", src.clr.className, dst.clr.className},
				{"CLR_FUNCTION_METHOD", src.clr.methodName, dst.clr.methodName},
				{"CLR_FUNCTION_SIGNATURE", src.clr.signature, dst.clr.signature},
				{"CLR_FUNCTION_EXECUTE_AS", src.clr.executeAs, dst.clr.executeAs},
				{"CLR_FUNCTION_NULL_ON_NULL_INPUT", onOff(src.clr.nullOnNullInput), onOff(dst.clr.nullOnNullInput)},
			} {
				if field.source != field.destination {
					diffs = append(diffs, difference{field.kind, name.String(), field.source, field.destination})
				}
			}
			continue
		}
		// Keep the original line layout for the report. Normalization affects
		// equality only; it must not shift the unified diff's line numbers.
		a := strings.ReplaceAll(src.definition, "\r\n", "\n")
		b := strings.ReplaceAll(dst.definition, "\r\n", "\n")
		comparableA, comparableB := a, b
		if mode == sqlModeNormalized {
			comparableA = normalizeSQLDeclaration(a)
			comparableB = normalizeSQLDeclaration(b)
		}
		prefix := src.category()
		if comparableA != comparableB {
			diffs = append(diffs, difference{prefix + "_DEFINITION", name.String(), a, b})
		}
		if src.ansiNulls != dst.ansiNulls {
			diffs = append(diffs, difference{prefix + "_ANSI_NULLS", name.String(), onOff(src.ansiNulls), onOff(dst.ansiNulls)})
		}
		if src.quotedIdentifier != dst.quotedIdentifier {
			diffs = append(diffs, difference{prefix + "_QUOTED_IDENTIFIER", name.String(), onOff(src.quotedIdentifier), onOff(dst.quotedIdentifier)})
		}
	}
	return diffs
}

func onOff(value bool) string {
	if value {
		return "ON"
	}
	return "OFF"
}
