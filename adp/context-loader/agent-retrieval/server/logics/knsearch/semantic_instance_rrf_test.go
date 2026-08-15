// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// --- 测试脚手架 ---

func rrfTestObjectType() *interfaces.KnSearchObjectType {
	return &interfaces.KnSearchObjectType{
		ConceptID:   "ot1",
		ConceptName: "Type1",
		DataProperties: []*interfaces.KnSearchDataProperty{
			{
				Name: "title",
				Type: "text",
				ConditionOperations: []interfaces.KnOperationType{
					interfaces.KnOperationTypeKnn,
					interfaces.KnOperationTypeMatch,
				},
			},
		},
	}
}

// condChannel 判断一条请求属于哪一路：拆分之后每条查询只含单一算子。
func condChannel(cond *interfaces.KnCondition) string {
	if cond == nil || len(cond.SubConditions) == 0 {
		return ""
	}
	for _, sub := range cond.SubConditions {
		if sub.Operation == interfaces.KnOperationTypeKnn {
			return channelKnn
		}
	}
	return channelMatch
}

func instanceRow(id string, score float64) map[string]any {
	row := map[string]any{
		"unique_identities": map[string]any{"id": id},
		"instance_name":     id,
	}
	if score > 0 {
		row["_score"] = score
	}
	return row
}

func rowsToResp(rows []map[string]any) *interfaces.QueryObjectInstancesResp {
	data := make([]any, 0, len(rows))
	for _, r := range rows {
		data = append(data, r)
	}
	return &interfaces.QueryObjectInstancesResp{Data: data}
}

func countPrefixed(nodes []*interfaces.KnSearchNode, prefix string) int {
	n := 0
	for _, node := range nodes {
		if strings.HasPrefix(node.InstanceName, prefix) {
			n++
		}
	}
	return n
}

// bm25Flood 造 50 条 BM25 高分行，向量行只有 5 条低分行——这正是线上分布：
// BM25 无上界，knn 相似度在 0~1。
func bm25Flood() []map[string]any {
	rows := make([]map[string]any, 0, 50)
	for i := 0; i < 50; i++ {
		rows = append(rows, instanceRow(fmt.Sprintf("m%02d", i+1), 20.0-0.1*float64(i)))
	}
	return rows
}

func vectorHits() []map[string]any {
	rows := make([]map[string]any, 0, 5)
	for i := 0; i < 5; i++ {
		rows = append(rows, instanceRow(fmt.Sprintf("v%d", i+1), 0.9-0.1*float64(i)))
	}
	return rows
}

// --- 用例 ---

// 融合路径下向量命中不会被 BM25 洪水挤掉；旧的单查询路径会。
func TestFusedRetrieval_VectorHitsSurviveBM25Flood(t *testing.T) {
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			if condChannel(req.Cond) == channelKnn {
				return rowsToResp(vectorHits()), nil
			}
			return rowsToResp(bm25Flood()), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultSemanticInstanceRetrievalConfig()
	objType := rrfTestObjectType()

	nodes, err := svc.retrieveInstancesForObjectType(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "test"}, objType, config, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(nodes); got != config.PerTypeInstanceLimit {
		t.Fatalf("expected %d nodes, got %d", config.PerTypeInstanceLimit, got)
	}
	if n := countPrefixed(nodes, "v"); n < 2 {
		t.Errorf("expected vector hits to survive fusion, got %d of %d nodes", n, len(nodes))
	}
	if calls := mockQuery.calls(); calls != 2 {
		t.Errorf("expected one query per channel, got %d", calls)
	}
}

// 旧路径（enable_rrf_fusion=false）复现缺陷：单条 OR 查询里 BM25 主导，
// 向量命中一条都进不了 Top-K。留这个用例是为了让回归可见。
func TestSingleQueryRetrieval_VectorHitsLostToBM25(t *testing.T) {
	merged := append(bm25Flood(), vectorHits()...) // 单查询按 _score 降序返回，BM25 行全部在前
	mockQuery := &mockOntologyQuery{instancesResp: rowsToResp(merged)}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultSemanticInstanceRetrievalConfig()
	config.EnableRRFFusion = boolPtr(false)

	nodes, err := svc.retrieveInstancesForObjectType(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "test"}, rrfTestObjectType(), config, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := countPrefixed(nodes, "v"); n != 0 {
		t.Errorf("legacy path unexpectedly kept %d vector hits; the regression guard is stale", n)
	}
	if calls := mockQuery.calls(); calls != 1 {
		t.Errorf("legacy path should issue a single query, got %d", calls)
	}
}

// RRF 分的算式：Σ 1/(k+rank) × (k+1)。
func TestFuseByRRF_ScoreMath(t *testing.T) {
	k := 60
	knn := channelOutcome{name: channelKnn, scored: true, nodes: []*interfaces.KnSearchNode{
		{ObjectTypeID: "ot1", InstanceName: "a", UniqueIdentities: map[string]any{"id": "a"}},
		{ObjectTypeID: "ot1", InstanceName: "b", UniqueIdentities: map[string]any{"id": "b"}},
	}}
	match := channelOutcome{name: channelMatch, scored: true, nodes: []*interfaces.KnSearchNode{
		{ObjectTypeID: "ot1", InstanceName: "b", UniqueIdentities: map[string]any{"id": "b"}},
		{ObjectTypeID: "ot1", InstanceName: "c", UniqueIdentities: map[string]any{"id": "c"}},
	}}

	fused := fuseByRRF([]channelOutcome{knn, match}, k, equalWeights())
	if len(fused) != 3 {
		t.Fatalf("expected 3 unique instances, got %d", len(fused))
	}

	byName := map[string]float64{}
	for _, n := range fused {
		byName[n.InstanceName] = n.Score
	}
	norm := float64(k + 1)
	want := map[string]float64{
		"a": (1.0 / float64(k+1)) * norm,                  // 仅 knn 第 1 → 1.0
		"b": (1.0/float64(k+2) + 1.0/float64(k+1)) * norm, // knn 第 2 + match 第 1
		"c": (1.0 / float64(k+2)) * norm,                  // 仅 match 第 2
	}
	for name, expected := range want {
		if math.Abs(byName[name]-expected) > 1e-9 {
			t.Errorf("%s: expected score %.6f, got %.6f", name, expected, byName[name])
		}
	}
	// 两路都命中的 b 必须排第一：那是真信号。
	sortNodesByScore(fused)
	if fused[0].InstanceName != "b" {
		t.Errorf("expected 'b' (hit by both channels) first, got %s", fused[0].InstanceName)
	}
	// 任一通道的第 1 名恰为 1.0——这个锚点不随该对象类发了几路而变，
	// 否则双通道对象类里只被一路命中的实例会被系统性压低（VM 实测踩过）。
	single := fuseByRRF([]channelOutcome{knn}, k, equalWeights())
	if math.Abs(single[0].Score-1.0) > 1e-9 {
		t.Errorf("expected rank-1-in-one-channel to score 1.0, got %.6f", single[0].Score)
	}
	if math.Abs(byName["a"]-1.0) > 1e-9 {
		t.Errorf("rank-1 in one of two channels must also score 1.0, got %.6f", byName["a"])
	}
}

// 同一实例被两路召回时只保留一份，原始召回分取较大者。
func TestFuseByRRF_DedupKeepsMaxRecallScore(t *testing.T) {
	node := func(score float64) *interfaces.KnSearchNode {
		return &interfaces.KnSearchNode{
			ObjectTypeID:     "ot1",
			InstanceName:     "same",
			UniqueIdentities: map[string]any{"id": "same"},
			RecallScore:      score,
		}
	}
	fused := fuseByRRF([]channelOutcome{
		{name: channelKnn, scored: true, nodes: []*interfaces.KnSearchNode{node(0.8)}},
		{name: channelMatch, scored: true, nodes: []*interfaces.KnSearchNode{node(17.2)}},
	}, 60, equalWeights())

	if len(fused) != 1 {
		t.Fatalf("expected dedup to a single node, got %d", len(fused))
	}
	if math.Abs(fused[0].RecallScore-17.2) > 1e-9 {
		t.Errorf("expected max recall score 17.2, got %.4f", fused[0].RecallScore)
	}
	if math.Abs(fused[0].Score-2.0) > 1e-9 {
		t.Errorf("expected fused score 2.0 (rank 1 in both channels), got %.6f", fused[0].Score)
	}
}

// 缺唯一标识且缺实例名的行不参与去重——宁可重复也不误合并两个不同实例。
func TestFuseByRRF_AnonymousRowsNotMerged(t *testing.T) {
	anon := func() *interfaces.KnSearchNode { return &interfaces.KnSearchNode{ObjectTypeID: "ot1"} }
	fused := fuseByRRF([]channelOutcome{
		{name: channelKnn, scored: true, nodes: []*interfaces.KnSearchNode{anon()}},
		{name: channelMatch, scored: true, nodes: []*interfaces.KnSearchNode{anon()}},
	}, 60, equalWeights())
	if len(fused) != 2 {
		t.Fatalf("expected anonymous rows kept separate, got %d", len(fused))
	}
}

// knn 通道 400（字段没有向量映射）不再打掉整个对象类，全文那路照常返回。
func TestFusedRetrieval_KnnChannelFailureIsolated(t *testing.T) {
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			if condChannel(req.Cond) == channelKnn {
				return nil, errors.New("OntologyQuery.InvalidParameter.Condition: left field is not a vector field")
			}
			return rowsToResp([]map[string]any{instanceRow("m1", 12.0), instanceRow("m2", 11.0)}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}

	nodes, err := svc.retrieveInstancesForObjectType(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "test"},
		rrfTestObjectType(), DefaultSemanticInstanceRetrievalConfig(), true)
	if err != nil {
		t.Fatalf("a single failing channel must not fail the object type: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes from the surviving channel, got %d", len(nodes))
	}
}

// 两路全失败才向上报错，由调用方跳过该对象类。
func TestFusedRetrieval_AllChannelsFailReturnsError(t *testing.T) {
	mockQuery := &mockOntologyQuery{instancesError: errors.New("downstream down")}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}

	_, err := svc.retrieveInstancesForObjectType(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "test"},
		rrfTestObjectType(), DefaultSemanticInstanceRetrievalConfig(), true)
	if err == nil {
		t.Fatal("expected an error when every channel fails")
	}
}

// 无 _score（源库直查）时名次无意义：跳过 RRF，走本地兜底打分，
// MinDirectRelevance 在这条路上才生效。
func TestFusedRetrieval_UnscoredRowsUseLocalScoring(t *testing.T) {
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			if condChannel(req.Cond) == channelKnn {
				return rowsToResp(nil), nil
			}
			return rowsToResp([]map[string]any{
				instanceRow("test instance", 0), // 目标含查询词 → 0.5
				instanceRow("unrelated", 0),     // 无重叠 → 0，被 MinDirectRelevance 滤掉
			}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultSemanticInstanceRetrievalConfig()

	nodes, err := svc.retrieveInstancesForObjectType(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "test"}, rrfTestObjectType(), config, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected the irrelevant row to be filtered, got %d nodes", len(nodes))
	}
	if nodes[0].InstanceName != "test instance" {
		t.Errorf("expected 'test instance', got %s", nodes[0].InstanceName)
	}
	if nodes[0].Score < config.MinDirectRelevance {
		t.Errorf("expected local fallback score >= %.2f, got %.2f", config.MinDirectRelevance, nodes[0].Score)
	}
}

// 相对分数过滤在通道内做：融合之后第一名恒为 1.0，"整体都不相关"表达不出来。
func TestPruneChannelByScoreRatio(t *testing.T) {
	nodes := []*interfaces.KnSearchNode{
		{InstanceName: "top", RecallScore: 0.9},
		{InstanceName: "far", RecallScore: 0.1},
	}
	kept := pruneChannelByScoreRatio(nodes, 0.5)
	if len(kept) != 1 || kept[0].InstanceName != "top" {
		t.Fatalf("expected only the top row kept, got %v", kept)
	}

	// 全零分（无 _score）时不做裁剪，交给本地兜底打分。
	unscored := []*interfaces.KnSearchNode{{InstanceName: "a"}, {InstanceName: "b"}}
	if got := pruneChannelByScoreRatio(unscored, 0.5); len(got) != 2 {
		t.Errorf("unscored rows must not be pruned, got %d", len(got))
	}
}

// 通道条件构造：没有向量字段 / 本轮不发向量时，knn 通道整体不发出。
func TestBuildChannelConditions(t *testing.T) {
	config := DefaultSemanticInstanceRetrievalConfig()
	withKnn := findSemanticSearchableFields(rrfTestObjectType())

	if cond := buildKnnOnlyCondition("q", withKnn, config, false); cond != nil {
		t.Error("expected no knn condition when knn is disabled for this round")
	}
	cond := buildKnnOnlyCondition("q", withKnn, config, true)
	if cond == nil || len(cond.SubConditions) != 1 {
		t.Fatalf("expected exactly one knn sub-condition, got %+v", cond)
	}
	if cond.SubConditions[0].Operation != interfaces.KnOperationTypeKnn {
		t.Errorf("knn channel must carry only knn sub-conditions, got %s", cond.SubConditions[0].Operation)
	}
	if cond.SubConditions[0].LimitValue != config.PerTypeInstanceLimit {
		t.Errorf("expected k=%d, got %v", config.PerTypeInstanceLimit, cond.SubConditions[0].LimitValue)
	}

	matchOnly := []searchableField{{Name: "title", HasMatch: true}}
	if cond := buildKnnOnlyCondition("q", matchOnly, config, true); cond != nil {
		t.Error("expected no knn condition when no field carries a vector operator")
	}
	mc := buildMatchOnlyCondition("q", matchOnly, config)
	if mc == nil || mc.SubConditions[0].Operation != interfaces.KnOperationTypeMatch {
		t.Fatalf("expected a match-only condition, got %+v", mc)
	}

	knnOnly := []searchableField{{Name: "title", HasKnn: true}}
	if cond := buildMatchOnlyCondition("q", knnOnly, config); cond != nil {
		t.Error("expected no match condition when no field carries a match operator")
	}
}

// 去重键跨通道稳定，且不同对象类的同名实例不会撞在一起。
func TestInstanceKey(t *testing.T) {
	a := &interfaces.KnSearchNode{ObjectTypeID: "ot1", UniqueIdentities: map[string]any{"b": 2, "a": 1}}
	b := &interfaces.KnSearchNode{ObjectTypeID: "ot1", UniqueIdentities: map[string]any{"a": 1, "b": 2}}
	if instanceKey(a) != instanceKey(b) {
		t.Errorf("key must not depend on map iteration order: %q vs %q", instanceKey(a), instanceKey(b))
	}

	other := &interfaces.KnSearchNode{ObjectTypeID: "ot2", UniqueIdentities: map[string]any{"a": 1, "b": 2}}
	if instanceKey(a) == instanceKey(other) {
		t.Error("instances from different object types must not share a key")
	}

	named := &interfaces.KnSearchNode{ObjectTypeID: "ot1", InstanceName: "n"}
	if instanceKey(named) == "" {
		t.Error("expected fallback to instance name")
	}
	if instanceKey(&interfaces.KnSearchNode{ObjectTypeID: "ot1"}) != "" {
		t.Error("expected empty key when neither identity, id property, name nor properties are present")
	}
}

// 索引行常常两个顶层身份字段都空，身份落在 properties 的 _instance_id 上（VM 实测）。
func TestInstanceKey_FallsBackToInstanceIDProperty(t *testing.T) {
	knnRow := &interfaces.KnSearchNode{
		ObjectTypeID: "stadiums",
		Properties:   map[string]any{"_instance_id": "sid-42", "stadium_name": "Maracana", "_score": 0.63},
	}
	matchRow := &interfaces.KnSearchNode{
		ObjectTypeID: "stadiums",
		Properties:   map[string]any{"_instance_id": "sid-42", "stadium_name": "Maracana", "_score": 3.09},
	}
	if instanceKey(knnRow) != instanceKey(matchRow) {
		t.Fatalf("same instance across channels must share a key: %q vs %q", instanceKey(knnRow), instanceKey(matchRow))
	}

	fused := fuseByRRF([]channelOutcome{
		{name: channelKnn, scored: true, nodes: []*interfaces.KnSearchNode{knnRow}},
		{name: channelMatch, scored: true, nodes: []*interfaces.KnSearchNode{matchRow}},
	}, 60, equalWeights())
	if len(fused) != 1 {
		t.Fatalf("expected the duplicate to be merged, got %d nodes", len(fused))
	}
}

// 连 _instance_id 都没有时按属性内容取指纹，仍能跨通道认出同一行。
func TestInstanceKey_FallsBackToPropertiesFingerprint(t *testing.T) {
	a := &interfaces.KnSearchNode{ObjectTypeID: "ot1", Properties: map[string]any{"b": 2, "a": 1}}
	b := &interfaces.KnSearchNode{ObjectTypeID: "ot1", Properties: map[string]any{"a": 1, "b": 2}}
	if instanceKey(a) != instanceKey(b) {
		t.Errorf("fingerprint must not depend on map order: %q vs %q", instanceKey(a), instanceKey(b))
	}
	c := &interfaces.KnSearchNode{ObjectTypeID: "ot1", Properties: map[string]any{"a": 1, "b": 3}}
	if instanceKey(a) == instanceKey(c) {
		t.Error("different property values must produce different keys")
	}
}

// equalWeights 等权，即默认 knn_weight=0.5。老用例全部走它——「加了权重之后默认
// 行为逐位不变」这件事，就靠这些原样保留的断言盯着。
func equalWeights() map[string]float64 {
	return channelWeights(DefaultSemanticInstanceRetrievalConfig())
}
