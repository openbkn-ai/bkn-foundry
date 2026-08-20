// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func intPtr(v int) *int { return &v }

// The entire reason this tool exists: Set only_schema to false to get to semantic instance recall.
func TestNormalizeSearchInstanceReq_TurnsInstanceRecallOn(t *testing.T) {
	req := &interfaces.SearchInstanceReq{
		KnID:                "kn1",
		Query:               "巴西队的球场",
		ConceptGroups:       []string{"g1", " ", "g1", "g2"},
		MaxObjectTypes:      intPtr(3),
		MaxInstancesPerType: intPtr(7),
	}

	knReq, err := NormalizeSearchInstanceReq(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if knReq.OnlySchema == nil || *knReq.OnlySchema {
		t.Fatal("only_schema must be false, otherwise instance recall never runs")
	}
	cfg, ok := knReq.RetrievalConfig.(*interfaces.KnSearchRetrievalConfig)
	if !ok {
		t.Fatalf("expected *KnSearchRetrievalConfig, got %T", knReq.RetrievalConfig)
	}
	if cfg.ConceptRetrieval.TopK != 3 {
		t.Errorf("max_object_types must drive concept top_k, got %d", cfg.ConceptRetrieval.TopK)
	}
	if cfg.SemanticInstanceRetrieval.PerTypeInstanceLimit != 7 {
		t.Errorf("max_instances_per_type must drive per_type_instance_limit, got %d",
			cfg.SemanticInstanceRetrieval.PerTypeInstanceLimit)
	}
	if cfg.ConceptRetrieval.SchemaBrief == nil || !*cfg.ConceptRetrieval.SchemaBrief {
		t.Error("schema_brief must stay on: the schema is only an intermediate product here")
	}
	if got := cfg.ConceptRetrieval.ConceptGroups; len(got) != 2 || got[0] != "g1" || got[1] != "g2" {
		t.Errorf("concept groups must be trimmed and deduped, got %v", got)
	}
}

// The default value comes from the struct tag, and the caller must be able to run without passing it.
func TestNormalizeSearchInstanceReq_AppliesDefaults(t *testing.T) {
	knReq, err := NormalizeSearchInstanceReq(&interfaces.SearchInstanceReq{KnID: "kn1", Query: "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := knReq.RetrievalConfig.(*interfaces.KnSearchRetrievalConfig)
	if cfg.ConceptRetrieval.TopK != 10 {
		t.Errorf("expected default max_object_types=10, got %d", cfg.ConceptRetrieval.TopK)
	}
	if cfg.SemanticInstanceRetrieval.PerTypeInstanceLimit != 5 {
		t.Errorf("expected default max_instances_per_type=5, got %d", cfg.SemanticInstanceRetrieval.PerTypeInstanceLimit)
	}
}

// include_object_types is enabled by default: Insufficient tools will force the caller to go back and check the Schema again.
// And that run will recall the concept of the same query and run it again.
func TestSearchInstanceReq_IncludeObjectTypesDefaultsOn(t *testing.T) {
	req := &interfaces.SearchInstanceReq{KnID: "kn1", Query: "q"}
	if _, err := NormalizeSearchInstanceReq(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.IncludeObjectTypes == nil || !*req.IncludeObjectTypes {
		t.Error("include_object_types must default to true")
	}
}

// kn_id also counts when moving, and an error will be reported if both are missing.
func TestNormalizeSearchInstanceReq_KnIDFromHeader(t *testing.T) {
	knReq, err := NormalizeSearchInstanceReq(&interfaces.SearchInstanceReq{XKnID: " kn-header ", Query: "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if knReq.KnID != "kn-header" {
		t.Errorf("expected kn id from header, got %q", knReq.KnID)
	}
}

func TestNormalizeSearchInstanceReq_Rejects(t *testing.T) {
	cases := []struct {
		name string
		req  *interfaces.SearchInstanceReq
		want string
	}{
		{"no kn id", &interfaces.SearchInstanceReq{Query: "q"}, "kn_id is required"},
		{"blank query", &interfaces.SearchInstanceReq{KnID: "kn1", Query: "   "}, "query is required"},
		{"zero object types", &interfaces.SearchInstanceReq{KnID: "kn1", Query: "q", MaxObjectTypes: intPtr(0)}, "max_object_types"},
		{"negative instances", &interfaces.SearchInstanceReq{KnID: "kn1", Query: "q", MaxInstancesPerType: intPtr(-1)}, "max_instances_per_type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeSearchInstanceReq(tc.req)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

// Only the object types whose instances were returned are brought back; relation types and action classes are discarded.
func TestFilterSearchInstanceResp_KeepsOnlyHitObjectTypes(t *testing.T) {
	msg := "未检索到符合条件的实例数据"
	out := FilterSearchInstanceResp(&interfaces.KnSearchResp{
		ObjectTypes: []any{
			map[string]any{"concept_id": "ot1"},
			map[string]any{"concept_id": "ot2"}, // The concept recall was scanned, but no examples were produced.
		},
		RelationTypes: []any{map[string]any{"concept_id": "rt1"}},
		ActionTypes:   []any{map[string]any{"concept_id": "at1"}},
		Nodes:         []any{map[string]any{"object_type_id": "ot1", "score": 1.0}},
		Message:       &msg,
	}, true)

	if len(out.Nodes) != 1 {
		t.Fatalf("expected the instance to survive, got %d", len(out.Nodes))
	}
	if len(out.ObjectTypes) != 1 {
		t.Fatalf("expected only the object type that produced rows, got %d", len(out.ObjectTypes))
	}
	if id := out.ObjectTypes[0].(map[string]any)["concept_id"]; id != "ot1" {
		t.Errorf("expected ot1, got %v", id)
	}
	if out.Message != msg {
		t.Errorf("retrieval's message must reach the caller, got %q", out.Message)
	}
}

// The relevance gate can have something to say about rows it did return: "configured but unable to
// run, so these are unfiltered". Dropping that left the caller unable to tell a filtered result from
// an unfiltered one.
func TestFilterSearchInstanceResp_KeepsMessageAlongsideRows(t *testing.T) {
	msg := "相关性阈值 min_reranker_score=0.3000 未生效：精排模型不可用"
	out := FilterSearchInstanceResp(&interfaces.KnSearchResp{
		ObjectTypes: []any{map[string]any{"concept_id": "ot1"}},
		Nodes:       []any{map[string]any{"object_type_id": "ot1", "score": 1.0}},
		Message:     &msg,
	}, true)

	if len(out.Nodes) != 1 {
		t.Fatalf("expected the row to survive, got %d", len(out.Nodes))
	}
	if out.Message != msg {
		t.Fatalf("the warning was dropped because the result was non-empty: %q", out.Message)
	}
}

// Turning off the switch will not return any schema - saving volume is the caller's choice, not the default.
func TestFilterSearchInstanceResp_ObjectTypesCanBeTurnedOff(t *testing.T) {
	out := FilterSearchInstanceResp(&interfaces.KnSearchResp{
		ObjectTypes: []any{map[string]any{"concept_id": "ot1"}},
		Nodes:       []any{map[string]any{"object_type_id": "ot1"}},
	}, false)

	if len(out.ObjectTypes) != 0 {
		t.Errorf("expected no object types when the switch is off, got %d", len(out.ObjectTypes))
	}
	if len(out.Nodes) != 1 {
		t.Errorf("instances must be unaffected by the switch, got %d", len(out.Nodes))
	}
}

// The empty result should have a reason and it is not an error - it is more usable for the Agent to get "Not Found" than to get 500.
func TestFilterSearchInstanceResp_EmptyKeepsMessage(t *testing.T) {
	msg := "未检索到符合条件的实例数据"
	out := FilterSearchInstanceResp(&interfaces.KnSearchResp{Nodes: []any{}, Message: &msg}, true)
	if len(out.Nodes) != 0 {
		t.Fatalf("expected no nodes, got %d", len(out.Nodes))
	}
	if out.Message != msg {
		t.Errorf("expected the message to be carried through, got %q", out.Message)
	}

	// A nil response cannot panic, and nodes must be an empty array rather than null - saving the caller from parsing in two cases.
	empty := FilterSearchInstanceResp(nil, true)
	if empty.Nodes == nil {
		t.Error("nodes must serialize as [] rather than null")
	}
}

// Unset switches must remain "unfilled" and cannot be made explicitly false.
//
// RetrievalConfig on the request side will step into this trap: its switch is the value type bool, and the conversion layer is unconditional.
// boolPtr contains one layer, so the unfilled enable_global_final_score_ratio_filter is explicitly false.
// Overriding the default of true, cross-object type correlation filtering is never performed in this way.
func TestNormalizeSearchInstanceReq_DoesNotClobberDefaultSwitches(t *testing.T) {
	knReq, err := NormalizeSearchInstanceReq(&interfaces.SearchInstanceReq{KnID: "kn1", Query: "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	local := KnSearchReqToLocal(&interfaces.KnSearchReq{
		Query:           knReq.Query,
		KnID:            knReq.KnID,
		OnlySchema:      knReq.OnlySchema,
		RetrievalConfig: knReq.RetrievalConfig,
	})
	merged := MergeRetrievalConfig(local.RetrievalConfig)

	if !boolValue(merged.SemanticInstanceRetrieval.EnableGlobalFinalScoreRatioFilter) {
		t.Error("global score ratio filter must stay enabled; an unset switch became an explicit false")
	}
	if !boolValue(merged.ConceptRetrieval.EnableCoarseRecall) {
		t.Error("coarse recall must stay enabled; an unset switch became an explicit false")
	}
	if merged.SemanticInstanceRetrieval.GlobalFinalScoreRatio != 0.25 {
		t.Errorf("expected the default ratio 0.25, got %v", merged.SemanticInstanceRetrieval.GlobalFinalScoreRatio)
	}
	if merged.SemanticInstanceRetrieval.PerTypeInstanceLimit != 5 {
		t.Errorf("expected per-type limit 5, got %d", merged.SemanticInstanceRetrieval.PerTypeInstanceLimit)
	}
}

// The MCP plane must only send the operators brought by the index. The comparison operator can be deduced according to the attribute type, and it is pure noise to issue one by one.
// And it's very expensive - the measured total operator of 154 attributes is 15KB, leaving only 364 bytes for the index operator.
func TestNormalizeSearchInstanceReq_PassesIndexOpsOnly(t *testing.T) {
	knReq, err := NormalizeSearchInstanceReq(&interfaces.SearchInstanceReq{
		KnID: "kn1", Query: "q", IndexOpsOnly: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !knReq.IndexOpsOnly {
		t.Error("index_ops_only must reach KnSearchReq, otherwise MCP callers get every comparison operator")
	}

	// The REST caller does not set it and takes the full operator.
	restReq, err := NormalizeSearchInstanceReq(&interfaces.SearchInstanceReq{KnID: "kn1", Query: "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if restReq.IndexOpsOnly {
		t.Error("index_ops_only must stay off unless the MCP layer sets it")
	}
}

// The switch for fine-tuning is handed over to the caller: only the person who initiated the query knows whether the query should be accurate or fast this time.
func TestNormalizeSearchInstanceReq_RerankFlag(t *testing.T) {
	modeOf := func(req *interfaces.SearchInstanceReq) string {
		knReq, err := NormalizeSearchInstanceReq(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return knReq.RetrievalConfig.(*interfaces.KnSearchRetrievalConfig).
			SemanticInstanceRetrieval.InstanceRerankMode
	}

	// Default off: Fine sorting requires one more model call and should not occur without the caller requesting it.
	if got := modeOf(&interfaces.SearchInstanceReq{KnID: "kn1", Query: "q"}); got != InstanceRerankModeOff {
		t.Errorf("rerank must default to off, got %q", got)
	}

	on := true
	if got := modeOf(&interfaces.SearchInstanceReq{KnID: "kn1", Query: "q", Rerank: &on}); got != InstanceRerankModeOn {
		t.Errorf("rerank=true must turn the stage on, got %q", got)
	}

	off := false
	if got := modeOf(&interfaces.SearchInstanceReq{KnID: "kn1", Query: "q", Rerank: &off}); got != InstanceRerankModeOff {
		t.Errorf("rerank=false must keep it off, got %q", got)
	}
}

func TestNormalizeSearchInstanceReq_PassesObjectTypeScope(t *testing.T) {
	req := &interfaces.SearchInstanceReq{
		KnID:               "kn1",
		Query:              "q",
		ObjectTypes:        []string{" Material ", "material", ""},
		ExcludeObjectTypes: []string{"audit_log"},
	}

	knReq, err := NormalizeSearchInstanceReq(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	local := KnSearchReqToLocal(&interfaces.KnSearchReq{
		Query:           knReq.Query,
		KnID:            knReq.KnID,
		OnlySchema:      knReq.OnlySchema,
		RetrievalConfig: knReq.RetrievalConfig,
	})
	concept := MergeRetrievalConfig(local.RetrievalConfig).ConceptRetrieval
	if len(concept.ObjectTypes) != 1 || concept.ObjectTypes[0] != "Material" {
		t.Fatalf("allow list not trimmed and de-duplicated: %v", concept.ObjectTypes)
	}
	if len(concept.ExcludeObjectTypes) != 1 || concept.ExcludeObjectTypes[0] != "audit_log" {
		t.Fatalf("deny list lost on the way to concept retrieval: %v", concept.ExcludeObjectTypes)
	}
}
