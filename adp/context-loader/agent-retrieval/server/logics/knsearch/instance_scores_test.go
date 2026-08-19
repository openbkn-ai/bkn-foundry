// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"encoding/json"
	"math"
	"strings"
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
	if math.Abs(node.Score-2.0) > 1e-9 {
		t.Errorf("expected score 2.0, got %v", node.Score)
	}
	if node.HeuristicScore != 0 {
		t.Errorf("index-backed rows must not be marked as heuristically scored: %v", node.HeuristicScore)
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
	if math.Abs(target.Score-want) > 1e-9 {
		t.Errorf("expected score %.12f, got %.12f", want, target.Score)
	}
}

// score carries the fusion scale on the index-backed path and the tier scale on the fallback path.
// heuristic_score is the marker that says which of the two a caller is looking at.
func TestScoreNodes_MarksTheHeuristicScale(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	config := DefaultSemanticInstanceRetrievalConfig()
	nodes := []*interfaces.KnSearchNode{{InstanceName: "青岛啤酒"}}

	svc.scoreNodes("青岛啤酒", nodes, nil, config)

	if nodes[0].HeuristicScore <= 0 {
		t.Fatalf("expected a heuristic score, got %v", nodes[0].HeuristicScore)
	}
	if math.Abs(nodes[0].HeuristicScore-nodes[0].Score) > 1e-9 {
		t.Errorf("score must carry the heuristic value on this path: %v vs %v", nodes[0].Score, nodes[0].HeuristicScore)
	}
}

// recall_score is an internal working value (channel pruning, duplicate merging, tie-breaking) and
// must not reach the caller: it keeps only the larger of the two channels' raw scores, so on the
// wire it would read as a single relevance number while silently being whichever channel won.
func TestKnSearchNode_RecallScoreStaysOffTheWire(t *testing.T) {
	payload, err := json.Marshal(&interfaces.KnSearchNode{
		ObjectTypeID: "brand",
		Rank:         1,
		Score:        1.97,
		RecallScore:  16.02,
		BM25Score:    16.02,
		KnnScore:     0.46,
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	body := string(payload)
	if strings.Contains(body, "recall_score") {
		t.Errorf("recall_score leaked into the response: %s", body)
	}
	for _, field := range []string{`"rank":1`, `"score":1.97`, `"bm25_score":16.02`, `"knn_score":0.46`} {
		if !strings.Contains(body, field) {
			t.Errorf("expected %s in the response, got %s", field, body)
		}
	}
}
