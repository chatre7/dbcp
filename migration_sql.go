package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// renderMigrationSQL produces a script, never a connection or an execution.
func renderMigrationSQL(destination *schema, steps []migrationStep, notes []string) (string, error) {
	if destination == nil || destination.migration == nil || destination.migration.databaseName == "" || destination.migration.serverName == "" {
		return "", fmt.Errorf("migration requires the actual destination database and server identity")
	}
	var sql strings.Builder
	sql.WriteString("-- Migrates supported SQL modules and synonyms only.\n-- Destination-only objects are not deleted.\n-- Catalog-based ordering cannot discover dynamic SQL dependencies.\n-- Review this script before running it in production.\n")
	for _, note := range notes {
		// Quoting escapes all newlines/control characters, so even hostile names
		// cannot terminate this line comment.
		sql.WriteString("-- Note: " + strconv.Quote(note) + "\n")
	}
	sql.WriteString("\nIF @@TRANCOUNT > 0\n    THROW 50000, N'Migration requires no existing transaction.', 1;\n")
	// Binary conversions enforce exact identity, including case and trailing
	// spaces, independently of the destination's collation.
	fmt.Fprintf(&sql, "IF DB_NAME() IS NULL OR CONVERT(varbinary(max), DB_NAME()) <> CONVERT(varbinary(max), %s)\n    THROW 50000, N'Migration destination database does not match.', 1;\n", migrationSQLLiteral(destination.migration.databaseName))
	fmt.Fprintf(&sql, "IF SERVERPROPERTY('ServerName') IS NULL OR CONVERT(varbinary(max), CONVERT(nvarchar(128), SERVERPROPERTY('ServerName'))) <> CONVERT(varbinary(max), %s)\n    THROW 50000, N'Migration destination server does not match.', 1;\n", migrationSQLLiteral(destination.migration.serverName))
	sql.WriteString("SET XACT_ABORT ON;\n\nBEGIN TRY\n    BEGIN TRANSACTION;\n")
	for _, step := range steps {
		if !validMigrationIdentifier(step.name.schema) || !validMigrationIdentifier(step.name.name) {
			return "", fmt.Errorf("invalid migration object name %s", step.name)
		}
		if step.object.typeCode == "SN" {
			if step.action != "CREATE" && step.action != "REPLACE" {
				return "", fmt.Errorf("unsupported synonym action %q for %s", step.action, step.name)
			}
			target, err := migrationSynonymTarget(step.object.target)
			if err != nil {
				return "", fmt.Errorf("synonym %s: %w", step.name, err)
			}
			if step.action == "REPLACE" {
				fmt.Fprintf(&sql, "\n    DROP SYNONYM %s;", step.name)
			}
			fmt.Fprintf(&sql, "\n    CREATE SYNONYM %s FOR %s;\n", step.name, target)
			continue
		}
		definition, err := migrationModuleDefinition(step)
		if err != nil {
			return "", fmt.Errorf("module %s: %w", step.name, err)
		}
		// QUOTED_IDENTIFIER is a parse-time setting. Isolate each module's
		// settings in its own batch so later modules cannot change its parsing.
		batch := fmt.Sprintf("SET ANSI_NULLS %s;\nSET QUOTED_IDENTIFIER %s;\nEXEC sys.sp_executesql %s;", onOff(step.object.ansiNulls), onOff(step.object.quotedIdentifier), migrationSQLLiteral(definition))
		fmt.Fprintf(&sql, "\n    EXEC sys.sp_executesql %s;\n", migrationSQLLiteral(batch))
	}
	sql.WriteString("\n    COMMIT TRANSACTION;\nEND TRY\nBEGIN CATCH\n    IF XACT_STATE() <> 0 ROLLBACK TRANSACTION;\n    THROW;\nEND CATCH;\n")
	return sql.String(), nil
}

func migrationSQLLiteral(text string) string {
	return "N'" + strings.ReplaceAll(text, "'", "''") + "'"
}

func migrationModuleDefinition(step migrationStep) (string, error) {
	if step.action != "CREATE" && step.action != "ALTER" {
		return "", fmt.Errorf("unsupported module action %q", step.action)
	}
	text := step.object.definition
	verbStart, verbEnd, ok := nextSQLHeaderWord(text, 0, nil)
	if !ok || (!strings.EqualFold(text[verbStart:verbEnd], "CREATE") && !strings.EqualFold(text[verbStart:verbEnd], "ALTER")) {
		return "", fmt.Errorf("expected a CREATE, ALTER, or CREATE OR ALTER module declaration")
	}
	kindStart, kindEnd, ok := nextSQLHeaderWord(text, verbEnd, nil)
	if !ok {
		return "", fmt.Errorf("missing module declaration kind")
	}
	if strings.EqualFold(text[verbStart:verbEnd], "CREATE") && strings.EqualFold(text[kindStart:kindEnd], "OR") {
		// Only the multiword verb needs normalization; ordinary CREATE/ALTER
		// headers retain their original whitespace as well as their comments.
		text = normalizeSQLDeclaration(text)
		verbStart, verbEnd, ok = nextSQLHeaderWord(text, 0, nil)
		if !ok {
			return "", fmt.Errorf("invalid module declaration")
		}
		kindStart, kindEnd, ok = nextSQLHeaderWord(text, verbEnd, nil)
		if !ok {
			return "", fmt.Errorf("missing module declaration kind")
		}
	}
	kind := strings.ToUpper(text[kindStart:kindEnd])
	validKind := false
	switch step.object.typeCode {
	case "P":
		validKind = kind == "PROC" || kind == "PROCEDURE"
	case "V":
		validKind = kind == "VIEW"
	case "FN", "IF", "TF":
		validKind = kind == "FUNCTION"
	}
	if !validKind {
		return "", fmt.Errorf("declaration %q does not match SQL object type %q", kind, step.object.typeCode)
	}
	nameStart, err := skipMigrationSQLTrivia(text, kindEnd)
	if err != nil {
		return "", err
	}
	parts, nameEnd, err := parseMigrationSQLName(text, nameStart, 2)
	if err != nil {
		return "", fmt.Errorf("invalid declared module name: %w", err)
	}
	next, err := skipMigrationSQLTrivia(text, nameEnd)
	if err != nil {
		return "", err
	}
	if next == len(text) {
		return "", fmt.Errorf("module declaration has no body")
	}
	if text[next] == ';' {
		return "", fmt.Errorf("numbered procedures and terminated declarations are unsupported")
	}
	// Check the boundary without parsing parameters or the trusted module body.
	// No executable SQL after the declared name is modified.
	wordStart, wordEnd, hasWord := nextSQLHeaderWord(text, next, nil)
	word := ""
	if hasWord {
		word = strings.ToUpper(text[wordStart:wordEnd])
	}
	validSuffix := false
	switch step.object.typeCode {
	case "P":
		validSuffix = text[next] == '@' || word == "AS" || word == "WITH" || word == "FOR"
	case "V":
		validSuffix = text[next] == '(' || word == "AS" || word == "WITH"
	case "FN", "IF", "TF":
		validSuffix = text[next] == '('
	}
	if !validSuffix {
		return "", fmt.Errorf("unexpected text after declared module name")
	}
	name := step.name.String()
	if len(parts) == 2 && parts[0] == step.name.schema && parts[1] == step.name.name {
		// Keep an already correct qualified spelling so applying the script does
		// not introduce a cosmetic difference on the next comparison.
		name = text[nameStart:nameEnd]
	}
	return text[:verbStart] + step.action + text[verbEnd:nameStart] + name + text[nameEnd:], nil
}

func migrationSynonymTarget(text string) (string, error) {
	parts, end, err := parseMigrationSQLName(text, 0, 4)
	if err != nil {
		return "", fmt.Errorf("invalid synonym target: %w", err)
	}
	end, err = skipMigrationSQLTrivia(text, end)
	if err != nil || end != len(text) {
		return "", fmt.Errorf("invalid synonym target: trailing SQL or malformed comment")
	}
	for i := range parts {
		parts[i] = quoteIdentifier(parts[i])
	}
	return strings.Join(parts, "."), nil
}

// parseMigrationSQLName accepts only nonempty identifier parts. Omitted/default
// qualification (db..name) is intentionally rejected rather than guessed.
func parseMigrationSQLName(text string, offset, maxParts int) ([]string, int, error) {
	var parts []string
	for {
		start, err := skipMigrationSQLTrivia(text, offset)
		if err != nil {
			return nil, 0, err
		}
		if start == len(text) {
			return nil, 0, fmt.Errorf("missing identifier")
		}
		offset = start
		var name string
		if text[offset] == '[' || text[offset] == '"' {
			close := byte(']')
			if text[offset] == '"' {
				close = '"'
			}
			offset++
			var decoded strings.Builder
			closed := false
			for offset < len(text) {
				if text[offset] != close {
					decoded.WriteByte(text[offset])
					offset++
					continue
				}
				offset++
				if offset < len(text) && text[offset] == close {
					decoded.WriteByte(close)
					offset++
					continue
				}
				closed = true
				break
			}
			if !closed {
				return nil, 0, fmt.Errorf("unterminated quoted identifier")
			}
			name = decoded.String()
		} else {
			for offset < len(text) {
				r, size := utf8.DecodeRuneInString(text[offset:])
				valid := unicode.IsLetter(r) || r == '_' || r == '@' || r == '#'
				if offset > start {
					valid = valid || unicode.IsDigit(r) || r == '$' || unicode.IsMark(r)
				}
				if !valid {
					break
				}
				offset += size
			}
			name = text[start:offset]
		}
		if !validMigrationIdentifier(name) {
			return nil, 0, fmt.Errorf("empty, oversized, or invalid identifier")
		}
		parts = append(parts, name)
		next, err := skipMigrationSQLTrivia(text, offset)
		if err != nil {
			return nil, 0, err
		}
		if next == len(text) || text[next] != '.' {
			return parts, offset, nil
		}
		if len(parts) == maxParts {
			return nil, 0, fmt.Errorf("unexpected qualification: more than %d parts", maxParts)
		}
		offset = next + 1
	}
}

func validMigrationIdentifier(name string) bool {
	if name == "" || !utf8.ValidString(name) || strings.IndexByte(name, 0) >= 0 {
		return false
	}
	// sysname is nvarchar(128); supplementary characters occupy two UTF-16 units.
	units := 0
	for _, r := range name {
		units++
		if r > 0xffff {
			units++
		}
	}
	return units <= 128
}

func skipMigrationSQLTrivia(text string, offset int) (int, error) {
	for offset < len(text) {
		switch text[offset] {
		case ' ', '\t', '\r', '\n', '\v', '\f':
			offset++
			continue
		}
		if strings.HasPrefix(text[offset:], "--") {
			end := strings.IndexAny(text[offset:], "\r\n")
			if end < 0 {
				return len(text), nil
			}
			offset += end + 1
			continue
		}
		if !strings.HasPrefix(text[offset:], "/*") {
			return offset, nil
		}
		offset += 2
		depth := 1
		for offset < len(text) && depth > 0 {
			switch {
			case strings.HasPrefix(text[offset:], "/*"):
				depth++
				offset += 2
			case strings.HasPrefix(text[offset:], "*/"):
				depth--
				offset += 2
			default:
				offset++
			}
		}
		if depth != 0 {
			return 0, fmt.Errorf("unterminated SQL comment")
		}
	}
	return offset, nil
}
