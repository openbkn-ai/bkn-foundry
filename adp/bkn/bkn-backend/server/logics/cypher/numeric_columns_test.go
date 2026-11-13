// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestQueryReturnsExactDecimalNumbers(t *testing.T) {
	for _, literal := range []string{"20", "0.1", "-123.4500", "9007199254740993", "100.0000000000000001", "999999999999999999999999999999.99999999"} {
		t.Run(literal, func(t *testing.T) {
			vega := &recordingVega{response: &interfaces.RawQueryResponse{
				Columns: []interfaces.RawQueryColumn{{Name: "amount", Type: "decimal(38,8)"}, {Name: "code", Type: "string"}},
				Entries: []map[string]any{{"amount": literal, "code": "00123"}, {"amount": nil}},
			}}
			result, err := testService(t, vega, &stubPermission{}).Query(context.Background(), interfaces.CypherQuery{
				KNID: "kn_1", Branch: "main", Query: "MATCH (o:Order) RETURN o.id AS amount LIMIT 5",
			})
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(wire), `"amount":`+literal) || result.Entries[0]["code"] != "00123" || result.Entries[1]["amount"] != nil {
				t.Fatalf("numeric contract lost: %s", wire)
			}
			if result.Entries[0]["amount"] != json.Number(literal) {
				t.Fatalf("decimal narrowed: %#v", result.Entries[0]["amount"])
			}
		})
	}
}

func TestDecimalColumnsRejectMalformedNumbers(t *testing.T) {
	for _, literal := range []string{"NaN", "Infinity", "1,000", "1 2", ""} {
		if err := decimalColumnsAsNumbers([]interfaces.RawQueryColumn{{Name: "n", Type: "numeric"}}, []map[string]any{{"n": literal}}); err == nil {
			t.Fatalf("accepted invalid numeric value %q", literal)
		}
	}
}
