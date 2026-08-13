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

// 这个工具存在的全部理由：把 only_schema 置为 false，进到语义实例召回。
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

// 缺省值来自 struct tag，调用方不传也要能跑。
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

// kn_id 走头也算数，两处都没有才报错。
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

// 响应只留实例：Schema 三件套要字段含义有 search_schema，在这里重复一份是纯体积。
func TestFilterSearchInstanceResp_DropsSchema(t *testing.T) {
	msg := "未检索到符合条件的实例数据"
	out := FilterSearchInstanceResp(&interfaces.KnSearchResp{
		ObjectTypes:   []any{map[string]any{"concept_id": "ot1"}},
		RelationTypes: []any{map[string]any{"concept_id": "rt1"}},
		ActionTypes:   []any{map[string]any{"concept_id": "at1"}},
		Nodes:         []any{map[string]any{"object_type_id": "ot1", "score": 1.0}},
		Message:       &msg,
	})

	if len(out.Nodes) != 1 {
		t.Fatalf("expected the instance to survive, got %d", len(out.Nodes))
	}
	if out.Message != "" {
		t.Errorf("message belongs to the empty case only, got %q", out.Message)
	}
}

// 空结果要带上原因，且不是错误——Agent 拿到「没找到」比拿到 500 更可用。
func TestFilterSearchInstanceResp_EmptyKeepsMessage(t *testing.T) {
	msg := "未检索到符合条件的实例数据"
	out := FilterSearchInstanceResp(&interfaces.KnSearchResp{Nodes: []any{}, Message: &msg})
	if len(out.Nodes) != 0 {
		t.Fatalf("expected no nodes, got %d", len(out.Nodes))
	}
	if out.Message != msg {
		t.Errorf("expected the message to be carried through, got %q", out.Message)
	}

	// nil 响应也不能 panic，且 nodes 必须是空数组而不是 null——省得调用方分两种情况解析。
	empty := FilterSearchInstanceResp(nil)
	if empty.Nodes == nil {
		t.Error("nodes must serialize as [] rather than null")
	}
}

// 未设置的开关必须保持「没填」，不能变成显式 false。
//
// 走请求面的 RetrievalConfig 会踩这个坑：它的开关是值类型 bool，转换层无条件
// boolPtr 包一层，于是没填的 enable_global_final_score_ratio_filter 以显式 false
// 覆盖掉默认的 true，跨对象类的相关性过滤在这条路上永不执行。
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
