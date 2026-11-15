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

// exclude_deprecated filters the whole query and each knn, so deprecated concepts take none of
// its k neighbours; without it the query is unchanged.
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
	if knn.Operation != cond.OperationKNN || len(knn.SubConds) != 1 || !isNotDeprecated(knn.SubConds[0]) {
		t.Fatalf("knn = %+v, want the not-deprecated filter as its filter", knn)
	}

	query = recallQuery(false)
	if err := validateConceptsQuery(context.Background(), query); err != nil {
		t.Fatal(err)
	}
	if n := len(query.ActualCondition.SubConds); n != 4 || len(query.ActualCondition.SubConds[0].SubConds[0].SubConds) != 0 {
		t.Fatalf("without exclude_deprecated the query gained filters: %+v", query.ActualCondition)
	}
}
