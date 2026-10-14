// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package mariadb provides MariaDB database connector implementation.
package mariadb

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// mariadbQuoteIdent 引用单个标识符，将内部反引号转义为两个反引号。
func mariadbQuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// mariadbQuotedColumn returns a safely quoted physical source column name.
func mariadbQuotedColumn(property *interfaces.Property) string {
	name := mariadbOriginalName(property)
	if idx := strings.Index(name, "."); idx >= 0 {
		alias := strings.TrimSpace(name[:idx])
		col := strings.TrimSpace(name[idx+1:])
		return mariadbQuoteIdent(alias) + "." + mariadbQuoteIdent(col)
	}
	return mariadbQuoteIdent(strings.TrimSpace(name))
}

// mariadbQuoteColumnName converts column names to SQL identifiers; Support "alias.col" -> "alias.col"
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

// mariadbOriginalName selects the source column name when one is available.
func mariadbOriginalName(property *interfaces.Property) string {
	if property.OriginalName != "" {
		return property.OriginalName
	}
	return property.Name
}
