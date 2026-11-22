// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
)

func TestDatasetQueryResponsePreservesTotalPresenceAndNumbers(t *testing.T) {
	for _, total := range []struct {
		name, field string
		want        *int64
	}{
		{name: "absent"}, {name: "null", field: `,"total_count":null`},
		{name: "zero", field: `,"total_count":0`, want: new(int64(0))},
		{name: "positive", field: `,"total_count":3`, want: new(int64(3))},
	} {
		t.Run(total.name, func(t *testing.T) {
			input := `{"entries":[{"id":9007199254740993,"amount":1234567890123456.78}]` + total.field + `}`
			var response DatasetQueryResponse
			if err := common.UnmarshalJSON([]byte(input), &response); err != nil {
				t.Fatal(err)
			}
			if (response.TotalCount == nil) != (total.want == nil) || total.want != nil && *response.TotalCount != *total.want {
				t.Fatalf("total=%v want=%v", response.TotalCount, total.want)
			}
			if response.Entries[0]["id"] != json.Number("9007199254740993") || response.Entries[0]["amount"] != json.Number("1234567890123456.78") {
				t.Fatalf("decoded numbers changed: %#v", response.Entries)
			}
			for _, output := range []any{
				response,
				Objects{Datas: response.Entries, TotalCount: response.TotalCount},
				ObjectTypeSampleData{Entries: response.Entries, TotalCount: response.TotalCount},
				ObjectSubGraph{TotalCount: response.TotalCount},
			} {
				wire, err := json.Marshal(output)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(wire, &fields); err != nil {
					t.Fatal(err)
				}
				value, present := fields["total_count"]
				if present != (total.want != nil) {
					t.Fatalf("total presence changed: %s", wire)
				}
				if present && string(value) != strings.TrimPrefix(total.field, `,"total_count":`) {
					t.Fatalf("total value changed: %s", wire)
				}
			}
			wire, err := json.Marshal(Objects{Datas: response.Entries, TotalCount: response.TotalCount})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(wire), "9007199254740993") || !strings.Contains(string(wire), "1234567890123456.78") {
				t.Fatalf("final precision changed: %s", wire)
			}
		})
	}
}
