// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// mariadbQuoteIdent quotes one identifier and escapes embedded backticks by doubling them.
// Dots remain part of the identifier; this helper does not parse qualified names.
func mariadbQuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// mariadbQuotedColumn resolves the original column name and quotes it for conditions.
// A dot separates alias and column: t1.col becomes `t1`.`col`.
func mariadbQuotedColumn(property *interfaces.Property) string {
	name := mariadbOriginalName(property)
	if idx := strings.Index(name, "."); idx >= 0 {
		alias := strings.TrimSpace(name[:idx])
		col := strings.TrimSpace(name[idx+1:])
		return mariadbQuoteIdent(alias) + "." + mariadbQuoteIdent(col)
	}
	return mariadbQuoteIdent(strings.TrimSpace(name))
}

// mariadbQuoteColumnName quotes a column or alias-qualified column name.
// A dot separates alias and column: t1.col becomes `t1`.`col`.
func mariadbQuoteColumnName(name string) string {
	if name == "" {
		return mariadbQuoteIdent(name)
	}
	if idx := strings.Index(name, "."); idx >= 0 {
		alias := strings.TrimSpace(name[:idx])
		col := strings.TrimSpace(name[idx+1:])
		return mariadbQuoteIdent(alias) + "." + mariadbQuoteIdent(col)
	}
	return mariadbQuoteIdent(strings.TrimSpace(name))
}

// mariadbOriginalName returns OriginalName, falling back to the resource property name.
func mariadbOriginalName(property *interfaces.Property) string {
	if property.OriginalName != "" {
		return property.OriginalName
	}
	return property.Name
}

// BuildSourceIdentifier quotes raw database and table names as a SQL-ready reference.
// Database falls back to Schema; the caller supplies complete name components.
func (c *MariaDBConnector) BuildSourceIdentifier(meta *interfaces.TableMeta) string {
	namespace := meta.Database
	if namespace == "" {
		namespace = meta.Schema
	}

	return mariadbQuoteIdent(namespace) + "." + mariadbQuoteIdent(meta.Name)
}

// BuildOriginalName joins raw database and table names without SQL quoting for display or CDC.
// Database falls back to Schema; the caller supplies complete name components.
func (c *MariaDBConnector) BuildOriginalName(meta *interfaces.TableMeta) string {
	namespace := meta.Database
	if namespace == "" {
		namespace = meta.Schema
	}
	return namespace + "." + meta.Name
}
