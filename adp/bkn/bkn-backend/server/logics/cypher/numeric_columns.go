// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// Vega preserves DECIMAL as text. Cypher exposes typed JSON numbers without
// passing exact decimal digits through a binary floating-point conversion.
func decimalColumnsAsNumbers(columns []interfaces.RawQueryColumn, rows []map[string]any) error {
	for _, column := range columns {
		typ := strings.ToLower(strings.TrimSpace(column.Type))
		if base, _, ok := strings.Cut(typ, "("); ok {
			typ = strings.TrimSpace(base)
		}
		if typ != "decimal" && typ != "numeric" {
			continue
		}
		for _, row := range rows {
			value, ok := row[column.Name].(string)
			if !ok {
				continue
			}
			number := json.Number(value)
			if value == "" {
				return fmt.Errorf("invalid decimal returned for column %q", column.Name)
			}
			if _, err := json.Marshal(number); err != nil {
				return fmt.Errorf("invalid decimal returned for column %q", column.Name)
			}
			row[column.Name] = number
		}
	}
	return nil
}
