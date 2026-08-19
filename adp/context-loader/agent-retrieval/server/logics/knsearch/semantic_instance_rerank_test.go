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

// Default is off: the model cannot be called at a time. One more call means 100~400ms more and one more model dependency.
func TestRerankInstances_OffDoesNotCallModel(t *testing.T) {
	client := &mockRerankClient{}
	nodes := []*interfaces.KnSearchNode{rerankNode("a", 1.0), rerankNode("b", 0.9)}

	got, _ := rerankService(client).rerankInstances(context.Background(), "q", nodes, DefaultSemanticInstanceRetrievalConfig(), nil)

	if client.callCount() != 0 {
		t.Errorf("default mode must not call the model, got %d calls", client.callCount())
	}
	if got[0].InstanceName != "a" || got[1].InstanceName != "b" {
		t.Errorf("order must be untouched, got %s,%s", got[0].InstanceName, got[1].InstanceName)
	}
}

// on: Model points cover sorting, and the old fusion points no longer determine the order.
func TestRerankInstances_OnReordersByModelScore(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 0, RelevanceScore: 0.11}, // The fusion order is 1st, and the model is judged to be irrelevant.
		{Index: 1, RelevanceScore: 0.94}, // The fusion order is 2nd, and the model is judged to be strongly correlated.
	}}}
	nodes := []*interfaces.KnSearchNode{rerankNode("还款单", 1.0), rerankNode("欠款单", 0.98)}

	got, _ := rerankService(client).rerankInstances(context.Background(), "欠款记录", nodes, rerankConfig(InstanceRerankModeOn), nil)

	if got[0].InstanceName != "欠款单" {
		t.Fatalf("expected the model to promote 欠款单, got %s", got[0].InstanceName)
	}
	if math.Abs(got[0].RerankScore-0.94) > 1e-9 {
		t.Errorf("rerank_score must be carried out, got %.4f", got[0].RerankScore)
	}
	// Fine sorting only changes the order and does not cover score: there can only be one ruler in one response. Fusion points are comparable across object types,
	// After covering, the unscored candidates and the tails outside top_n will be mixed into the same list with another dimension.
	if math.Abs(got[0].Score-0.98) > 1e-9 {
		t.Errorf("fusion score must survive reranking, got %.4f", got[0].Score)
	}
	if math.Abs(got[1].Score-1.0) > 1e-9 {
		t.Errorf("fusion score must survive reranking, got %.4f", got[1].Score)
	}
}

// In the on mode, the candidates that have not been scored by the model and the tails other than top_n must keep their respective fusion scores——.
// Overriding them with model scores will mark them as 0, and once the score is 0, it is indistinguishable from "local fallback scores cannot overlap".
func TestRerankInstances_OnKeepsFusionScoreForUnscoredAndTail(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 1, RelevanceScore: 0.9},
	}}}
	config := rerankConfig(InstanceRerankModeOn)
	config.RerankTopN = 2
	nodes := []*interfaces.KnSearchNode{rerankNode("unscored", 1.0), rerankNode("scored", 0.9), rerankNode("tail", 0.8)}

	got, _ := rerankService(client).rerankInstances(context.Background(), "q", nodes, config, nil)

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

// Shadow: The order remains unchanged, but is brought out separately - this is exactly the form required for A/B evidence collection.
func TestRerankInstances_ShadowKeepsOrderButRecordsScores(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 0, RelevanceScore: 0.11},
		{Index: 1, RelevanceScore: 0.94},
	}}}
	nodes := []*interfaces.KnSearchNode{rerankNode("还款单", 1.0), rerankNode("欠款单", 0.98)}
	logger := &mockLogger{}
	svc := &localSearchImpl{logger: logger, rerankClient: client}

	got, _ := svc.rerankInstances(context.Background(), "欠款记录", nodes, rerankConfig(InstanceRerankModeShadow), nil)

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

// Unavailability of the model is the norm rather than an exception: return to the fusion sequence and the results cannot be nulled.
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
			got, _ := rerankService(client).rerankInstances(context.Background(), "q", nodes, rerankConfig(InstanceRerankModeOn), nil)

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

// Partial backfilling: Use model points for those who can match, keep the fusion ranking for those who can't match, and do not give up as a whole.
func TestRerankInstances_PartialResultsKeepUnscoredNodes(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 1, RelevanceScore: 0.9},
	}}}
	nodes := []*interfaces.KnSearchNode{rerankNode("a", 1.0), rerankNode("b", 0.9), rerankNode("c", 0.8)}

	got, _ := rerankService(client).rerankInstances(context.Background(), "q", nodes, rerankConfig(InstanceRerankModeOn), nil)

	if len(got) != 3 {
		t.Fatalf("expected all nodes kept, got %d", len(got))
	}
	if got[0].InstanceName != "b" {
		t.Errorf("the only scored node must lead, got %s", got[0].InstanceName)
	}
}

// Out-of-order returns are aligned by index, not in return order - both behaviors of manufacturers exist.
func TestRerankInstances_AlignsByIndexNotOrder(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 2, RelevanceScore: 0.30},
		{Index: 0, RelevanceScore: 0.95},
		{Index: 1, RelevanceScore: 0.60},
	}}}
	nodes := []*interfaces.KnSearchNode{rerankNode("a", 0.5), rerankNode("b", 0.5), rerankNode("c", 0.5)}

	got, _ := rerankService(client).rerankInstances(context.Background(), "q", nodes, rerankConfig(InstanceRerankModeOn), nil)

	want := []string{"a", "b", "c"}
	for i, name := range want {
		if got[i].InstanceName != name {
			t.Fatalf("expected %v by index alignment, got %s at %d", want, got[i].InstanceName, i)
		}
	}
}

// The tail that exceeds top_n will not be sent to the model and cannot be lost.
func TestRerankInstances_TailBeyondTopNIsKeptUnscored(t *testing.T) {
	client := &mockRerankClient{rerankResp: &interfaces.RerankResp{Results: []interfaces.RerankResult{
		{Index: 0, RelevanceScore: 0.2},
		{Index: 1, RelevanceScore: 0.9},
	}}}
	config := rerankConfig(InstanceRerankModeOn)
	config.RerankTopN = 2
	nodes := []*interfaces.KnSearchNode{rerankNode("a", 1.0), rerankNode("b", 0.9), rerankNode("tail", 0.8)}

	got, _ := rerankService(client).rerankInstances(context.Background(), "q", nodes, config, nil)

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

// Document text: Internal metadata is not included, field name sorting is stable, and long values are truncated by characters.
func TestInstanceRerankDocument(t *testing.T) {
	node := &interfaces.KnSearchNode{
		InstanceName: "马拉卡纳球场",
		Properties: map[string]any{
			"_score":       12.5,
			"_instance_id": "sid-1",
			"city_name":    "里约热内卢",
			"comment":      strings.Repeat("长", 500),
			"capacity":     78838, // non-string, skip.
		},
	}

	doc := instanceRerankDocument(node, 10, nil)

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
	// Fields are sorted by name: the text spelled out twice on the same line must be consistent, otherwise the same query will get different model points.
	if strings.Index(doc, "capacity") > strings.Index(doc, "city_name") && strings.Contains(doc, "capacity") {
		t.Error("fields must be emitted in sorted order")
	}
}

// There is an upper limit on the total length of a single document: the downstream will silently cut it to 4000 characters, and the truncated fields will not participate in scoring.
func TestInstanceRerankDocument_TotalLengthCapped(t *testing.T) {
	props := map[string]any{}
	for i := 0; i < 100; i++ {
		props[string(rune('a'+i%26))+strings.Repeat("x", 3)+string(rune('0'+i/26))] = strings.Repeat("值", 200)
	}
	doc := instanceRerankDocument(&interfaces.KnSearchNode{Properties: props}, 200, nil)

	if len([]rune(doc)) > rerankDocumentCharLimit {
		t.Errorf("document must stay under the cap, got %d runes", len([]rune(doc)))
	}
	if doc == "" {
		t.Error("expected some content within the cap")
	}
}

// Unrecognized patterns fall back to off, which is not an error - misconfiguration should not cause the overall retrieval to fail.
func TestRerankInstances_UnknownModeFallsBackToOff(t *testing.T) {
	client := &mockRerankClient{}
	nodes := []*interfaces.KnSearchNode{rerankNode("a", 1.0), rerankNode("b", 0.9)}

	got, _ := rerankService(client).rerankInstances(context.Background(), "q", nodes, rerankConfig("ON!!"), nil)

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
