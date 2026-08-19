// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knrunsql provides the shared read-only SQL execution service used by
// both the MCP run_sql tool and the internal REST endpoint.
package knrunsql

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// run_sql Read-only SQL guard.
//
// Vega's original query interface will make the final decision based on the SQLGlot AST strategy, rejecting non-top-level SELECT, WITH/CTE,
// Set operations and writing/DDL. Here we use "stripping comments and string literals before doing lexical determination" for defense in depth:
// First eliminate the comments/strings/placeholders that can hide keywords, and then require a single statement, starting with SELECT/WITH, without the write/DDL keyword.
// It is not a full SQL parser and is not a replacement for final policy validation on the vega side.

var (
	// resourcePlaceholderRe is consistent with vega extractResourceIDs: {{.resource_id}} or {{resource_id}}.
	resourcePlaceholderRe = regexp.MustCompile(`\{\{\.?([a-z0-9][a-z0-9_-]{0,39})\}\}`)
	// anyPlaceholderRe replaces the entire placeholder before keyword determination to avoid misjudgments triggered by internal words such as {{.delete}}.
	anyPlaceholderRe = regexp.MustCompile(`\{\{[^}]*\}\}`)
	// startsWithSelectRe only checks the entry form, allowing leading whitespace and left parentheses; set operations may pass local guards,
	// But will be rejected by vega's final strategy.
	startsWithSelectRe = regexp.MustCompile(`(?is)^[\s(]*(SELECT|WITH)\b`)
	// forbiddenKeywordRe writes / DDL / permissions / process keyword blacklist (determined after stripping comments and strings).
	forbiddenKeywordRe = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|TRUNCATE|GRANT|REVOKE|REPLACE|MERGE|UPSERT|CALL|EXEC|EXECUTE|RENAME|LOAD|COPY|INTO|ATTACH|DETACH|USE|VACUUM|ANALYZE|REFRESH|COMMENT|PREPARE|DEALLOCATE)\b`)
)

// ExtractResourceIDs Extracts all resource_ids within {{.resource_id}} placeholders from SQL (removes duplication, preserves order).
func ExtractResourceIDs(sql string) []string {
	matches := resourcePlaceholderRe.FindAllStringSubmatch(sql, -1)
	ids := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids
}

// stripSQLNoise removes line comments (-- and #), block comments (/* */), MySQL strings and backtick identifiers,
// To prevent hidden keywords or semicolons from interfering with guard judgment.
func stripSQLNoise(sql string) string {
	var b strings.Builder
	runes := []rune(sql)
	n := len(runes)
	for i := 0; i < n; i++ {
		c := runes[i]
		switch {
		case c == '-' && i+1 < n && runes[i+1] == '-' &&
			(i+2 >= n || unicode.IsSpace(runes[i+2]) || unicode.IsControl(runes[i+2])): // MySQL line comments --.
			for i < n && runes[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
		case c == '#': // Line comments #.
			for i < n && runes[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
		case c == '/' && i+1 < n && runes[i+1] == '*': // Block comments /* */.
			i += 2
			for i+1 < n && !(runes[i] == '*' && runes[i+1] == '/') {
				i++
			}
			i++ // Skip the trailing '/'.
			b.WriteByte(' ')
		case c == '\'' || c == '"': // MySQL single/double quote string (backslash and repeated quote escape)
			i = skipQuotedSQLLiteral(runes, i, c)
			b.WriteByte(' ')
		case c == '`': // backtick identifier.
			i = skipQuotedSQLLiteral(runes, i, c)
			b.WriteByte(' ')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// skipQuotedSQLLiteral Returns the ending position of a MySQL string or backtick identifier starting at openingQuote.
// Paired quotes do not prematurely terminate a literal; backslash escaping takes effect on string literals,
// Backslashes within backtick identifiers are ordinary characters.
func skipQuotedSQLLiteral(runes []rune, start int, quote rune) int {
	for i := start + 1; i < len(runes); i++ {
		if quote != '`' && runes[i] == '\\' && i+1 < len(runes) {
			i++
			continue
		}
		if runes[i] == quote {
			if i+1 < len(runes) && runes[i+1] == quote {
				i++
				continue
			}
			return i
		}
	}
	return len(runes) - 1
}

// EnsureReadOnlySQL verifies that the SQL is a single read-only SELECT/WITH query; an error is returned for violations.
func EnsureReadOnlySQL(sql string) error {
	if strings.TrimSpace(sql) == "" {
		return fmt.Errorf("sql is empty")
	}

	cleaned := stripSQLNoise(sql)
	// The placeholder is replaced entirely with neutral words to prevent internal words from triggering keyword determination.
	cleaned = anyPlaceholderRe.ReplaceAllString(cleaned, " _rid_ ")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return fmt.Errorf("sql has no executable statement")
	}

	// After removing the trailing semicolon and whitespace, no more semicolons are allowed (that is, multiple statements are prohibited).
	trimmed := strings.TrimRight(cleaned, "; \t\r\n")
	if strings.Contains(trimmed, ";") {
		return fmt.Errorf("multiple statements are not allowed; only a single read-only SELECT is permitted")
	}

	if !startsWithSelectRe.MatchString(trimmed) {
		return fmt.Errorf("only read-only queries are allowed: statement must start with SELECT or WITH")
	}

	if loc := forbiddenKeywordRe.FindString(trimmed); loc != "" {
		return fmt.Errorf("forbidden keyword %q detected: run_sql is read-only (no writes/DDL)", strings.ToUpper(loc))
	}

	return nil
}
