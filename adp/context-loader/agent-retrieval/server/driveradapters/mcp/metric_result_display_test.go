// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestMetricResultPreservesNumericValueAndExactDisplay(t *testing.T) {
	resp := &interfaces.QueryMetricResp{Entries: []interfaces.ObjectMetricDataEntry{
		{Value: json.Number("9007199254740993.01"), Status: "ok"},
		{Value: nil, Status: "no_data"},
	}}
	data, err := json.Marshal(metricResultWithExactValues(resp))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"value":9007199254740993.01,"status":"ok","display_value":"9007199254740993.01"`) {
		t.Fatalf("exact decimal lost: %s", data)
	}
	if strings.Contains(string(data), `"display_value":""`) {
		t.Fatalf("empty display value must be omitted: %s", data)
	}
}
