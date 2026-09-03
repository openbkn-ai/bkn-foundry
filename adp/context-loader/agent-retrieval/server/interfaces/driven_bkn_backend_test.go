// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"testing"
)

func TestKnowledgeNetworkDetailDecodesExportedMetrics(t *testing.T) {
	var detail KnowledgeNetworkDetail
	if err := json.Unmarshal([]byte(`{
		"id":"supply",
		"metrics":[{"id":"monthly-revenue","name":"月度营收","scope_ref":"purchase_order"}]
	}`), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Metrics) != 1 || detail.Metrics[0].ID != "monthly-revenue" || detail.Metrics[0].Name != "月度营收" || detail.Metrics[0].ScopeRef != "purchase_order" {
		t.Fatalf("decoded metrics = %+v", detail.Metrics)
	}
}
