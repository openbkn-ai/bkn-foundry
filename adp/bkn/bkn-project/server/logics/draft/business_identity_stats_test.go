// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"strings"
	"testing"
)

func TestBusinessIdentityUsesFullTableCountsAndRejectsIncompleteMetrics(t *testing.T) {
	for _, scenario := range []string{"valid", "blank", "null", "duplicate", "missing", "fractional", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			result := map[string]any{"total": float64(10), "distinct_values": float64(10), "nulls": float64(0), "blanks": float64(0)}
			switch scenario {
			case "blank":
				result["blanks"] = float64(1)
			case "null":
				result["nulls"] = float64(1)
			case "duplicate":
				result["distinct_values"] = float64(9)
			case "missing":
				delete(result, "distinct_values")
			case "fractional":
				result["total"] = 1.5
			case "empty":
				result = map[string]any{"total": float64(0), "distinct_values": float64(0), "nulls": nil, "blanks": nil}
			}
			backend := &recordingVega{rows: []map[string]any{result}}
			counter := &vegaCounter{va: backend, account: interfaces.AccountInfo{ID: "operator", Type: "user"}, timeoutSeconds: 30}
			rows, distinct, nulls, blanks, err := counter.businessKeyStats(context.Background(), &interfaces.VegaTable{ID: "contracts"}, "business`number", "string")
			if scenario == "missing" || scenario == "fractional" {
				if err == nil {
					t.Fatal("malformed upstream metric attested uniqueness")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(backend.sql) != 1 || strings.Contains(strings.ToUpper(backend.sql[0]), "LIMIT") || !strings.Contains(backend.sql[0], "TRIM(`business``number`)") {
				t.Fatal("business key was sampled or not blank checked")
			}
			valid := rows == distinct && nulls == 0 && blanks == 0
			if valid != (scenario == "valid" || scenario == "empty") {
				t.Fatal("wrong unique/nonempty decision")
			}
		})
	}
}

func TestExactCountsSupportIntegralSQLDecimalsWithoutRounding(t *testing.T) {
	for _, value := range []any{"3.0", "3.000", json.Number("3.0"), int64(3)} {
		count, err := exactCount(value)
		if err != nil || count != 3 {
			t.Fatal("integral SQL aggregate rejected", value, err)
		}
	}
	for _, value := range []any{"3.1", "3.", "3.0.0", "9223372036854775808.0", json.Number("3.1"), "-1.0"} {
		if _, err := exactCount(value); err == nil {
			t.Fatal("inexact or invalid count accepted", value)
		}
	}
}
