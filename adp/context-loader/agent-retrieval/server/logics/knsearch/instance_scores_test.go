// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"math"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Both channels' raw scores have to survive fusion under their own names. Keeping only the larger
// one drops the vector evidence every time BM25 also hit, because BM25 is unbounded and cosine
// similarity is 0~1.
func TestFuseByRRF_KeepsBothChannelScores(t *testing.T) {
	outcomes := []channelOutcome{
		{
			name:   channelMatch,
			scored: true,
			nodes: []*interfaces.KnSearchNode{
				{InstanceName: "青岛啤酒", RecallScore: 31.8, BM25Score: 31.8},
			},
		},
		{
			name:   channelKnn,
			scored: true,
			nodes: []*interfaces.KnSearchNode{
				{InstanceName: "青岛啤酒", RecallScore: 0.58, KnnScore: 0.58},
			},
		},
	}

	fused := fuseByRRF(outcomes, 60, map[string]float64{channelKnn: 0.5, channelMatch: 0.5})
	if len(fused) != 1 {
		t.Fatalf("expected the two channels to merge into one row, got %d", len(fused))
	}
	node := fused[0]
	if math.Abs(node.BM25Score-31.8) > 1e-9 {
		t.Errorf("bm25_score lost: %v", node.BM25Score)
	}
	if math.Abs(node.KnnScore-0.58) > 1e-9 {
		t.Errorf("knn_score lost: %v", node.KnnScore)
	}
	// First place in both channels is the 2.0 anchor.
	if math.Abs(node.RRFScore-2.0) > 1e-9 {
		t.Errorf("expected rrf_score 2.0, got %v", node.RRFScore)
	}
	if math.Abs(node.RRFScore-node.Score) > 1e-9 {
		t.Errorf("score must stay an alias of rrf_score: %v vs %v", node.Score, node.RRFScore)
	}
}

// The exact shape a caller reported: first in one channel, third in the other.
func TestFuseByRRF_ScoreDecodesToChannelRanks(t *testing.T) {
	match := []*interfaces.KnSearchNode{
		{InstanceName: "青岛啤酒", BM25Score: 3.29, RecallScore: 3.29},
	}
	knn := []*interfaces.KnSearchNode{
		{InstanceName: "other-a"}, {InstanceName: "other-b"},
		{InstanceName: "青岛啤酒", KnnScore: 0.71, RecallScore: 0.71},
	}
	fused := fuseByRRF(
		[]channelOutcome{{name: channelMatch, scored: true, nodes: match}, {name: channelKnn, scored: true, nodes: knn}},
		60, map[string]float64{channelKnn: 0.5, channelMatch: 0.5})

	var target *interfaces.KnSearchNode
	for _, n := range fused {
		if n.InstanceName == "青岛啤酒" {
			target = n
		}
	}
	if target == nil {
		t.Fatal("row disappeared from the fused set")
	}
	// 61*(1/61 + 1/63): rank 0 in one channel, rank 2 in the other.
	want := 61.0 * (1.0/61.0 + 1.0/63.0)
	if math.Abs(target.RRFScore-want) > 1e-9 {
		t.Errorf("expected rrf_score %.12f, got %.12f", want, target.RRFScore)
	}
}

// Rows scored by the local fallback must not claim an rrf_score: the two scales do not line up, and
// a caller comparing 0.85 against a fusion 2.0 would read the wrong conclusion.
func TestScoreNodes_TagsHeuristicScoreOnly(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	config := DefaultSemanticInstanceRetrievalConfig()
	nodes := []*interfaces.KnSearchNode{{InstanceName: "青岛啤酒"}}

	svc.scoreNodes("青岛啤酒", nodes, nil, config)

	if nodes[0].HeuristicScore <= 0 {
		t.Fatalf("expected a heuristic score, got %v", nodes[0].HeuristicScore)
	}
	if nodes[0].RRFScore != 0 {
		t.Errorf("fallback rows must not carry an rrf_score: %v", nodes[0].RRFScore)
	}
	if math.Abs(nodes[0].HeuristicScore-nodes[0].Score) > 1e-9 {
		t.Errorf("score must stay an alias of heuristic_score: %v vs %v", nodes[0].Score, nodes[0].HeuristicScore)
	}
}
