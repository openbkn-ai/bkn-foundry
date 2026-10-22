// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"testing"
)

func TestObjectMetricQueryRequestV1GroupBy(t *testing.T) {
	const request = `{"group_by":[{"object_type_id":"order","property_name":"region"}]}`
	var query ObjectMetricQueryRequestV1
	if err := json.Unmarshal([]byte(request), &query); err != nil {
		t.Fatal(err)
	}
	if len(query.GroupBy) != 1 || query.GroupBy[0].ObjectTypeID != "order" || query.GroupBy[0].PropertyName != "region" {
		t.Fatalf("unexpected group_by: %+v", query.GroupBy)
	}
	var trial ObjectMetricTrialRequestV1
	if err := json.Unmarshal([]byte(`{"definition":{"query_capabilities":{"analysis_dimensions":[{"object_type_id":"order","property_name":"region"}]}},"query":`+request+`}`), &trial); err != nil {
		t.Fatal(err)
	}
	if len(trial.Definition.QueryCapabilities.AnalysisDimensions) != 1 || len(trial.Query.GroupBy) != 1 {
		t.Fatalf("unexpected trial dimensions: definition=%+v query=%+v", trial.Definition.QueryCapabilities.AnalysisDimensions, trial.Query.GroupBy)
	}
}
