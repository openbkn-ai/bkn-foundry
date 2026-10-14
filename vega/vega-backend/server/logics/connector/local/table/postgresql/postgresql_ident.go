// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// pgQuoteIdent quotes one identifier and escapes embedded double quotes by doubling them.
// Dots remain part of the identifier; this helper does not parse qualified names.
func pgQuoteIdent(s string) string {
	if s == "" {
		return `""`
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// pgQuoteColumnName quotes a column or alias-qualified column name.
// A dot separates alias and column: t1.col becomes "t1"."col".
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

// BuildSourceIdentifier returns a SQL-ready table reference without connecting to the source.
func (c *PostgresqlConnector) BuildSourceIdentifier(meta *interfaces.TableMeta) string {
	return pgQuoteIdent(meta.Schema) + "." + pgQuoteIdent(meta.Name)
}

// BuildOriginalName returns the unquoted source name without connecting to the source.
func (c *PostgresqlConnector) BuildOriginalName(meta *interfaces.TableMeta) string {
	return meta.Schema + "." + meta.Name
}
