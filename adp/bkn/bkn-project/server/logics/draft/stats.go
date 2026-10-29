// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// counter computes the full-table counts that check keys and relations, and reads the values of
// code tables. Only counts and code-table values come back; no other data leaves vega-backend.
type counter interface {
	keyStats(ctx context.Context, table *interfaces.VegaTable, field string) (rows, distinct, nulls int64, err error)
	keyTupleStats(ctx context.Context, table *interfaces.VegaTable, fields []string) (rows, distinct, nulls int64, err error)
	joinStats(ctx context.Context, source *interfaces.VegaTable, field string, target *interfaces.VegaTable,
		key string) (rows, nulls, orphans, distinct int64, err error)
	codeValues(ctx context.Context, table *interfaces.VegaTable, code, name string, limit int64) ([]interfaces.DraftEnumValue, error)
}

// vegaCounter counts through vega-backend's SQL route as the drafting account, so the account
// needs query permission on the tables.
type vegaCounter struct {
	va             interfaces.VegaAccess
	account        interfaces.VegaAccount
	timeoutSeconds int
}

// quote writes a field name as a MySQL identifier; vega-backend transpiles to the catalog's dialect.
func quote(field string) string {
	return "`" + strings.ReplaceAll(field, "`", "``") + "`"
}

func ref(table *interfaces.VegaTable) string {
	return "{{" + table.ID + "}}"
}

func (c *vegaCounter) one(ctx context.Context, sql string) (map[string]any, error) {
	rows, err := c.va.Query(ctx, c.account, sql, c.timeoutSeconds)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("expected one row, got %d", len(rows))
	}
	return rows[0], nil
}

func (c *vegaCounter) keyStats(ctx context.Context, table *interfaces.VegaTable,
	field string) (int64, int64, int64, error) {
	f := quote(field)
	row, err := c.one(ctx, fmt.Sprintf(
		"SELECT COUNT(*) AS total, COUNT(DISTINCT %s) AS distinct_values, "+
			"SUM(CASE WHEN %s IS NULL THEN 1 ELSE 0 END) AS nulls FROM %s",
		f, f, ref(table)))
	if err != nil {
		return 0, 0, 0, err
	}
	return number(row["total"]), number(row["distinct_values"]), number(row["nulls"]), nil
}

// keyTupleStats counts a key of several fields: the rows, the rows with any of them null, and
// the distinct keys of the rows with none null, as keyStats counts one field. One query over the
// groups of the key's values keeps the counts to one scan of the same rows; a group is complete
// (c = 1) or has a null field (c = 0) for all of its rows. Grouping keeps it to SQL every dialect
// has; COUNT(DISTINCT a, b) is MySQL's.
func (c *vegaCounter) keyTupleStats(ctx context.Context, table *interfaces.VegaTable,
	fields []string) (int64, int64, int64, error) {
	quoted := make([]string, 0, len(fields))
	isNull := make([]string, 0, len(fields))
	for _, field := range fields {
		f := quote(field)
		quoted, isNull = append(quoted, f), append(isNull, f+" IS NULL")
	}
	row, err := c.one(ctx, fmt.Sprintf(
		"SELECT SUM(n) AS total, SUM(CASE WHEN c = 0 THEN n ELSE 0 END) AS nulls, SUM(c) AS distinct_values "+
			"FROM (SELECT COUNT(*) AS n, MIN(CASE WHEN %s THEN 0 ELSE 1 END) AS c FROM %s GROUP BY %s) k",
		strings.Join(isNull, " OR "), ref(table), strings.Join(quoted, ", ")))
	if err != nil {
		return 0, 0, 0, err
	}
	return number(row["total"]), number(row["distinct_values"]), number(row["nulls"]), nil
}

func (c *vegaCounter) joinStats(ctx context.Context, source *interfaces.VegaTable, field string,
	target *interfaces.VegaTable, key string) (int64, int64, int64, int64, error) {
	f, k := quote(field), quote(key)
	// Joining the distinct keys keeps a non-unique target key from multiplying source rows.
	row, err := c.one(ctx, fmt.Sprintf(
		"SELECT COUNT(*) AS total, "+
			"SUM(CASE WHEN s.%s IS NULL THEN 1 ELSE 0 END) AS nulls, "+
			"SUM(CASE WHEN s.%s IS NOT NULL AND t.%s IS NULL THEN 1 ELSE 0 END) AS orphans, "+
			"COUNT(DISTINCT s.%s) AS distinct_values "+
			"FROM %s s LEFT JOIN (SELECT DISTINCT %s FROM %s) t ON s.%s = t.%s",
		f, f, k, f, ref(source), k, ref(target), f, k))
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return number(row["total"]), number(row["nulls"]), number(row["orphans"]), number(row["distinct_values"]), nil
}

func (c *vegaCounter) codeValues(ctx context.Context, table *interfaces.VegaTable, code string,
	name string, limit int64) ([]interfaces.DraftEnumValue, error) {
	rows, err := c.va.Query(ctx, c.account, fmt.Sprintf(
		"SELECT %s AS code_value, %s AS display_value FROM %s ORDER BY %s LIMIT %d",
		quote(code), quote(name), ref(table), quote(code), limit), c.timeoutSeconds)
	if err != nil {
		return nil, err
	}
	values := make([]interfaces.DraftEnumValue, 0, len(rows))
	for _, row := range rows {
		values = append(values, interfaces.DraftEnumValue{Value: text(row["code_value"]), Display: text(row["display_value"])})
	}
	return values, nil
}

// number reads a count, which JSON brings as a number and some drivers as a string.
func number(value any) int64 {
	switch v := value.(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
		f, _ := v.Float64()
		return int64(f)
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		n, _ := strconv.ParseFloat(v, 64)
		return int64(n)
	}
	return 0
}

func text(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return fmt.Sprint(value)
}
