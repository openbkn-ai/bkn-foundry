// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func rerankNode(name string, fusionScore float64) *interfaces.KnSearchNode {
	return &interfaces.KnSearchNode{
		ObjectTypeID: "ot1",
		InstanceName: name,
		Score:        fusionScore,
		Properties:   map[string]any{"note": name + " 的说明"},
	}
}

func rerankConfig(mode string) *interfaces.KnSearchSemanticInstanceRetrievalConfig {
	config := DefaultSemanticInstanceRetrievalConfig()
	config.InstanceRerankMode = mode
	return config
}

func rerankService(client *mockRerankClient) *localSearchImpl {
	return &localSearchImpl{logger: &mockLogger{}, rerankClient: client}
}

// 默认 off：一次模型都不能调。多一次调用就是多 100~400ms 和一份模型依赖。
func TestRerankInstances_OffDoesNotCallModel(t *testing.T) {
	client := &mockRerankClient{}
	nodes := []*interfaces.KnSearchNode{rerankNode("a", 1.0), rerankNode("b", 0.9)}

	got := rerankService(client).rerankInstances(context.Background(), "q", nodes, DefaultSemanticInstanceRetrievalConfig())

	if client.callCount() != 0 {
		t.Errorf("default mode must not call the model, got %d calls", client.callCount())
	}
	if got[0].InstanceName != "a" || got[1].InstanceName != "b" {
		t.Errorf("order must be untouched, got %s,%s", got[0].InstanceName, got[1].InstanceName)
	}
}

// on：模型分覆盖排序，且旧的融合分不再决定次序。
func TestRerankInstances_OnReordersByModelScore(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 0, RelevanceScore: 0.11}, // 融合序第 1，模型判为无关
		{Index: 1, RelevanceScore: 0.94}, // 融合序第 2，模型判为强相关
	}}}
	nodes := []*interfaces.KnSearchNode{rerankNode("还款单", 1.0), rerankNode("欠款单", 0.98)}

	got := rerankService(client).rerankInstances(context.Background(), "欠款记录", nodes, rerankConfig(InstanceRerankModeOn))

	if got[0].InstanceName != "欠款单" {
		t.Fatalf("expected the model to promote 欠款单, got %s", got[0].InstanceName)
	}
	if math.Abs(got[0].RerankScore-0.94) > 1e-9 {
		t.Errorf("rerank_score must be carried out, got %.4f", got[0].RerankScore)
	}
	// 精排只改顺序，不覆盖 score：一次响应里只能有一把尺子。融合分跨对象类可比，
	// 覆盖之后未打分候选与 top_n 之外的尾部会带着另一种量纲混进同一个列表。
	if math.Abs(got[0].Score-0.98) > 1e-9 {
		t.Errorf("fusion score must survive reranking, got %.4f", got[0].Score)
	}
	if math.Abs(got[1].Score-1.0) > 1e-9 {
		t.Errorf("fusion score must survive reranking, got %.4f", got[1].Score)
	}
}

// on 模式下，模型没打到分的候选与 top_n 之外的尾部都必须保住各自的融合分——
// 覆盖成模型分会把它们打成 0，而 score 一旦为 0 就与「本地兜底打不出重叠」不可区分。
func TestRerankInstances_OnKeepsFusionScoreForUnscoredAndTail(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 1, RelevanceScore: 0.9},
	}}}
	config := rerankConfig(InstanceRerankModeOn)
	config.RerankTopN = 2
	nodes := []*interfaces.KnSearchNode{rerankNode("unscored", 1.0), rerankNode("scored", 0.9), rerankNode("tail", 0.8)}

	got := rerankService(client).rerankInstances(context.Background(), "q", nodes, config)

	byName := map[string]*interfaces.KnSearchNode{}
	for _, n := range got {
		byName[n.InstanceName] = n
	}
	if math.Abs(byName["unscored"].Score-1.0) > 1e-9 {
		t.Errorf("an unscored candidate must keep its fusion score, got %.4f", byName["unscored"].Score)
	}
	if math.Abs(byName["tail"].Score-0.8) > 1e-9 {
		t.Errorf("the tail must keep its fusion score, got %.4f", byName["tail"].Score)
	}
	if got[0].InstanceName != "scored" {
		t.Errorf("the scored candidate must lead, got %s", got[0].InstanceName)
	}
}

// shadow：序不变，但分带出来——这正是 A/B 取证需要的形态。
func TestRerankInstances_ShadowKeepsOrderButRecordsScores(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 0, RelevanceScore: 0.11},
		{Index: 1, RelevanceScore: 0.94},
	}}}
	nodes := []*interfaces.KnSearchNode{rerankNode("还款单", 1.0), rerankNode("欠款单", 0.98)}
	logger := &mockLogger{}
	svc := &localSearchImpl{logger: logger, rerankClient: client}

	got := svc.rerankInstances(context.Background(), "欠款记录", nodes, rerankConfig(InstanceRerankModeShadow))

	if got[0].InstanceName != "还款单" {
		t.Fatalf("shadow must not reorder, got %s first", got[0].InstanceName)
	}
	if math.Abs(got[0].Score-1.0) > 1e-9 {
		t.Errorf("shadow must not overwrite the fusion score, got %.4f", got[0].Score)
	}
	if math.Abs(got[1].RerankScore-0.94) > 1e-9 {
		t.Errorf("expected the model score to be recorded, got %.4f", got[1].RerankScore)
	}

	var line string
	for _, entry := range logger.entries() {
		if strings.Contains(entry, "[InstanceRerank]") && strings.Contains(entry, "spearman") {
			line = entry
		}
	}
	if line == "" {
		t.Fatal("shadow mode must log the order delta; that log is its entire product")
	}
	if !strings.Contains(line, "top5_changed=") {
		t.Errorf("expected top-K movement in the log, got %q", line)
	}
}

// 模型不可用是常态而非异常：退回融合序，结果不能被打空。
func TestRerankInstances_DegradesToFusionOrder(t *testing.T) {
	cases := map[string]*mockRerankClient{
		"未注册": {rerankError: errors.New("NameNotExist: reranker")},
		"空响应": {rerankResp: nil},
		"索引对不上": {rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
			{Index: 99, RelevanceScore: 0.9},
		}}},
	}
	for name, client := range cases {
		t.Run(name, func(t *testing.T) {
			nodes := []*interfaces.KnSearchNode{rerankNode("a", 1.0), rerankNode("b", 0.9)}
			got := rerankService(client).rerankInstances(context.Background(), "q", nodes, rerankConfig(InstanceRerankModeOn))

			if len(got) != 2 {
				t.Fatalf("degradation must not drop results, got %d", len(got))
			}
			if got[0].InstanceName != "a" {
				t.Errorf("expected the fusion order to survive, got %s first", got[0].InstanceName)
			}
			if got[0].Score != 1.0 {
				t.Errorf("fusion score must be intact, got %.4f", got[0].Score)
			}
		})
	}
}

// 部分回填：能对上的用模型分，对不上的保留融合名次，不整体放弃。
func TestRerankInstances_PartialResultsKeepUnscoredNodes(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 1, RelevanceScore: 0.9},
	}}}
	nodes := []*interfaces.KnSearchNode{rerankNode("a", 1.0), rerankNode("b", 0.9), rerankNode("c", 0.8)}

	got := rerankService(client).rerankInstances(context.Background(), "q", nodes, rerankConfig(InstanceRerankModeOn))

	if len(got) != 3 {
		t.Fatalf("expected all nodes kept, got %d", len(got))
	}
	if got[0].InstanceName != "b" {
		t.Errorf("the only scored node must lead, got %s", got[0].InstanceName)
	}
}

// 乱序返回按 index 对齐，不按返回顺序——厂商两种行为都存在。
func TestRerankInstances_AlignsByIndexNotOrder(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 2, RelevanceScore: 0.30},
		{Index: 0, RelevanceScore: 0.95},
		{Index: 1, RelevanceScore: 0.60},
	}}}
	nodes := []*interfaces.KnSearchNode{rerankNode("a", 0.5), rerankNode("b", 0.5), rerankNode("c", 0.5)}

	got := rerankService(client).rerankInstances(context.Background(), "q", nodes, rerankConfig(InstanceRerankModeOn))

	want := []string{"a", "b", "c"}
	for i, name := range want {
		if got[i].InstanceName != name {
			t.Fatalf("expected %v by index alignment, got %s at %d", want, got[i].InstanceName, i)
		}
	}
}

// 超过 top_n 的尾部不送模型，也不能丢。
func TestRerankInstances_TailBeyondTopNIsKeptUnscored(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 0, RelevanceScore: 0.2},
		{Index: 1, RelevanceScore: 0.9},
	}}}
	config := rerankConfig(InstanceRerankModeOn)
	config.RerankTopN = 2
	nodes := []*interfaces.KnSearchNode{rerankNode("a", 1.0), rerankNode("b", 0.9), rerankNode("tail", 0.8)}

	got := rerankService(client).rerankInstances(context.Background(), "q", nodes, config)

	if len(client.documents()) != 2 {
		t.Errorf("only top_n candidates may be sent to the model, got %d", len(client.documents()))
	}
	if len(got) != 3 || got[2].InstanceName != "tail" {
		t.Fatalf("the tail must stay at the end, got %v", []string{got[0].InstanceName, got[1].InstanceName, got[2].InstanceName})
	}
	if got[0].InstanceName != "b" {
		t.Errorf("expected the head to be reordered, got %s", got[0].InstanceName)
	}
}

// 文档文本：内部元数据不进去，字段名排序稳定，长值按字符截断。
func TestInstanceRerankDocument(t *testing.T) {
	node := &interfaces.KnSearchNode{
		InstanceName: "马拉卡纳球场",
		Properties: map[string]any{
			"_score":       12.5,
			"_instance_id": "sid-1",
			"city_name":    "里约热内卢",
			"comment":      strings.Repeat("长", 500),
			"capacity":     78838, // 非字符串，跳过
		},
	}

	doc := instanceRerankDocument(node, 10)

	if !strings.HasPrefix(doc, "马拉卡纳球场") {
		t.Errorf("instance name must lead the document, got %q", doc)
	}
	if strings.Contains(doc, "_score") || strings.Contains(doc, "_instance_id") {
		t.Errorf("internal metadata must not reach the model, got %q", doc)
	}
	if strings.Contains(doc, "78838") {
		t.Errorf("non-string properties are skipped, got %q", doc)
	}
	if !strings.Contains(doc, "city_name: 里约热内卢") {
		t.Errorf("expected the field to be labelled, got %q", doc)
	}
	if strings.Count(doc, "长") != 10 {
		t.Errorf("long values must be truncated by rune count, got %d", strings.Count(doc, "长"))
	}
	// 字段按名字排序：同一行两次拼出的文本必须一致，否则同一 query 会拿到不同模型分。
	if strings.Index(doc, "capacity") > strings.Index(doc, "city_name") && strings.Contains(doc, "capacity") {
		t.Error("fields must be emitted in sorted order")
	}
}

// 单文档总长有上限：下游会静默截到 4000 字符，被截掉的字段等于没参与打分。
func TestInstanceRerankDocument_TotalLengthCapped(t *testing.T) {
	props := map[string]any{}
	for i := 0; i < 100; i++ {
		props[string(rune('a'+i%26))+strings.Repeat("x", 3)+string(rune('0'+i/26))] = strings.Repeat("值", 200)
	}
	doc := instanceRerankDocument(&interfaces.KnSearchNode{Properties: props}, 200)

	if len([]rune(doc)) > rerankDocumentCharLimit {
		t.Errorf("document must stay under the cap, got %d runes", len([]rune(doc)))
	}
	if doc == "" {
		t.Error("expected some content within the cap")
	}
}

// 无法识别的模式回落 off，不是报错——配置写错不该让检索整体失败。
func TestRerankInstances_UnknownModeFallsBackToOff(t *testing.T) {
	client := &mockRerankClient{}
	nodes := []*interfaces.KnSearchNode{rerankNode("a", 1.0), rerankNode("b", 0.9)}

	got := rerankService(client).rerankInstances(context.Background(), "q", nodes, rerankConfig("ON!!"))

	if client.callCount() != 0 {
		t.Errorf("an unrecognized mode must not call the model, got %d calls", client.callCount())
	}
	if len(got) != 2 {
		t.Errorf("results must be untouched, got %d", len(got))
	}
}

func TestOrderDelta(t *testing.T) {
	a, b, c := rerankNode("a", 1), rerankNode("b", 1), rerankNode("c", 1)

	rho, changed := orderDelta([]*interfaces.KnSearchNode{a, b, c}, []*interfaces.KnSearchNode{a, b, c}, 5)
	if math.Abs(rho-1) > 1e-9 || changed != 0 {
		t.Errorf("identical orders: expected rho=1 changed=0, got %.4f %d", rho, changed)
	}

	rho, _ = orderDelta([]*interfaces.KnSearchNode{a, b, c}, []*interfaces.KnSearchNode{c, b, a}, 5)
	if math.Abs(rho-(-1)) > 1e-9 {
		t.Errorf("reversed order: expected rho=-1, got %.4f", rho)
	}

	_, changed = orderDelta([]*interfaces.KnSearchNode{a, b, c}, []*interfaces.KnSearchNode{c, a, b}, 1)
	if changed != 1 {
		t.Errorf("expected the top-1 to have changed, got %d", changed)
	}
}
