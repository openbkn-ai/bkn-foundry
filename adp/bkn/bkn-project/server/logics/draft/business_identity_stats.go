// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"math"
	"strconv"
)

func exactCount(value any) (int64, error) {
	var count int64
	var err error
	switch v := value.(type) {
	case int:
		count = int64(v)
	case int64:
		count = v
	case json.Number:
		count, err = v.Int64()
	case string:
		count, err = strconv.ParseInt(v, 10, 64)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 9007199254740991 || math.Trunc(v) != v {
			return 0, fmt.Errorf("invalid count")
		}
		count = int64(v)
	default:
		return 0, fmt.Errorf("missing count")
	}
	if err != nil || count < 0 {
		return 0, fmt.Errorf("invalid count")
	}
	return count, nil
}

// businessKeyStats uses one full-table statement, not a sample or a cardinality
// estimate. Blank text identifiers are rejected in addition to SQL NULLs.
func (c *vegaCounter) businessKeyStats(ctx context.Context, table *interfaces.VegaTable, field, typ string) (rows, distinct, nulls, blanks int64, err error) {
	f := quote(field)
	blankExpr := "0"
	if typ == "string" || typ == "text" {
		blankExpr = "SUM(CASE WHEN " + f + " IS NOT NULL AND TRIM(" + f + ") = '' THEN 1 ELSE 0 END)"
	}
	result, err := c.one(ctx, fmt.Sprintf("SELECT COUNT(*) AS total, COUNT(DISTINCT %s) AS distinct_values, SUM(CASE WHEN %s IS NULL THEN 1 ELSE 0 END) AS nulls, %s AS blanks FROM %s", f, f, blankExpr, ref(table)))
	if err != nil {
		return 0, 0, 0, 0, err
	}
	rows, err = exactCount(result["total"])
	if err != nil {
		return
	}
	distinct, err = exactCount(result["distinct_values"])
	if err != nil {
		return
	}
	// SQL SUM on a known-empty table is NULL, while COUNT is exactly zero.
	if rows == 0 && result["nulls"] == nil {
		nulls = 0
	} else {
		nulls, err = exactCount(result["nulls"])
		if err != nil {
			return
		}
	}
	if rows == 0 && result["blanks"] == nil {
		blanks = 0
	} else {
		blanks, err = exactCount(result["blanks"])
		if err != nil {
			return
		}
	}
	if distinct > rows || nulls > rows || blanks > rows {
		err = fmt.Errorf("inconsistent counts")
	}
	return
}
