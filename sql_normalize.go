package main

import "strings"

type sqlCompareMode string

const (
	sqlModeStrict     sqlCompareMode = "strict"
	sqlModeNormalized sqlCompareMode = "normalized"
)

// normalizeSQLDeclaration only rewrites a module's leading declaration verbs.
// Everything from PROC/PROCEDURE/VIEW/FUNCTION onwards is left byte-for-byte
// intact. Leading comments are retained, and comments between declaration
// keywords are retained in order. This is not a semantic SQL comparison.
func normalizeSQLDeclaration(text string) string {
	start, end, ok := nextSQLHeaderWord(text, 0, nil)
	if !ok {
		return text
	}
	verb := text[start:end]
	if !strings.EqualFold(verb, "CREATE") && !strings.EqualFold(verb, "ALTER") {
		return text
	}

	var comments strings.Builder
	typeStart, typeEnd, ok := nextSQLHeaderWord(text, end, &comments)
	if !ok {
		return text
	}
	if strings.EqualFold(verb, "CREATE") && strings.EqualFold(text[typeStart:typeEnd], "OR") {
		alterStart, alterEnd, ok := nextSQLHeaderWord(text, typeEnd, &comments)
		if !ok || !strings.EqualFold(text[alterStart:alterEnd], "ALTER") {
			return text
		}
		typeStart, typeEnd, ok = nextSQLHeaderWord(text, alterEnd, &comments)
		if !ok {
			return text
		}
	}
	kind := text[typeStart:typeEnd]
	if !strings.EqualFold(kind, "PROC") && !strings.EqualFold(kind, "PROCEDURE") &&
		!strings.EqualFold(kind, "VIEW") && !strings.EqualFold(kind, "FUNCTION") {
		return text
	}
	prefix := "CREATE" + comments.String() + " "
	if text[start:typeStart] == prefix {
		return text
	}
	return text[:start] + prefix + text[typeStart:]
}

// nextSQLHeaderWord reads one unquoted word, skipping SQL whitespace and
// comments (including nested block comments). It never scans past a quoted
// identifier/string or other punctuation, so text inside them cannot be
// mistaken for a declaration. Unrecognized input stays strict-comparable.
func nextSQLHeaderWord(text string, offset int, comments *strings.Builder) (int, int, bool) {
	for offset < len(text) {
		switch text[offset] {
		case ' ', '\t', '\r', '\n', '\v', '\f':
			offset++
			continue
		}
		start := offset
		switch {
		case strings.HasPrefix(text[offset:], "--"):
			end := strings.IndexByte(text[offset:], '\n')
			if end < 0 {
				return 0, 0, false
			}
			offset += end + 1
		case strings.HasPrefix(text[offset:], "/*"):
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
				return 0, 0, false
			}
		default:
			for offset < len(text) && sqlWordByte(text[offset]) {
				offset++
			}
			return start, offset, offset > start
		}
		if comments != nil {
			comments.WriteByte(' ')
			comments.WriteString(text[start:offset])
		}
	}
	return 0, 0, false
}

func sqlWordByte(b byte) bool {
	// Treat non-ASCII bytes as identifier content, not a keyword boundary.
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' ||
		b >= '0' && b <= '9' || b == '_' || b == '@' || b == '#' || b == '$' || b >= 0x80
}
