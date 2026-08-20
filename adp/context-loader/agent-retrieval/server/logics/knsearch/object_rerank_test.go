package knsearch

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Regression for the reported "企业对象类字段" case.
//
// The coarse pass is BM25 over ten equally weighted fields, and the BKN concept index tokenizes
// Chinese per character, so a query carrying schema meta-words ("企业 对象类 字段") scores a long
// dispatch-record object type - whose comment happens to repeat 企业 three times - above the object
// type actually named 企业. With max_concepts=1 that one slot went to the decoy and the answer the
// caller wanted never appeared. The reranker is the only stage that can tell 字段 is a meta-word.

const (
	decoyObjectName  = "企业派单记录"
	targetObjectName = "企业"
)

func buildMetaWordQueryNetwork() *interfaces.KnowledgeNetworkDetail {
	detail := &interfaces.KnowledgeNetworkDetail{ID: "kn_meta_word"}
	objects := []struct{ id, name, comment string }{
		{"met_coal_share", decoyObjectName, "企业派单记录是表示企业给司机派活、找车、找司机的一次任务记录，包含。分享企业、分享张数、收货信息id"},
		{"met_coal_company", targetObjectName, "企业主体信息"},
		{"met_coal_used_info", "司机拉运记录", "司机每次接了企业派单拉运任务的详细信息"},
		{"met_coal_road", "路线", "平台司机常跑的路线，起点是煤矿，终点是卸货地点"},
	}
	for _, o := range objects {
		detail.ObjectTypes = append(detail.ObjectTypes, &interfaces.ObjectType{
			ID: o.id, Name: o.name, Comment: o.comment,
		})
	}
	detail.RelationTypes = append(detail.RelationTypes, &interfaces.RelationType{
		ID:                 "rel_road",
		Name:               "司机拉运记录和路线的关系",
		Comment:            "一条拉运记录跑在一条路线上",
		SourceObjectTypeID: "met_coal_used_info",
		TargetObjectTypeID: "met_coal_road",
	})
	return detail
}

// coarseScoresFavouringDecoy is what BM25 actually returned for this network: the decoy far ahead
// of every other object type, the object type the query names down with the rest.
func coarseScoresFavouringDecoy(net *interfaces.KnowledgeNetworkDetail) []*interfaces.ObjectType {
	entries := make([]*interfaces.ObjectType, 0, len(net.ObjectTypes))
	for _, o := range net.ObjectTypes {
		cp := *o
		cp.Score = 1.0
		if cp.Name == decoyObjectName {
			cp.Score = 12.5
		}
		entries = append(entries, &cp)
	}
	return entries
}

// conceptNameOf pulls the leading name out of a rerank document. buildObjectText writes
// "name，comment"; a relation document is space-joined and never matches a bare object name.
func conceptNameOf(doc string) string {
	if i := strings.Index(doc, "，"); i >= 0 {
		return doc[:i]
	}
	return doc
}

// metaWordAwareRerank stands in for a semantic reranker: it recognises that the query is about
// 企业 and is not fooled by a comment that merely repeats the word.
func metaWordAwareRerank() *mockRerankClient {
	return &mockRerankClient{
		rerankFunc: func(query string, documents []string, model string) (*interfaces.RerankResp, error) {
			resp := &interfaces.RerankResp{}
			for i, doc := range documents {
				score := 0.2
				if conceptNameOf(doc) == targetObjectName {
					score = 0.95
				}
				resp.Results = append(resp.Results, interfaces.RerankResult{Index: i, RelevanceScore: score})
			}
			return resp, nil
		},
	}
}

func runMetaWordQuery(t *testing.T, enableRerank bool) []string {
	t.Helper()

	net := buildMetaWordQueryNetwork()
	svc := &localSearchImpl{
		logger: &mockLogger{},
		bknBackend: &mockBknBackend{
			networkDetail:     net,
			objectTypesResp:   &interfaces.ObjectTypeConcepts{Entries: coarseScoresFavouringDecoy(net)},
			relationTypesResp: &interfaces.RelationTypeConcepts{Entries: net.RelationTypes},
		},
		rerankClient: metaWordAwareRerank(),
	}

	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = 1

	req := &interfaces.KnSearchLocalRequest{
		KnID:         "kn_meta_word",
		Query:        "企业 对象类 字段",
		EnableRerank: enableRerank,
	}
	res, err := svc.conceptRetrieval(context.Background(), req, cfg)
	if err != nil {
		t.Fatalf("conceptRetrieval failed: %v", err)
	}

	// Go through the response filter the MCP tool uses, so the assertion is on what the caller sees.
	knResp := &interfaces.KnSearchResp{ObjectTypes: res.ObjectTypes, RelationTypes: res.RelationTypes}
	scope := SearchSchemaScope{IncludeObjectTypes: true, IncludeRelationTypes: true}
	filtered := FilterSearchSchemaResp(knResp, nil, scope, cfg.TopK)

	names := make([]string, 0, len(filtered.ObjectTypes))
	for _, o := range filtered.ObjectTypes {
		if m, ok := o.(map[string]any); ok {
			if n, ok := m["concept_name"].(string); ok {
				names = append(names, n)
			}
		}
	}
	return names
}

func TestMetaWordQueryReachesTheObjectTypeItNames(t *testing.T) {
	names := runMetaWordQuery(t, true)
	t.Logf("with rerank -> %v", names)

	if len(names) == 0 || names[0] != targetObjectName {
		t.Fatalf("expected %q ranked first, got %v", targetObjectName, names)
	}
	for _, n := range names {
		if n == decoyObjectName {
			t.Errorf("the decoy %q must not take the single max_concepts slot, got %v", decoyObjectName, names)
		}
	}
}

// The coarse-only behaviour is kept verbatim behind enable_rerank=false, and it is also the
// evidence that the fixture reproduces the reported failure rather than assuming it.
func TestMetaWordQueryWithoutRerankStillPicksTheDecoy(t *testing.T) {
	names := runMetaWordQuery(t, false)
	t.Logf("without rerank -> %v", names)

	if len(names) == 0 || names[0] != decoyObjectName {
		t.Fatalf("coarse-only path should still rank the BM25 winner %q first, got %v", decoyObjectName, names)
	}
}

// Coarse scoring failing no longer costs object relevance: the reranker is a second, independent
// source of it. Only losing both drops to the endpoint-first order asserted by
// TestObjectScoringDegradesWhenBackendFails.
func TestObjectRelevanceSurvivesCoarseFailure(t *testing.T) {
	net := buildEndpointsLastNetwork()
	svc := &localSearchImpl{
		logger: &mockLogger{},
		bknBackend: &mockBknBackend{
			networkDetail:    net,
			objectTypesError: fmt.Errorf("backend unavailable"),
		},
		rerankClient: &mockRerankClient{
			rerankFunc: func(query string, documents []string, model string) (*interfaces.RerankResp, error) {
				resp := &interfaces.RerankResp{}
				for i, doc := range documents {
					score := 0.1
					if conceptNameOf(doc) == "对象_0" {
						score = 0.99
					}
					resp.Results = append(resp.Results, interfaces.RerankResult{Index: i, RelevanceScore: score})
				}
				return resp, nil
			},
		},
	}

	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = 5

	req := &interfaces.KnSearchLocalRequest{KnID: "kn_endpoints_last", Query: "对象_0", EnableRerank: true}
	res, err := svc.conceptRetrieval(context.Background(), req, cfg)
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if len(res.ObjectTypes) == 0 {
		t.Fatalf("expected object types, got none")
	}
	// obj_0 hangs off no relation in this fixture, so ranking it first can only come from relevance.
	if res.ObjectTypes[0].ConceptID != "obj_0" {
		got := make([]string, 0, len(res.ObjectTypes))
		for _, o := range res.ObjectTypes {
			got = append(got, o.ConceptID)
		}
		t.Errorf("expected obj_0 first on reranker-only relevance, got %v", got)
	}
}

// Both concept categories ride one rerank call: two would double the latency of every schema
// search, and scores from separate invocations are not comparable with each other.
func TestConceptRerankIssuesOneCallCarryingBothCategories(t *testing.T) {
	net := buildMetaWordQueryNetwork()
	rerank := metaWordAwareRerank()
	svc := &localSearchImpl{
		logger: &mockLogger{},
		bknBackend: &mockBknBackend{
			networkDetail:     net,
			objectTypesResp:   &interfaces.ObjectTypeConcepts{Entries: coarseScoresFavouringDecoy(net)},
			relationTypesResp: &interfaces.RelationTypeConcepts{Entries: net.RelationTypes},
		},
		rerankClient: rerank,
	}

	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = 3
	req := &interfaces.KnSearchLocalRequest{KnID: "kn_meta_word", Query: "企业", EnableRerank: true}
	if _, err := svc.conceptRetrieval(context.Background(), req, cfg); err != nil {
		t.Fatalf("conceptRetrieval failed: %v", err)
	}

	if got := rerank.callCount(); got != 1 {
		t.Errorf("expected exactly 1 rerank call, got %d", got)
	}
	docs := rerank.documents()
	want := len(net.ObjectTypes) + len(net.RelationTypes)
	if len(docs) != want {
		t.Fatalf("expected %d documents (%d object types + %d relation types), got %d: %v",
			want, len(net.ObjectTypes), len(net.RelationTypes), len(docs), docs)
	}
	// Object documents come first; the split point is what the score demux relies on.
	for i := 0; i < len(net.ObjectTypes); i++ {
		if strings.Contains(docs[i], " ") {
			t.Errorf("document %d should be an object type, got a relation-shaped one: %q", i, docs[i])
		}
	}
}

// The candidate cap bounds one model call. Object types below the cut stay in the schema; they
// just cannot outrank a reranked one, which is why their score is cleared rather than left on the
// coarse scale.
func TestObjectRerankCandidateLimitBoundsDocuments(t *testing.T) {
	net := buildObjectRankingNetwork()
	rerank := &mockRerankClient{
		rerankFunc: func(query string, documents []string, model string) (*interfaces.RerankResp, error) {
			resp := &interfaces.RerankResp{}
			for i := range documents {
				resp.Results = append(resp.Results, interfaces.RerankResult{Index: i, RelevanceScore: 0.5})
			}
			return resp, nil
		},
	}
	svc := &localSearchImpl{
		logger: &mockLogger{},
		bknBackend: &mockBknBackend{
			networkDetail:     net,
			objectTypesResp:   &interfaces.ObjectTypeConcepts{Entries: buildScoredObjectEntries(net, "员工")},
			relationTypesResp: &interfaces.RelationTypeConcepts{Entries: net.RelationTypes},
		},
		rerankClient: rerank,
	}

	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = 5
	cfg.ObjectRerankCandidateLimit = 2

	req := &interfaces.KnSearchLocalRequest{KnID: "kn_demo", Query: "员工 性别", EnableRerank: true}
	res, err := svc.conceptRetrieval(context.Background(), req, cfg)
	if err != nil {
		t.Fatalf("conceptRetrieval failed: %v", err)
	}
	if len(res.ObjectTypes) == 0 {
		t.Fatalf("expected object types, got none")
	}

	docs := rerank.documents()
	want := cfg.ObjectRerankCandidateLimit + len(net.RelationTypes)
	if len(docs) != want {
		t.Errorf("expected %d documents (%d object candidates + %d relation types), got %d",
			want, cfg.ObjectRerankCandidateLimit, len(net.RelationTypes), len(docs))
	}
}
