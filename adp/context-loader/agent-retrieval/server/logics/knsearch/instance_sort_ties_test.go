// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func sortedNames(nodes []*interfaces.KnSearchNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.InstanceName)
	}
	return out
}

// Ties across object types are the common case: first place in one channel is exactly 1.0 for every
// object type. Breaking those ties by the raw recall score would order them by BM25 magnitude, which
// says nothing about relevance and drifts with corpus and document length.
func TestSortNodesByScore_CrossTypeTiesIgnoreRawScores(t *testing.T) {
	nodes := []*interfaces.KnSearchNode{
		{ObjectTypeID: "term_alias", InstanceName: "alias", Score: 1, RecallScore: 3.99, BM25Score: 3.99},
		{ObjectTypeID: "term", InstanceName: "term", Score: 1, RecallScore: 14.70, BM25Score: 14.70},
		{ObjectTypeID: "fact", InstanceName: "fact", Score: 1, KnnScore: 0.72, RecallScore: 0.72},
	}

	sortNodesByScore(nodes)

	// Recall order preserved: the object type concept recall ranked first still comes first.
	want := []string{"alias", "term", "fact"}
	for i, name := range sortedNames(nodes) {
		if name != want[i] {
			t.Fatalf("cross-type tie was reordered by a raw score: got %v, want %v", sortedNames(nodes), want)
		}
	}
}

// Inside one object type the raw score is comparable — same index, same query, same operator — so it
// still decides.
func TestSortNodesByScore_SameTypeTiesUseRecallScore(t *testing.T) {
	nodes := []*interfaces.KnSearchNode{
		{ObjectTypeID: "term", InstanceName: "weaker", Score: 1, RecallScore: 4.1},
		{ObjectTypeID: "term", InstanceName: "stronger", Score: 1, RecallScore: 14.7},
	}

	sortNodesByScore(nodes)

	if got := sortedNames(nodes); got[0] != "stronger" {
		t.Fatalf("same-type tie ignored the raw recall score: %v", got)
	}
}

// The fused score still decides whenever it differs, across object types included.
func TestSortNodesByScore_ScoreStillWins(t *testing.T) {
	nodes := []*interfaces.KnSearchNode{
		{ObjectTypeID: "term", InstanceName: "single-channel", Score: 1, RecallScore: 14.7},
		{ObjectTypeID: "qa", InstanceName: "both-channels", Score: 2, RecallScore: 0.4},
	}

	sortNodesByScore(nodes)

	if got := sortedNames(nodes); got[0] != "both-channels" {
		t.Fatalf("expected the higher fused score first: %v", got)
	}
}
