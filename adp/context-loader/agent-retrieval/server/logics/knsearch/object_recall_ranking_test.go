package knsearch

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// issue #778 回归：对象类必须按自身与 query 的相关性排序，不能只靠关系端点带出。
//
// 场景：查询「员工 性别」，「员工」对象类的 name/comment 与 query 命中度最高，
// 但挂着它的唯一一条关系被 reranker 排在末位。修复前该对象类在 max_concepts=5
// 时完全不出现，max_concepts=10 时排在最后。

func buildObjectRankingNetwork() *interfaces.KnowledgeNetworkDetail {
	objNames := []string{
		"工单", "采购订单", "设备", "仓库", "结算单",
		"运单", "客户", "入库记录", "质检记录", "员工",
	}
	detail := &interfaces.KnowledgeNetworkDetail{ID: "kn_demo"}
	for i, n := range objNames {
		detail.ObjectTypes = append(detail.ObjectTypes, &interfaces.ObjectType{
			ID:      fmt.Sprintf("obj_%d", i),
			Name:    n,
			Comment: n + "实体",
		})
	}
	detail.ObjectTypes[9].Comment = "员工主体，含姓名、性别、手机号、岗位"

	rels := [][3]string{
		{"工单-归属-采购订单", "obj_0", "obj_1"},
		{"采购订单-使用-设备", "obj_1", "obj_2"},
		{"采购订单-来自-仓库", "obj_1", "obj_3"},
		{"采购订单-产生-结算单", "obj_1", "obj_4"},
		{"运单-关联-采购订单", "obj_5", "obj_1"},
		{"客户-下达-采购订单", "obj_6", "obj_1"},
		{"入库-属于-运单", "obj_7", "obj_5"},
		{"质检-属于-运单", "obj_8", "obj_5"},
		{"设备-配备-员工", "obj_2", "obj_9"},
	}
	for i, r := range rels {
		detail.RelationTypes = append(detail.RelationTypes, &interfaces.RelationType{
			ID:                 fmt.Sprintf("rel_%d", i),
			Name:               r[0],
			Comment:            r[0],
			SourceObjectTypeID: r[1],
			TargetObjectTypeID: r[2],
		})
	}
	return detail
}

// objectRankingRerank 把挂着「员工」的那条关系排到末位。
type objectRankingRerank struct{}

func (d *objectRankingRerank) Rerank(ctx context.Context, query string, documents []string, model string) (*interfaces.RerankResp, error) {
	resp := &interfaces.RerankResp{}
	for i, doc := range documents {
		score := 0.9 - float64(i)*0.01
		if strings.Contains(doc, "员工") {
			score = 0.5
		}
		resp.Results = append(resp.Results, interfaces.RerankResult{Index: i, RelevanceScore: score})
	}
	return resp, nil
}

func (d *objectRankingRerank) Chat(ctx context.Context, req *interfaces.LLMChatReq) (string, error) {
	return "", nil
}

// buildScoredObjectEntries 模拟 BKN 概念检索：与 query 最匹配的对象类得分最高。
func buildScoredObjectEntries(net *interfaces.KnowledgeNetworkDetail, topName string) []*interfaces.ObjectType {
	entries := make([]*interfaces.ObjectType, 0, len(net.ObjectTypes))
	for _, o := range net.ObjectTypes {
		cp := *o
		cp.Score = 1.0
		if cp.Name == topName {
			cp.Score = 12.5
		}
		entries = append(entries, &cp)
	}
	return entries
}

func runObjectRankingCase(t *testing.T, maxConcepts int, scope SearchSchemaScope, conceptGroups []string) []string {
	t.Helper()

	net := buildObjectRankingNetwork()
	backend := &mockBknBackend{
		networkDetail:     net,
		objectTypesResp:   &interfaces.ObjectTypeConcepts{Entries: buildScoredObjectEntries(net, "员工")},
		relationTypesResp: &interfaces.RelationTypeConcepts{Entries: net.RelationTypes},
		actionTypesResp:   &interfaces.ActionTypeConcepts{Entries: nil},
	}
	svc := &localSearchImpl{
		logger:       &mockLogger{},
		bknBackend:   backend,
		rerankClient: &objectRankingRerank{},
	}

	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = maxConcepts
	cfg.ConceptGroups = conceptGroups

	req := &interfaces.KnSearchLocalRequest{KnID: "kn_demo", Query: "员工 性别", EnableRerank: true}
	res, err := svc.conceptRetrieval(context.Background(), req, cfg)
	if err != nil {
		t.Fatalf("conceptRetrieval failed: %v", err)
	}

	knResp := &interfaces.KnSearchResp{ObjectTypes: res.ObjectTypes, RelationTypes: res.RelationTypes}
	filtered := FilterSearchSchemaResp(knResp, nil, scope, maxConcepts)

	names := []string{}
	for _, o := range filtered.ObjectTypes {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		if n, ok := m["concept_name"].(string); ok {
			names = append(names, n)
		}
	}
	return names
}

func TestObjectTypesRankedByOwnRelevance(t *testing.T) {
	allOn := SearchSchemaScope{IncludeObjectTypes: true, IncludeRelationTypes: true, IncludeActionTypes: true}
	objectOnly := SearchSchemaScope{IncludeObjectTypes: true}

	cases := []struct {
		name          string
		maxConcepts   int
		scope         SearchSchemaScope
		conceptGroups []string
		// 关系一并返回时，端点补齐允许超出 max_concepts（既有的 schema 自洽契约）；
		// 只要对象类，就必须严格受 max_concepts 约束。
		exactLimit bool
	}{
		{"四类全开/传统路径", 5, allOn, nil, false},
		{"仅对象类/传统路径", 5, objectOnly, nil, true},
		{"四类全开/分组路径", 5, allOn, []string{"grp_demo"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runObjectRankingCase(t, tc.maxConcepts, tc.scope, tc.conceptGroups)
			t.Logf("max_concepts=%d -> %v", tc.maxConcepts, got)

			if len(got) == 0 {
				t.Fatalf("expected object types, got none")
			}
			if got[0] != "员工" {
				t.Errorf("expected 员工 ranked first, got %v", got)
			}
			if tc.exactLimit && len(got) != tc.maxConcepts {
				t.Errorf("expected exactly %d object types (max_concepts), got %d: %v", tc.maxConcepts, len(got), got)
			}
		})
	}
}

// 孤儿对象类（不挂任何关系）同样必须能按相关性召回。
func TestOrphanObjectTypeIsRecalled(t *testing.T) {
	net := buildObjectRankingNetwork()
	// 摘掉唯一一条挂着「员工」的关系，令其成为孤儿对象
	net.RelationTypes = net.RelationTypes[:len(net.RelationTypes)-1]

	backend := &mockBknBackend{
		networkDetail:     net,
		objectTypesResp:   &interfaces.ObjectTypeConcepts{Entries: buildScoredObjectEntries(net, "员工")},
		relationTypesResp: &interfaces.RelationTypeConcepts{Entries: net.RelationTypes},
	}
	svc := &localSearchImpl{
		logger:       &mockLogger{},
		bknBackend:   backend,
		rerankClient: &objectRankingRerank{},
	}

	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = 5

	req := &interfaces.KnSearchLocalRequest{KnID: "kn_demo", Query: "员工 性别", EnableRerank: true}
	res, err := svc.conceptRetrieval(context.Background(), req, cfg)
	if err != nil {
		t.Fatalf("conceptRetrieval failed: %v", err)
	}

	knResp := &interfaces.KnSearchResp{ObjectTypes: res.ObjectTypes, RelationTypes: res.RelationTypes}
	scope := SearchSchemaScope{IncludeObjectTypes: true, IncludeRelationTypes: true}
	filtered := FilterSearchSchemaResp(knResp, nil, scope, 5)

	names := []string{}
	for _, o := range filtered.ObjectTypes {
		if m, ok := o.(map[string]any); ok {
			if n, ok := m["concept_name"].(string); ok {
				names = append(names, n)
			}
		}
	}
	t.Logf("orphan case -> %v", names)

	if len(names) == 0 || names[0] != "员工" {
		t.Errorf("expected orphan 员工 ranked first, got %v", names)
	}
}

// buildEndpointsLastNetwork 构造一个「关系端点全部排在定义序末尾」的网络。
//
// 这个形状是刻意的：只有当端点集不等于定义序的前若干个时，「端点优先」与「定义序」
// 才是两种可区分的顺序，降级路径的顺序断言才有鉴别力。
func buildEndpointsLastNetwork() *interfaces.KnowledgeNetworkDetail {
	detail := &interfaces.KnowledgeNetworkDetail{ID: "kn_endpoints_last"}
	for i := 0; i < 10; i++ {
		detail.ObjectTypes = append(detail.ObjectTypes, &interfaces.ObjectType{
			ID:      fmt.Sprintf("obj_%d", i),
			Name:    fmt.Sprintf("对象_%d", i),
			Comment: fmt.Sprintf("对象_%d 说明", i),
		})
	}
	// 关系只挂在定义序靠后的 obj_6..obj_9 上
	rels := [][3]string{
		{"关系_A", "obj_6", "obj_7"},
		{"关系_B", "obj_8", "obj_9"},
		{"关系_C", "obj_7", "obj_8"},
	}
	for i, r := range rels {
		detail.RelationTypes = append(detail.RelationTypes, &interfaces.RelationType{
			ID:                 fmt.Sprintf("rel_%d", i),
			Name:               r[0],
			Comment:            r[0],
			SourceObjectTypeID: r[1],
			TargetObjectTypeID: r[2],
		})
	}
	return detail
}

// 打分不可用（BKN 检索失败）时必须优雅降级：不报错、不返回空，
// 且顺序退回修复前的「关系端点优先」，不能凭空改成定义序。
func TestObjectScoringDegradesWhenBackendFails(t *testing.T) {
	net := buildEndpointsLastNetwork()
	backend := &mockBknBackend{
		networkDetail:    net,
		objectTypesError: fmt.Errorf("backend unavailable"),
	}
	svc := &localSearchImpl{
		logger:       &mockLogger{},
		bknBackend:   backend,
		rerankClient: &objectRankingRerank{},
	}

	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = 5

	req := &interfaces.KnSearchLocalRequest{KnID: "kn_endpoints_last", Query: "对象", EnableRerank: true}
	res, err := svc.conceptRetrieval(context.Background(), req, cfg)
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if len(res.ObjectTypes) == 0 {
		t.Fatalf("expected object types on degraded path, got none")
	}

	endpoints := map[string]struct{}{}
	for _, rel := range res.RelationTypes {
		endpoints[rel.SourceObjectTypeID] = struct{}{}
		endpoints[rel.TargetObjectTypeID] = struct{}{}
	}
	if len(endpoints) == 0 {
		t.Fatalf("fixture must yield relation endpoints, got none")
	}

	got := make([]string, 0, len(res.ObjectTypes))
	for _, obj := range res.ObjectTypes {
		got = append(got, obj.ConceptID)
	}
	t.Logf("degraded order -> %v (endpoints=%d)", got, len(endpoints))

	// 断言的鉴别力前提：端点集不等于定义序的前 N 个，否则「端点优先」与「定义序」
	// 无从区分，这条断言就是空的。
	definitionOrderHead := map[string]struct{}{}
	for i := 0; i < len(endpoints); i++ {
		definitionOrderHead[fmt.Sprintf("obj_%d", i)] = struct{}{}
	}
	sameAsDefinitionOrder := true
	for id := range endpoints {
		if _, ok := definitionOrderHead[id]; !ok {
			sameAsDefinitionOrder = false
			break
		}
	}
	if sameAsDefinitionOrder {
		t.Fatalf("fixture cannot distinguish endpoint-first from definition order; endpoints=%v", endpoints)
	}

	seenNonEndpoint := false
	for _, id := range got {
		_, isEndpoint := endpoints[id]
		if isEndpoint && seenNonEndpoint {
			t.Errorf("degraded path must keep relation endpoints first, got %s after a non-endpoint: %v", id, got)
			break
		}
		if !isEndpoint {
			seenNonEndpoint = true
		}
	}
}

// 入选关系的端点必须全部出现在对象类里，哪怕超出 maxObjectCount 预算——
// 否则返回的关系会指向缺失的对象，调用方拿到断头引用。
func TestRelationEndpointsAreNeverDropped(t *testing.T) {
	// 5 条互不共享端点的关系 → 10 个不同端点，刻意超过 maxObjectCount 预算
	detail := &interfaces.KnowledgeNetworkDetail{ID: "kn_disjoint"}
	for i := 0; i < 12; i++ {
		detail.ObjectTypes = append(detail.ObjectTypes, &interfaces.ObjectType{
			ID:      fmt.Sprintf("obj_%d", i),
			Name:    fmt.Sprintf("对象_%d", i),
			Comment: fmt.Sprintf("对象_%d 说明", i),
		})
	}
	for i := 0; i < 5; i++ {
		detail.RelationTypes = append(detail.RelationTypes, &interfaces.RelationType{
			ID:                 fmt.Sprintf("rel_%d", i),
			Name:               fmt.Sprintf("关系_%d", i),
			SourceObjectTypeID: fmt.Sprintf("obj_%d", i*2),
			TargetObjectTypeID: fmt.Sprintf("obj_%d", i*2+1),
		})
	}
	// 让两个与关系无关的对象类拿到最高分，占满 topK 名额
	scoredEntries := make([]*interfaces.ObjectType, 0, len(detail.ObjectTypes))
	for _, o := range detail.ObjectTypes {
		cp := *o
		cp.Score = 1.0
		if cp.ID == "obj_10" || cp.ID == "obj_11" {
			cp.Score = 99.0
		}
		scoredEntries = append(scoredEntries, &cp)
	}

	svc := &localSearchImpl{
		logger: &mockLogger{},
		bknBackend: &mockBknBackend{
			networkDetail:     detail,
			objectTypesResp:   &interfaces.ObjectTypeConcepts{Entries: scoredEntries},
			relationTypesResp: &interfaces.RelationTypeConcepts{Entries: detail.RelationTypes},
		},
		rerankClient: &objectRankingRerank{},
	}

	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = 5

	req := &interfaces.KnSearchLocalRequest{KnID: "kn_disjoint", Query: "对象", EnableRerank: true}
	res, err := svc.conceptRetrieval(context.Background(), req, cfg)
	if err != nil {
		t.Fatalf("conceptRetrieval failed: %v", err)
	}

	present := map[string]struct{}{}
	for _, obj := range res.ObjectTypes {
		present[obj.ConceptID] = struct{}{}
	}
	for _, rel := range res.RelationTypes {
		for _, id := range []string{rel.SourceObjectTypeID, rel.TargetObjectTypeID} {
			if _, ok := present[id]; !ok {
				t.Errorf("relation %s references object %s missing from the response (objects=%d)",
					rel.ConceptID, id, len(res.ObjectTypes))
			}
		}
	}
}
