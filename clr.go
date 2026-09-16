package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

type clrFunction struct {
	assemblyName     string
	assemblyIdentity string
	assemblySHA256   string
	permissionSet    string
	className        string
	methodName       string
	signature        string
	executeAs        string
	nullOnNullInput  bool
}

func isCLRFunctionType(typeCode string) bool {
	return typeCode == "FS" || typeCode == "FT"
}

// LEFT JOINs deliberately retain objects with inaccessible binding metadata.
const clrFunctionsQuery = `
SELECT s.name, o.name, o.type, am.assembly_id,
       a.name, a.clr_name, a.permission_set_desc,
       am.assembly_class, am.assembly_method, am.null_on_null_input,
       CASE WHEN am.execute_as_principal_id IS NULL THEN N'CALLER'
            WHEN am.execute_as_principal_id = -2 THEN N'OWNER'
            ELSE N'PRINCIPAL ' + QUOTENAME(dp.name) END
FROM sys.objects AS o
JOIN sys.schemas AS s ON s.schema_id = o.schema_id
LEFT JOIN sys.assembly_modules AS am ON am.object_id = o.object_id
LEFT JOIN sys.assemblies AS a ON a.assembly_id = am.assembly_id
LEFT JOIN sys.database_principals AS dp ON dp.principal_id = am.execute_as_principal_id
WHERE o.is_ms_shipped = 0 AND o.type IN ('FS', 'FT')`

// Fetch only the primary DLL, once per referenced assembly. Neither ancillary
// files nor dependencies are fingerprints of this function's implementing DLL.
const clrAssemblyFilesQuery = `
SELECT used.assembly_id, af.content
FROM (
    SELECT DISTINCT am.assembly_id
    FROM sys.objects AS o
    JOIN sys.assembly_modules AS am ON am.object_id = o.object_id
    WHERE o.is_ms_shipped = 0 AND o.type IN ('FS', 'FT')
) AS used
LEFT JOIN sys.assembly_files AS af ON af.assembly_id = used.assembly_id AND af.file_id = 1`

// Serialize sql_variant defaults on the server without relying on the driver's
// sql_variant conversions. Binary and floating-point values use lossless hex;
// temporal values use ISO text and money keeps all four fractional digits.
const clrSignatureQuery = `
WITH members AS (
    SELECT p.object_id, 'P' AS kind, p.parameter_id AS ordinal, p.name,
           p.user_type_id, p.system_type_id, p.max_length, p.precision, p.scale,
           p.is_nullable, p.xml_collection_id, p.is_xml_document,
           p.is_output, p.is_readonly, p.has_default_value, p.default_value,
           CAST(NULL AS sysname) AS collation_name
    FROM sys.parameters AS p
    JOIN sys.objects AS o ON o.object_id = p.object_id
    WHERE o.is_ms_shipped = 0 AND o.type IN ('FS', 'FT')
    UNION ALL
    SELECT c.object_id, 'C', c.column_id, c.name,
           c.user_type_id, c.system_type_id, c.max_length, c.precision, c.scale,
           c.is_nullable, c.xml_collection_id, c.is_xml_document,
           CAST(0 AS bit), CAST(0 AS bit), CAST(0 AS bit), CAST(NULL AS sql_variant),
           c.collation_name
    FROM sys.columns AS c
    JOIN sys.objects AS o ON o.object_id = c.object_id
    WHERE o.is_ms_shipped = 0 AND o.type = 'FT'
)
SELECT s.name, o.name, m.kind, m.ordinal, m.name,
       ts.name, ty.name, COALESCE(bt.name, ty.name), ty.is_user_defined,
       m.max_length, m.precision, m.scale, m.is_nullable,
       CAST(CASE WHEN m.xml_collection_id <> 0 THEN 1 ELSE 0 END AS bit),
       xs.name, xc.name, m.is_xml_document,
       m.is_output, m.is_readonly, m.collation_name, m.has_default_value,
       CONVERT(nvarchar(128), SQL_VARIANT_PROPERTY(m.default_value, 'BaseType')),
       CONVERT(int, SQL_VARIANT_PROPERTY(m.default_value, 'MaxLength')),
       CONVERT(int, SQL_VARIANT_PROPERTY(m.default_value, 'Precision')),
       CONVERT(int, SQL_VARIANT_PROPERTY(m.default_value, 'Scale')),
       CONVERT(nvarchar(128), SQL_VARIANT_PROPERTY(m.default_value, 'Collation')),
       CASE
           WHEN SQL_VARIANT_PROPERTY(m.default_value, 'BaseType') IN ('binary', 'varbinary', 'float', 'real')
               THEN N'0x' + CONVERT(nvarchar(max), CONVERT(varbinary(max), m.default_value), 2)
           WHEN SQL_VARIANT_PROPERTY(m.default_value, 'BaseType') IN ('money', 'smallmoney')
               THEN CONVERT(nvarchar(max), CONVERT(money, m.default_value), 2)
           ELSE CONVERT(nvarchar(max), m.default_value, 126)
       END
FROM members AS m
JOIN sys.objects AS o ON o.object_id = m.object_id
JOIN sys.schemas AS s ON s.schema_id = o.schema_id
LEFT JOIN sys.types AS ty ON ty.user_type_id = m.user_type_id
LEFT JOIN sys.schemas AS ts ON ts.schema_id = ty.schema_id
LEFT JOIN sys.types AS bt ON bt.user_type_id = m.system_type_id AND bt.system_type_id = bt.user_type_id
LEFT JOIN sys.xml_schema_collections AS xc ON xc.xml_collection_id = m.xml_collection_id AND m.xml_collection_id <> 0
LEFT JOIN sys.schemas AS xs ON xs.schema_id = xc.schema_id`

type clrSignatureMember struct {
	ordinal                    int
	name, dataType, collation  string
	nullable, output, readonly bool
	defaultValue               string
}

type clrSignature struct {
	typeCode   string
	parameters []clrSignatureMember
	columns    []clrSignatureMember
}

func loadCLRFunctions(ctx context.Context, db *sql.DB) (map[objectName]*clrFunction, error) {
	rows, err := db.QueryContext(ctx, clrFunctionsQuery)
	if err != nil {
		return nil, fmt.Errorf("read CLR function bindings: %w", err)
	}
	defer rows.Close()
	functions := make(map[objectName]*clrFunction)
	signatures := make(map[objectName]*clrSignature)
	assemblies := make(map[int][]*clrFunction)
	for rows.Next() {
		var name objectName
		var typeCode string
		var assemblyID int
		f := &clrFunction{}
		if err := rows.Scan(&name.schema, &name.name, &typeCode, &assemblyID,
			&f.assemblyName, &f.assemblyIdentity, &f.permissionSet, &f.className,
			&f.methodName, &f.nullOnNullInput, &f.executeAs); err != nil {
			return nil, fmt.Errorf("decode CLR binding for %s (metadata may be unavailable): %w", name, err)
		}
		if f.assemblyName == "" || f.assemblyIdentity == "" || f.permissionSet == "" || f.className == "" || f.methodName == "" || f.executeAs == "" {
			return nil, fmt.Errorf("incomplete CLR binding metadata for %s", name)
		}
		functions[name] = f
		signatures[name] = &clrSignature{typeCode: strings.TrimSpace(typeCode)}
		assemblies[assemblyID] = append(assemblies[assemblyID], f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read CLR binding rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close CLR binding rows: %w", err)
	}
	if len(functions) == 0 {
		return functions, nil
	}
	if err := loadCLRAssemblyHashes(ctx, db, assemblies); err != nil {
		return nil, err
	}
	if err := loadCLRSignatures(ctx, db, signatures); err != nil {
		return nil, err
	}
	for name, signature := range signatures {
		text, err := signature.format()
		if err != nil {
			return nil, fmt.Errorf("CLR signature for %s: %w", name, err)
		}
		functions[name].signature = text
	}
	return functions, nil
}

func loadCLRAssemblyHashes(ctx context.Context, db *sql.DB, assemblies map[int][]*clrFunction) error {
	rows, err := db.QueryContext(ctx, clrAssemblyFilesQuery)
	if err != nil {
		return fmt.Errorf("read CLR assembly DLLs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var content sql.RawBytes
		if err := rows.Scan(&id, &content); err != nil {
			return fmt.Errorf("decode CLR assembly DLL: %w", err)
		}
		functions, ok := assemblies[id]
		if !ok {
			return fmt.Errorf("CLR assembly metadata changed while reading")
		}
		if len(content) == 0 {
			return fmt.Errorf("primary DLL bytes unavailable for CLR assembly %q", functions[0].assemblyName)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(content))
		for _, f := range functions {
			f.assemblySHA256 = hash
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read CLR assembly DLL rows: %w", err)
	}
	for _, functions := range assemblies {
		if functions[0].assemblySHA256 == "" {
			return fmt.Errorf("primary DLL bytes unavailable for CLR assembly %q", functions[0].assemblyName)
		}
	}
	return rows.Close()
}

func loadCLRSignatures(ctx context.Context, db *sql.DB, signatures map[objectName]*clrSignature) error {
	rows, err := db.QueryContext(ctx, clrSignatureQuery)
	if err != nil {
		return fmt.Errorf("read CLR signatures: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name objectName
		var member clrSignatureMember
		var kind, typeSchema, typeName, baseType string
		var xmlSchema, xmlCollection, collation sql.NullString
		var userDefined, typedXML, xmlDocument, hasDefault bool
		var length, precision, scale int
		var defaultType, defaultCollation, defaultValue sql.NullString
		var defaultLength, defaultPrecision, defaultScale sql.NullInt64
		if err := rows.Scan(&name.schema, &name.name, &kind, &member.ordinal, &member.name,
			&typeSchema, &typeName, &baseType, &userDefined, &length, &precision, &scale, &member.nullable,
			&typedXML, &xmlSchema, &xmlCollection, &xmlDocument,
			&member.output, &member.readonly, &collation, &hasDefault,
			&defaultType, &defaultLength, &defaultPrecision, &defaultScale, &defaultCollation, &defaultValue); err != nil {
			return fmt.Errorf("decode CLR signature for %s (metadata may be unavailable): %w", name, err)
		}
		if typeSchema == "" || typeName == "" || baseType == "" {
			return fmt.Errorf("type metadata unavailable for CLR signature %s", name)
		}
		member.dataType = formatType(baseType, length, precision, scale)
		if userDefined {
			member.dataType = quoteIdentifier(typeSchema) + "." + quoteIdentifier(typeName) + " (base: " + member.dataType + ")"
		} else if typeName != baseType {
			member.dataType = typeName + " (base: " + member.dataType + ")"
		}
		if typedXML {
			if !xmlSchema.Valid || !xmlCollection.Valid || xmlSchema.String == "" || xmlCollection.String == "" {
				return fmt.Errorf("XML collection metadata unavailable for CLR signature %s", name)
			}
			content := "CONTENT"
			if xmlDocument {
				content = "DOCUMENT"
			}
			member.dataType += "(" + content + " " + quoteIdentifier(xmlSchema.String) + "." + quoteIdentifier(xmlCollection.String) + ")"
		}
		member.collation = collation.String
		if kind == "C" {
			switch baseType {
			case "char", "varchar", "nchar", "nvarchar", "text", "ntext":
				if !collation.Valid || collation.String == "" {
					return fmt.Errorf("return column collation unavailable for CLR signature %s", name)
				}
			}
		}
		if hasDefault {
			if !defaultValue.Valid {
				if defaultType.Valid {
					return fmt.Errorf("default value unavailable for CLR signature %s", name)
				}
				member.defaultValue = "NULL"
			} else {
				if !defaultType.Valid || !defaultLength.Valid || !defaultPrecision.Valid || !defaultScale.Valid {
					return fmt.Errorf("default value type metadata unavailable for CLR signature %s", name)
				}
				switch defaultType.String {
				case "char", "varchar", "nchar", "nvarchar":
					if !defaultCollation.Valid || defaultCollation.String == "" {
						return fmt.Errorf("default collation unavailable for CLR signature %s", name)
					}
				}
				member.defaultValue = formatCLRDefault(defaultType.String, int(defaultLength.Int64), int(defaultPrecision.Int64), int(defaultScale.Int64), defaultCollation.String, defaultValue.String)
			}
		}
		signature := signatures[name]
		if signature == nil {
			return fmt.Errorf("CLR function metadata changed while reading %s", name)
		}
		if kind == "P" {
			signature.parameters = append(signature.parameters, member)
		} else {
			signature.columns = append(signature.columns, member)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read CLR signature rows: %w", err)
	}
	return rows.Close()
}

func formatCLRDefault(typeName string, length, precision, scale int, collation, value string) string {
	switch typeName {
	case "nvarchar", "nchar":
		value = "N'" + strings.ReplaceAll(value, "'", "''") + "'"
	case "varchar", "char", "date", "time", "datetime", "smalldatetime", "datetime2", "datetimeoffset", "uniqueidentifier":
		value = "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	result := formatType(typeName, length, precision, scale) + ": " + value
	if collation != "" {
		result += " COLLATE " + quoteIdentifier(collation)
	}
	return result
}

func (m clrSignatureMember) String() string {
	text := m.dataType
	if m.name != "" {
		text = quoteIdentifier(m.name) + " " + text
	}
	if m.collation != "" {
		text += " COLLATE " + quoteIdentifier(m.collation)
	}
	if m.nullable {
		text += " NULL"
	} else {
		text += " NOT NULL"
	}
	if m.output {
		text += " OUTPUT"
	}
	if m.readonly {
		text += " READONLY"
	}
	if m.defaultValue != "" {
		text += " DEFAULT " + m.defaultValue
	}
	return text
}

func (s *clrSignature) format() (string, error) {
	slices.SortFunc(s.parameters, func(a, b clrSignatureMember) int { return a.ordinal - b.ordinal })
	slices.SortFunc(s.columns, func(a, b clrSignatureMember) int { return a.ordinal - b.ordinal })
	parameters := make([]string, 0, len(s.parameters))
	var scalarReturn string
	for i, member := range s.parameters {
		if member.ordinal < 0 || (i > 0 && member.ordinal == s.parameters[i-1].ordinal) {
			return "", fmt.Errorf("invalid or duplicate parameter ordinal")
		}
		if member.ordinal == 0 {
			scalarReturn = member.String()
		} else {
			if member.name == "" {
				return "", fmt.Errorf("parameter name unavailable")
			}
			parameters = append(parameters, member.String())
		}
	}
	prefix := "(" + strings.Join(parameters, ", ") + ") RETURNS "
	if s.typeCode == "FS" {
		if scalarReturn == "" || len(s.columns) != 0 {
			return "", fmt.Errorf("scalar return metadata unavailable or inconsistent")
		}
		return prefix + scalarReturn, nil
	}
	if s.typeCode != "FT" || scalarReturn != "" || len(s.columns) == 0 {
		return "", fmt.Errorf("table return metadata unavailable or inconsistent")
	}
	columns := make([]string, 0, len(s.columns))
	for i, member := range s.columns {
		if member.ordinal <= 0 || member.name == "" || (i > 0 && member.ordinal == s.columns[i-1].ordinal) {
			return "", fmt.Errorf("invalid return column metadata")
		}
		columns = append(columns, member.String())
	}
	return prefix + "TABLE (" + strings.Join(columns, ", ") + ")", nil
}
