// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"strings"
)

// pgQuoteIdent double quotes escape PostgreSQL identifiers.
func pgQuoteIdent(s string) string {
	if s == "" {
		return `""`
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// pgQuoteColumnName column name/alias qualification; Support "alias.col" -> "alias.col".
func pgQuoteColumnName(name string) string {
	if name == "" {
		return `""`
	}
	if idx := strings.Index(name, "."); idx >= 0 {
		alias := strings.TrimSpace(name[:idx])
		col := strings.TrimSpace(name[idx+1:])
		return pgQuoteIdent(alias) + "." + pgQuoteIdent(col)
	}
	return pgQuoteIdent(strings.TrimSpace(name))
}
