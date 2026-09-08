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
)

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
