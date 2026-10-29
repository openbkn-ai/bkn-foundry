// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestBindStrictPreciseJSONMetricQuery(t *testing.T) {
	for _, input := range []string{
		`{"kn_id":"kn1","metric_id":"m1","query":{"instance_identities":[{"id":1}]}}`,
		`{"kn_id":"kn1","metric_id":"m1","query":{"bucket_selection":{"bucket_key":"b1","parent_query":{"instance_identities":[{"id":1}]}}}}`,
	} {
		var req interfaces.QueryMetricReq
		if err := BindStrictPreciseJSON(strings.NewReader(input), &req); err == nil || !strings.Contains(err.Error(), "unknown field \"instance_identities\"") {
			t.Fatalf("BindStrictPreciseJSON(%s) error = %v, want unknown instance_identities field", input, err)
		}
	}

	var req interfaces.QueryMetricReq
	if err := BindStrictPreciseJSON(strings.NewReader(`{"kn_id":"kn1","metric_id":"m1","query":{"having":{"operator":"eq","value":9007199254740993}}}`), &req); err != nil {
		t.Fatal(err)
	}
	if got, ok := req.Query.Having.Value.(json.Number); !ok || got.String() != "9007199254740993" {
		t.Fatalf("having value = %#v, want exact json.Number", req.Query.Having.Value)
	}
}
