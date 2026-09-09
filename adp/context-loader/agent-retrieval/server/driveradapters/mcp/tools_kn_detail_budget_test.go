// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

func groupedNetwork() *interfaces.KnowledgeNetworkDetail {
	prop := func(name string) *interfaces.DataProperty {
		return &interfaces.DataProperty{Name: name, Type: "string", MappedField: map[string]any{"column": name}}
	}
	// A bound data source makes the property filter consult the schema access stub,
	// whose grants keep the properties visible; an unbound type would lose them all.
	source := &interfaces.ResourceInfo{ID: "res", Type: "resource"}
	return &interfaces.KnowledgeNetworkDetail{
		ID: "kn-001", Name: "grouped",
		ConceptGroups: []*interfaces.ConceptGroup{
			{ID: "g_sales", Name: "销售", ObjectTypeIDs: []string{"order", "customer"}},
			{ID: "g_supply", Name: "供应", ObjectTypeIDs: []string{"supplier"}},
		},
		ObjectTypes: []*interfaces.ObjectType{
			{ID: "order", Name: "订单", DataSource: source, DataProperties: []*interfaces.DataProperty{prop("id"), prop("amount")}},
			{ID: "customer", Name: "客户", DataSource: source, DataProperties: []*interfaces.DataProperty{prop("id")}},
			{ID: "supplier", Name: "供应商", DataSource: source, DataProperties: []*interfaces.DataProperty{prop("id")}},
			{ID: "orphan", Name: "孤儿", DataSource: source, DataProperties: []*interfaces.DataProperty{prop("id")}},
		},
		RelationTypes: []*interfaces.RelationType{
			{ID: "order_customer", SourceObjectTypeID: "order", TargetObjectTypeID: "customer"},
			{ID: "order_supplier", SourceObjectTypeID: "order", TargetObjectTypeID: "supplier"},
			{ID: "supplier_orphan", SourceObjectTypeID: "supplier", TargetObjectTypeID: "orphan"},
		},
		ActionTypes: []*interfaces.ActionType{
			{ID: "ship", ObjectTypeID: "order"},
			{ID: "audit", ObjectTypeID: "supplier"},
		},
	}
}

func fullPropertyAccess() *mcpObjectSchemaAccessStub {
	return &mcpObjectSchemaAccessStub{permissions: map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "amount": interfaces.PropertyAccessFull,
	}}
}

func knDetailWith(t *testing.T, args map[string]any) map[string]any {
	t.Helper()
	bkn := &stubMetricBknBackend{detail: groupedNetwork()}
	handler := handleGetKnDetail(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil), fullPropertyAccess(), stubKnAuthz{})
	full := map[string]any{"kn_id": "kn-001", "response_format": "json"}
	for k, v := range args {
		full[k] = v
	}
	result, err := handler(context.Background(), mcpReq(full))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	return resultToMap(t, result)
}

func ids(t *testing.T, items any) []string {
	t.Helper()
	list, _ := items.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.(map[string]any)["id"].(string))
	}
	return out
}

func TestKnDetailOutlineDropsEveryProperty(t *testing.T) {
	got := knDetailWith(t, map[string]any{"detail_level": "outline"})
	for _, ot := range got["object_types"].([]any) {
		entry := ot.(map[string]any)
		if _, has := entry["data_properties"]; has {
			t.Fatalf("outline must not carry properties: %v", entry)
		}
		if entry["id"] == "" || entry["name"] == "" {
			t.Fatalf("outline must keep the skeleton: %v", entry)
		}
	}
	if len(got["concept_groups"].([]any)) != 2 || len(got["object_types"].([]any)) != 4 {
		t.Fatalf("outline must keep every group and object type: %v", got)
	}
	summary := knDetailWith(t, map[string]any{"detail_level": "summary"})
	if _, has := summary["object_types"].([]any)[0].(map[string]any)["data_properties"]; !has {
		t.Fatalf("summary must still carry the property table")
	}
}

func TestKnDetailConceptGroupsNarrowTheSchemaButNotTheIndex(t *testing.T) {
	got := knDetailWith(t, map[string]any{"concept_groups": []any{"g_sales", "no_such_group"}})

	if want, have := []string{"order", "customer"}, ids(t, got["object_types"]); strings.Join(want, ",") != strings.Join(have, ",") {
		t.Fatalf("object_types = %v, want %v", have, want)
	}
	// Edges leaving the group stay visible; edges with no endpoint inside do not.
	if want, have := []string{"order_customer", "order_supplier"}, ids(t, got["relation_types"]); strings.Join(want, ",") != strings.Join(have, ",") {
		t.Fatalf("relation_types = %v, want %v", have, want)
	}
	if want, have := []string{"ship"}, ids(t, got["action_types"]); strings.Join(want, ",") != strings.Join(have, ",") {
		t.Fatalf("action_types = %v, want %v", have, want)
	}
	if len(got["concept_groups"].([]any)) != 2 {
		t.Fatalf("the group index must stay whole: %v", got["concept_groups"])
	}
	missing, _ := got["missing_concept_groups"].([]any)
	if len(missing) != 1 || missing[0] != "no_such_group" {
		t.Fatalf("missing_concept_groups = %v", got["missing_concept_groups"])
	}
}

func TestKnDetailConceptGroupsAcceptNamesAndOmitMissingWhenAllMatch(t *testing.T) {
	got := knDetailWith(t, map[string]any{"concept_groups": []any{"供应"}})
	if want, have := []string{"supplier"}, ids(t, got["object_types"]); strings.Join(want, ",") != strings.Join(have, ",") {
		t.Fatalf("object_types = %v, want %v", have, want)
	}
	if _, has := got["missing_concept_groups"]; has {
		t.Fatalf("missing_concept_groups must be absent when every name matched: %v", got)
	}
}

func TestKnDetailWithoutConceptGroupsReturnsEverything(t *testing.T) {
	got := knDetailWith(t, nil)
	if len(ids(t, got["object_types"])) != 4 || len(ids(t, got["relation_types"])) != 3 {
		t.Fatalf("no filter must return the whole network: %v", got)
	}
	if _, has := got["missing_concept_groups"]; has {
		t.Fatalf("missing_concept_groups must be absent without a filter")
	}
}

func TestKnDetailRejectsUnknownDetailLevel(t *testing.T) {
	bkn := &stubMetricBknBackend{detail: groupedNetwork()}
	handler := handleGetKnDetail(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil), fullPropertyAccess(), stubKnAuthz{})
	result, err := handler(context.Background(), mcpReq(map[string]any{"kn_id": "kn-001", "detail_level": "brief"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError || !strings.Contains(toolResultErrorMessage(result), "outline, summary, full") {
		t.Fatalf("unknown level must be rejected with the accepted values, got %+v", result)
	}
}
