// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"testing"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func recallQuery(exclude bool) *interfaces.ConceptsQuery {
	return &interfaces.ConceptsQuery{
		KNID: "kn1", Branch: interfaces.MAIN_BRANCH, ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE,
		ExcludeDeprecated: exclude,
		Condition: map[string]any{
			"operation": "or",
			"sub_conditions": []any{
				map[string]any{"field": "*", "operation": "knn", "value": "订单", "value_from": "const",
					"limit_key": "k", "limit_value": 10},
				map[string]any{"field": "*", "operation": "match", "value": "订单", "value_from": "const"},
			},
		},
	}
}

// isNotDeprecated reports whether cfg keeps a concept with no lifecycle_status (a normal one) as
// well as one whose lifecycle_status is not deprecated.
func isNotDeprecated(cfg *cond.CondCfg) bool {
	if cfg == nil || cfg.Operation != cond.OperationOr || len(cfg.SubConds) != 2 {
		return false
	}
	missing, notEq := cfg.SubConds[0], cfg.SubConds[1]
	return missing.Field == interfaces.CONCEPT_FIELD_LIFECYCLE_STATUS && missing.Operation == cond.OperationNotExist &&
		notEq.Field == interfaces.CONCEPT_FIELD_LIFECYCLE_STATUS &&
		notEq.Operation == cond.OperationNotEq && notEq.ValueFrom == cond.ValueFrom_Const &&
		notEq.Value == interfaces.LIFECYCLE_DEPRECATED
}

// The filter must convert to a dataset filter that keeps a concept whose lifecycle_status is
// missing; a bare "!=" drops it (issue #3121).
func TestExcludeDeprecatedKeepsConceptsWithoutLifecycle(t *testing.T) {
	got, err := cond.ConvertCondCfgToFilterCondition(context.Background(), notDeprecatedFilter(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	subs, _ := got["sub_conditions"].([]map[string]any)
	if got["operation"] != "or" || len(subs) != 2 || subs[0]["operation"] != "not_exist" ||
		subs[1]["operation"] != "!=" || subs[1]["value"] != interfaces.LIFECYCLE_DEPRECATED {
		t.Fatalf("filter = %v, want or(not_exist, != deprecated)", got)
	}
}

// isScope reports whether filters are the network, kind and branch filters of recallQuery, in order.
func isScope(filters []*cond.CondCfg) bool {
	want := []struct{ field, value string }{
		{"kn_id", "kn1"}, {"module_type", interfaces.MODULE_TYPE_OBJECT_TYPE}, {"branch", interfaces.MAIN_BRANCH},
	}
	if len(filters) != len(want) {
		return false
	}
	for i, w := range want {
		f := filters[i]
		if f.Field != w.field || f.Operation != cond.OperationEq || f.ValueFrom != cond.ValueFrom_Const || f.Value != w.value {
			return false
		}
	}
	return true
}

// exclude_deprecated filters the whole query and each knn, so deprecated concepts take none of
// its k neighbours; without it neither carries the filter.
func TestValidateConceptsQueryExcludeDeprecated(t *testing.T) {
	query := recallQuery(true)
	if err := validateConceptsQuery(context.Background(), query); err != nil {
		t.Fatal(err)
	}
	root := query.ActualCondition.SubConds
	if !isNotDeprecated(root[len(root)-1]) {
		t.Fatalf("top-level filters = %+v, want the not-deprecated filter last", root)
	}
	knn := root[0].SubConds[0]
	if knn.Operation != cond.OperationKNN || len(knn.SubConds) != 4 || !isNotDeprecated(knn.SubConds[3]) {
		t.Fatalf("knn = %+v, want the not-deprecated filter as its filter", knn)
	}

	query = recallQuery(false)
	if err := validateConceptsQuery(context.Background(), query); err != nil {
		t.Fatal(err)
	}
	knn = query.ActualCondition.SubConds[0].SubConds[0]
	if n := len(query.ActualCondition.SubConds); n != 4 || len(knn.SubConds) != 3 {
		t.Fatalf("without exclude_deprecated the query gained filters: %+v", query.ActualCondition)
	}
}

// Every knn, at any depth, picks its neighbours within the network, kind and branch asked for,
// after its own filters; without that, other networks' concepts can take all k and the outer
// filters leave nothing (#3404). Each knn gets its own copy, and the outer filters stay.
func TestValidateConceptsQueryScopesEveryKnn(t *testing.T) {
	for _, exclude := range []bool{false, true} {
		query := recallQuery(exclude)
		query.Condition = map[string]any{
			"operation": "or",
			"sub_conditions": []any{
				map[string]any{"field": "*", "operation": "knn", "value": "订单", "value_from": "const",
					"limit_key": "k", "limit_value": 3,
					"sub_conditions": []any{
						map[string]any{"field": "tags", "operation": "==", "value": "sales", "value_from": "const"},
					}},
				map[string]any{"operation": "and", "sub_conditions": []any{
					map[string]any{"field": "*", "operation": "knn", "value": "客户", "value_from": "const",
						"limit_key": "k", "limit_value": 1},
					map[string]any{"field": "*", "operation": "match", "value": "客户", "value_from": "const"},
				}},
			},
		}
		if err := validateConceptsQuery(context.Background(), query); err != nil {
			t.Fatal(err)
		}

		extra := 0
		if exclude {
			extra = 1
		}
		root := query.ActualCondition.SubConds
		if len(root) != 4+extra || !isScope(root[1:4]) {
			t.Fatalf("exclude=%v: top-level filters = %+v, want the scope around the query", exclude, root)
		}
		caller := root[0]
		first, second := caller.SubConds[0], caller.SubConds[1].SubConds[0]
		if len(first.SubConds) != 4+extra || first.SubConds[0].Field != "tags" || !isScope(first.SubConds[1:4]) {
			t.Fatalf("exclude=%v: first knn filters = %+v, want its own filter then the scope", exclude, first.SubConds)
		}
		if len(second.SubConds) != 3+extra || !isScope(second.SubConds[0:3]) {
			t.Fatalf("exclude=%v: nested knn filters = %+v, want the scope", exclude, second.SubConds)
		}
		if exclude && (!isNotDeprecated(first.SubConds[4]) || !isNotDeprecated(second.SubConds[3])) {
			t.Fatalf("exclude=%v: knn filters lack the not-deprecated filter", exclude)
		}
		if first.SubConds[1] == root[1] || first.SubConds[1] == second.SubConds[0] {
			t.Fatalf("exclude=%v: knn filters share a filter with another place in the tree", exclude)
		}
	}
}

// The scope reaches the dataset as the knn's own filter, which vega turns into the knn filter.
func TestScopedKnnConvertsToKnnVectorFilter(t *testing.T) {
	query := recallQuery(false)
	query.Condition = map[string]any{"field": "*", "operation": "knn", "value": "订单", "value_from": "const",
		"limit_key": "k", "limit_value": 3}
	if err := validateConceptsQuery(context.Background(), query); err != nil {
		t.Fatal(err)
	}
	got, err := cond.ConvertCondCfgToFilterCondition(context.Background(), query.ActualCondition, interfaces.CONCPET_QUERY_FIELD,
		func(context.Context, string) ([]*cond.VectorResp, error) {
			return []*cond.VectorResp{{Vector: []float32{0.1, 0.2}}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	subs, _ := got["sub_conditions"].([]map[string]any)
	if len(subs) != 4 || subs[0]["operation"] != "knn_vector" {
		t.Fatalf("filter = %v, want and(knn_vector, scope...)", got)
	}
	knnFilters, _ := subs[0]["sub_conditions"].([]map[string]any)
	if len(knnFilters) != 3 || knnFilters[0]["field"] != "kn_id" || knnFilters[0]["value"] != "kn1" ||
		knnFilters[1]["field"] != "module_type" || knnFilters[2]["field"] != "branch" {
		t.Fatalf("knn_vector filters = %v, want kn_id, module_type and branch", knnFilters)
	}
}
