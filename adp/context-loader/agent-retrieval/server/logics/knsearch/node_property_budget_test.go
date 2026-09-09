// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knsearch

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func filterOne(t *testing.T, node *interfaces.KnSearchNode, config *interfaces.KnSearchPropertyFilterConfig) map[string]any {
	t.Helper()
	return (&localSearchImpl{}).filterNodeProperties([]*interfaces.KnSearchNode{node}, config)[0].Properties
}

func TestFilterNodePropertiesDropsEmptyValuesBeforeCapping(t *testing.T) {
	// A wide table: the alphabetically-first columns are all empty placeholders.
	props := map[string]any{}
	for _, name := range []string{"attr_text_001", "attr_text_002", "attr_text_003"} {
		props[name] = nil
	}
	props["attr_text_004"] = "   "
	props["attr_text_005"] = []any{}
	props["customer_name"] = "华东供应商"
	props["flag"] = 0
	props["active"] = false
	got := filterOne(t, &interfaces.KnSearchNode{Properties: props}, &interfaces.KnSearchPropertyFilterConfig{
		MaxPropertiesPerInstance: 3, MaxPropertyValueLength: 500,
	})
	if len(got) != 3 {
		t.Fatalf("expected the 3 populated properties, got %v", got)
	}
	for _, name := range []string{"customer_name", "flag", "active"} {
		if _, ok := got[name]; !ok {
			t.Fatalf("%s must survive (zero and false are answers): %v", name, got)
		}
	}
}

func TestFilterNodePropertiesProjection(t *testing.T) {
	node := &interfaces.KnSearchNode{Properties: map[string]any{
		"_instance_id": "order:1", "order_no": "SO-1", "amount": 99.5, "note": "x", "empty": nil,
	}}
	got := filterOne(t, node, &interfaces.KnSearchPropertyFilterConfig{
		MaxPropertiesPerInstance: 1, MaxPropertyValueLength: 500, Properties: []string{"order_no", "amount"},
	})
	if len(got) != 3 || got["order_no"] != "SO-1" || got["amount"] != 99.5 || got["_instance_id"] != "order:1" {
		t.Fatalf("projection must keep exactly the named properties plus _instance_id, got %v", got)
	}
}

func TestFilterNodePropertiesDropsDuplicatesOfNodeFields(t *testing.T) {
	node := &interfaces.KnSearchNode{
		InstanceName:     "SO-1",
		UniqueIdentities: map[string]any{"id": 1},
		Properties: map[string]any{
			"_display": "SO-1", "_instance_identity": map[string]any{"id": 1}, "_instance_id": "order:1", "order_no": "SO-1",
		},
	}
	got := filterOne(t, node, DefaultPropertyFilterConfig())
	if _, ok := got["_display"]; ok {
		t.Fatalf("_display duplicates instance_name: %v", got)
	}
	if _, ok := got["_instance_identity"]; ok {
		t.Fatalf("_instance_identity duplicates unique_identities: %v", got)
	}
	if got["_instance_id"] != "order:1" {
		t.Fatalf("_instance_id is not a duplicate and must stay: %v", got)
	}
	bare := filterOne(t, &interfaces.KnSearchNode{Properties: map[string]any{"_display": "SO-1"}}, DefaultPropertyFilterConfig())
	if bare["_display"] != "SO-1" {
		t.Fatalf("_display must stay when the node has no instance_name to duplicate: %v", bare)
	}
}

func TestFilterNodePropertiesCutsLongValues(t *testing.T) {
	node := &interfaces.KnSearchNode{Properties: map[string]any{"body": strings.Repeat("字", 400)}}
	got := filterOne(t, node, &interfaces.KnSearchPropertyFilterConfig{MaxPropertiesPerInstance: 20, MaxPropertyValueLength: 300})
	body := got["body"].(string)
	if !strings.HasSuffix(body, "...") || len([]rune(body)) != 303 {
		t.Fatalf("expected 300 runes plus ellipsis, got %d runes", len([]rune(body)))
	}
}

func TestNormalizeSearchInstanceReqCarriesPropertyBounds(t *testing.T) {
	limit := 7
	chars := 1200
	req := &interfaces.SearchInstanceReq{
		Query: "q", KnID: "kn", Properties: []string{" a ", "", "b"}, MaxPropertyChars: &chars, MaxPropertiesPerInstance: &limit,
	}
	got, err := NormalizeSearchInstanceReq(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pf := got.RetrievalConfig.(*interfaces.KnSearchRetrievalConfig).PropertyFilter
	if pf.MaxPropertyValueLength != 1200 || pf.MaxPropertiesPerInstance != 7 || strings.Join(pf.Properties, ",") != "a,b" {
		t.Fatalf("bounds not carried: %+v", pf)
	}

	defaulted, err := NormalizeSearchInstanceReq(&interfaces.SearchInstanceReq{Query: "q", KnID: "kn"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pf := defaulted.RetrievalConfig.(*interfaces.KnSearchRetrievalConfig).PropertyFilter; pf.MaxPropertyValueLength != interfaces.DefaultMaxPropertyChars || pf.MaxPropertiesPerInstance != interfaces.DefaultMaxPropertiesPerInstance || len(pf.Properties) != 0 {
		t.Fatalf("defaults not applied: %+v", pf)
	}

	tooMany := interfaces.MaxPropertyCharsCeiling + 1
	if _, err := NormalizeSearchInstanceReq(&interfaces.SearchInstanceReq{Query: "q", KnID: "kn", MaxPropertyChars: &tooMany}); err == nil {
		t.Fatalf("max_property_chars above the ceiling must be rejected")
	}
}

func TestObjectTypesOfNodesSlimsLogicProperties(t *testing.T) {
	objectTypes := []any{map[string]any{
		"concept_id": "order",
		"logic_properties": []any{map[string]any{
			"name": "gmv", "type": "metric", "data_source": map[string]any{"id": "m1"}, "parameters": []any{"p"},
		}},
	}}
	nodes := []any{map[string]any{"object_type_id": "order"}}
	kept := objectTypesOfNodes(objectTypes, nodes)
	lp := kept[0].(map[string]any)["logic_properties"].([]any)[0].(map[string]any)
	if _, has := lp["data_source"]; has {
		t.Fatalf("data_source must be dropped: %v", lp)
	}
	if _, has := lp["parameters"]; has {
		t.Fatalf("parameters must be dropped: %v", lp)
	}
	if lp["name"] != "gmv" || lp["type"] != "metric" {
		t.Fatalf("name/type must stay: %v", lp)
	}
}
