// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
	"ontology-query/logics"
)

func TestResourceQueryPreservesScoreMetadata(t *testing.T) {
	for _, tc := range []struct {
		name         string
		search       bool
		exclude      bool
		withScore    bool
		operation    string
		nested       bool
		explicitSort bool
	}{
		{name: "search returns score", search: true, withScore: true},
		{name: "knn returns score", search: true, withScore: true, operation: cond.OperationKNN},
		{name: "nested match returns score", search: true, withScore: true, nested: true},
		{name: "nested knn returns score", search: true, withScore: true, operation: cond.OperationKNN, nested: true},
		{name: "explicit primary key sort retains score", search: true, withScore: true, explicitSort: true},
		{name: "explicitly excluded", search: true, exclude: true, withScore: true},
		{name: "ordinary read omits score", withScore: true},
		{name: "source does not fabricate score", search: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectType := accessPlanObjectType()
			objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"}
			query := &interfaces.ObjectQueryBaseOnObjectType{Properties: []string{"id"}, PageQuery: interfaces.PageQuery{Limit: 10}}
			if tc.search {
				query.ActualCondition = &cond.CondCfg{Name: "id", Operation: cond.OperationMatch,
					ValueOptCfg: cond.ValueOptCfg{Value: "customer", ValueFrom: "const"}}
				if tc.operation != "" {
					query.ActualCondition.Operation = tc.operation
				}
				if tc.nested {
					query.ActualCondition = &cond.CondCfg{Operation: cond.OperationAnd, SubConds: []*cond.CondCfg{query.ActualCondition}}
				}
			}
			if tc.exclude {
				query.ExcludeSystemProperties = []string{interfaces.SORT_FIELD_SCORE}
			}
			plan, err := buildPropertyAccessPlan(context.Background(), fullPropertyAccessStub{}, objectType, query, true)
			if err != nil {
				t.Fatal(err)
			}
			query.Sort = logics.BuildViewSort(objectType)
			if tc.explicitSort {
				query.Sort = []*interfaces.SortParams{{Field: "id", Direction: "asc"}}
			}
			row := map[string]any{"customer_id": "customer-1", "secret": "hidden"}
			if tc.withScore {
				row[interfaces.SORT_FIELD_SCORE] = 0.875
			}
			vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{Entries: []map[string]any{row}}}
			var result interfaces.Objects
			if err := (&objectTypeService{vba: vega}).getObjectsFromResource(context.Background(), query, objectType, &result, plan.fieldPropertyMap(), plan); err != nil {
				t.Fatal(err)
			}
			if len(result.Datas) != 1 {
				t.Fatalf("rows = %#v", result.Datas)
			}
			score, exists := result.Datas[0][interfaces.SORT_FIELD_SCORE]
			wantScore := tc.search && !tc.exclude && tc.withScore
			if exists != wantScore || (exists && score != 0.875) {
				t.Fatalf("score = %#v, exists = %v", score, exists)
			}
			if _, leaked := result.Datas[0]["secret"]; leaked {
				t.Fatal("unmapped source field leaked")
			}
			requestedScore := false
			for _, field := range vega.lastParams.OutputFields {
				requestedScore = requestedScore || field == interfaces.SORT_FIELD_SCORE
			}
			if requestedScore != (tc.search && !tc.exclude) {
				t.Fatalf("output fields = %#v", vega.lastParams.OutputFields)
			}
			wantSortField := interfaces.SORT_FIELD_SCORE
			if tc.explicitSort {
				wantSortField = "customer_id"
			}
			if vega.lastParams.Sort[0].Field != wantSortField {
				t.Fatalf("sort = %#v", vega.lastParams.Sort)
			}
		})
	}
}

func TestScoreIsOnlyReturnedForExplicitFullPropertySearch(t *testing.T) {
	objectType := accessPlanObjectType()
	resolver := propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessMasked,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}

	plainQuery := &interfaces.ObjectQueryBaseOnObjectType{Properties: []string{"id"}}
	plainPlan, err := buildPropertyAccessPlan(context.Background(), resolver, objectType, plainQuery, true)
	if err != nil {
		t.Fatal(err)
	}
	plainResult := plainPlan.projectRow(map[string]any{"id": "1", interfaces.SORT_FIELD_SCORE: 0.8}, &objectType, plainQuery)
	if _, leaked := plainResult[interfaces.SORT_FIELD_SCORE]; leaked {
		t.Fatalf("non-search score leaked: %#v", plainResult)
	}

	searchQuery := &interfaces.ObjectQueryBaseOnObjectType{
		Properties:      []string{"id"},
		ActualCondition: &cond.CondCfg{Name: "id", Operation: cond.OperationMatch},
	}
	searchPlan, err := buildPropertyAccessPlan(context.Background(), resolver, objectType, searchQuery, true)
	if err != nil {
		t.Fatal(err)
	}
	searchResult := searchPlan.projectRow(map[string]any{"id": "1", interfaces.SORT_FIELD_SCORE: 0.8}, &objectType, searchQuery)
	if searchResult[interfaces.SORT_FIELD_SCORE] != 0.8 {
		t.Fatalf("full-property search score missing: %#v", searchResult)
	}

	maskedSearch := &interfaces.ObjectQueryBaseOnObjectType{
		Properties:      []string{"id"},
		ActualCondition: &cond.CondCfg{Name: "mobile", Operation: cond.OperationMatch},
	}
	_, err = buildPropertyAccessPlan(context.Background(), resolver, objectType, maskedSearch, true)
	httpError, ok := err.(*rest.HTTPError)
	if !ok || httpError.HTTPCode != http.StatusBadRequest {
		t.Fatalf("masked search property error = %#v", err)
	}
}

func TestLogicPropertyRequiresEveryPropertyInputToBeFull(t *testing.T) {
	objectType := accessPlanObjectType()
	objectType.LogicProperties = []*interfaces.LogicProperty{{
		Name: "risk_label", Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
		Parameters: []interfaces.Parameter{{
			Name: "mobile", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "mobile",
		}},
	}}
	query := &interfaces.ObjectQueryBaseOnObjectType{
		ObjectQueryInfo: &interfaces.ObjectQueryInfo{Properties: []string{"risk_label"}},
		CommonQueryParameters: interfaces.CommonQueryParameters{
			ExcludeSystemProperties: []string{interfaces.SYSTEM_PROPERTY_DISPLAY},
		},
	}
	maskedPlan, err := buildPropertyAccessPlan(context.Background(), propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessMasked,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}, objectType, query, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, allowed := maskedPlan.returnLogic["risk_label"]; allowed {
		t.Fatal("logic property with a masked input was made returnable")
	}
	if _, fetched := maskedPlan.dependencyFields["mobile"]; fetched {
		t.Fatal("masked logic dependency was fetched")
	}

	fullPlan, err := buildPropertyAccessPlan(context.Background(), propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessFull,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}, objectType, query, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, allowed := fullPlan.returnLogic["risk_label"]; !allowed {
		t.Fatal("logic property with full inputs was not made returnable")
	}
	if _, fetched := fullPlan.dependencyFields["mobile"]; !fetched {
		t.Fatal("full logic dependency was not fetched internally")
	}
	if _, returned := fullPlan.returnData["mobile"]; returned {
		t.Fatal("internal logic dependency was automatically added to return fields")
	}
}

func TestRequiredFullDependencyIsFetchedButNotAutomaticallyReturned(t *testing.T) {
	objectType := accessPlanObjectType()
	query := &interfaces.ObjectQueryBaseOnObjectType{
		Properties: []string{"id"},
		CommonQueryParameters: interfaces.CommonQueryParameters{
			RequiredFullProperties: []string{"mobile"},
		},
	}
	_, err := buildPropertyAccessPlan(context.Background(), propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessMasked,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}, objectType, query, true)
	httpError, ok := err.(*rest.HTTPError)
	if !ok || httpError.HTTPCode != http.StatusBadRequest {
		t.Fatalf("masked internal dependency error = %#v", err)
	}

	plan, err := buildPropertyAccessPlan(context.Background(), propertyAccessStub{levels: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "mobile": interfaces.PropertyAccessFull,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}}, objectType, query, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, fetched := plan.fetchFields["mobile"]; !fetched {
		t.Fatal("full internal dependency was not fetched")
	}
	if _, returned := plan.returnData["mobile"]; returned {
		t.Fatal("internal dependency was automatically returned")
	}
}
