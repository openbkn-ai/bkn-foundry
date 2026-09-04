// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// gateTestObjectType is an object type with one index-backed text field, enough to reach the
// instance recall path.
func gateTestObjectType(id string) []*interfaces.KnSearchObjectType {
	return []*interfaces.KnSearchObjectType{{
		ConceptID:   id,
		ConceptName: id,
		DataProperties: []*interfaces.KnSearchDataProperty{{
			Name: "name",
			Type: "text",
			ConditionOperations: []interfaces.KnOperationType{
				interfaces.KnOperationTypeKnn,
				interfaces.KnOperationTypeMatch,
			},
		}},
	}}
}

func gatedNodes(scores ...float64) []*interfaces.KnSearchNode {
	out := make([]*interfaces.KnSearchNode, 0, len(scores))
	for i, score := range scores {
		out = append(out, &interfaces.KnSearchNode{
			InstanceName:  string(rune('a' + i)),
			Score:         1,
			RerankerScore: score,
		})
	}
	return out
}

func gateNames(nodes []*interfaces.KnSearchNode) string {
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.InstanceName)
	}
	return strings.Join(names, ",")
}

func TestRelevanceGate_DropsWhatTheModelScoredLow(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	nodes := gatedNodes(0.74, 0.51, 0.12, 0.006)

	kept, message := svc.applyRelevanceGate(context.Background(), nodes, 0.5,
		rerankOutcome{scored: true, top: 0.74})

	if got := gateNames(kept); got != "a,b" {
		t.Fatalf("expected only the rows above the threshold, got %q", got)
	}
	if message != "" {
		t.Errorf("a partial filter is an ordinary result and needs no message: %q", message)
	}
}

// The case the gate exists for: the model looked at every candidate and none of them answers the
// query. Returning the best of a bad batch is what made callers read irrelevant rows as answers.
func TestRelevanceGate_RejectsTheWholeBatch(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	nodes := gatedNodes(0.0644, 0.0061, 0.0060)

	kept, message := svc.applyRelevanceGate(context.Background(), nodes, 0.3,
		rerankOutcome{scored: true, top: 0.0644})

	if len(kept) != 0 {
		t.Fatalf("expected an empty result, got %q", gateNames(kept))
	}
	for _, want := range []string{"0.3000", "0.0644", "min_reranker_score"} {
		if !strings.Contains(message, want) {
			t.Errorf("message should carry %q so the caller can see how far off it was: %q", want, message)
		}
	}
	if strings.Contains(message, "%!") {
		t.Errorf("message was not rendered from the locale catalog: %q", message)
	}
}

// No judgement means no filtering. Silently returning the unfiltered set as if it had passed a
// relevance bar is worse than not having the bar.
func TestRelevanceGate_UnavailableRerankerDoesNotFilter(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	nodes := gatedNodes(0, 0, 0)

	kept, message := svc.applyRelevanceGate(context.Background(), nodes, 0.3,
		rerankOutcome{unavailable: true})

	if len(kept) != len(nodes) {
		t.Fatalf("expected the unfiltered set, got %d of %d", len(kept), len(nodes))
	}
	if !strings.Contains(message, "min_reranker_score") {
		t.Errorf("the caller must be told the gate did not run: %q", message)
	}
}

// A judge that gives every candidate the same score is not judging. Applying a threshold to a
// constant keeps everything or drops everything depending on which side of it the constant fell.
func TestRelevanceGate_DegenerateScoresDoNotFilter(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	nodes := gatedNodes(0.006, 0.006, 0.006)

	kept, message := svc.applyRelevanceGate(context.Background(), nodes, 0.3,
		rerankOutcome{scored: true, degenerate: true, top: 0.006})

	if len(kept) != len(nodes) {
		t.Fatalf("expected the unfiltered set, got %d of %d", len(kept), len(nodes))
	}
	if message == "" {
		t.Error("skipping the gate silently is the failure mode this guards against")
	}
}

// Rows past the rerank window were never judged, so the gate cannot vouch for them.
func TestRelevanceGate_DropsUnjudgedRows(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	nodes := gatedNodes(0.74)
	nodes = append(nodes, &interfaces.KnSearchNode{InstanceName: "tail", Score: 1})

	kept, _ := svc.applyRelevanceGate(context.Background(), nodes, 0.5,
		rerankOutcome{scored: true, top: 0.74})

	if got := gateNames(kept); got != "a" {
		t.Fatalf("expected the unjudged row to be dropped, got %q", got)
	}
}

// A configured threshold has to bring the model with it: search_instance leaves reranking off by
// default, so a deployment that turned the gate on would otherwise get no gate at all.
func TestSemanticInstanceRetrieval_ThresholdEnablesRerank(t *testing.T) {
	client := &mockRerankClient{}
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			if condChannel(req.Cond) == channelKnn {
				return rowsToResp(nil), nil
			}
			return rowsToResp([]map[string]any{instanceRow("a", 9), instanceRow("b", 8)}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery,
		authorizer: allowAllQueryCandidateAuthorizer{}, rerankClient: client}
	config := DefaultRetrievalConfig()
	config.SemanticInstanceRetrieval.MinRerankerScore = 0.5
	if normalizeRerankMode(config.SemanticInstanceRetrieval.InstanceRerankMode) != InstanceRerankModeOff {
		t.Fatal("fixture assumes reranking starts off")
	}

	_, err := svc.semanticInstanceRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"},
		gateTestObjectType("ot1"), config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.callCount() == 0 {
		t.Fatal("the threshold did not bring the reranker with it, so nothing was ever judged")
	}
}
