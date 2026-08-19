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

func weightNode(name string) *interfaces.KnSearchNode {
	return &interfaces.KnSearchNode{
		ObjectTypeID:     "ot1",
		InstanceName:     name,
		UniqueIdentities: map[string]any{"id": name},
	}
}

func weightedConfig(w float64) *interfaces.KnSearchSemanticInstanceRetrievalConfig {
	config := DefaultSemanticInstanceRetrievalConfig()
	config.KnnWeight = float64Ptr(w)
	return config
}

// One road on each side, each ranked 1st, is the cleanest shape for observing weights.
func fuseTwoChannels(t *testing.T, w float64) (knnFirst, matchFirst float64) {
	t.Helper()
	knn := channelOutcome{name: channelKnn, scored: true,
		nodes: []*interfaces.KnSearchNode{weightNode("v1")}}
	match := channelOutcome{name: channelMatch, scored: true,
		nodes: []*interfaces.KnSearchNode{weightNode("m1")}}

	fused := fuseByRRF([]channelOutcome{knn, match}, 60, channelWeights(weightedConfig(w)))
	if len(fused) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(fused))
	}
	byName := map[string]float64{}
	for _, n := range fused {
		byName[n.InstanceName] = n.Score
	}
	return byName["v1"], byName["m1"]
}

// The default of 0.5 must be bit-by-bit identical to "no weight", otherwise this feature is a silent behavior change.
func TestChannelWeights_DefaultIsIdenticalToUnweighted(t *testing.T) {
	knnFirst, matchFirst := fuseTwoChannels(t, 0.5)

	if math.Abs(knnFirst-1.0) > 1e-9 || math.Abs(matchFirst-1.0) > 1e-9 {
		t.Errorf("equal weights must keep the 1.0 anchor, got knn=%.6f match=%.6f", knnFirst, matchFirst)
	}

	// Hitting the same one in both lanes is still 2.0.
	node := weightNode("same")
	both := fuseByRRF([]channelOutcome{
		{name: channelKnn, scored: true, nodes: []*interfaces.KnSearchNode{node}},
		{name: channelMatch, scored: true, nodes: []*interfaces.KnSearchNode{node}},
	}, 60, channelWeights(DefaultSemanticInstanceRetrievalConfig()))
	if len(both) != 1 || math.Abs(both[0].Score-2.0) > 1e-9 {
		t.Errorf("a hit in both channels must still score 2.0, got %+v", both)
	}
}

// The weight directly determines the relative ranking of the two No. 1s. This is the purpose of this knob.
func TestChannelWeights_ShiftTheBalance(t *testing.T) {
	cases := []struct {
		w         float64
		knnFirst  float64
		matchFrst float64
	}{
		{0.5, 1.0, 1.0},
		{0.8, 1.6, 0.4},
		{0.2, 0.4, 1.6},
		{1.0, 2.0, 0.0}, // Trust vectors only: hits unique to the full text no longer contribute points.
		{0.0, 0.0, 2.0},
	}
	for _, c := range cases {
		knnFirst, matchFirst := fuseTwoChannels(t, c.w)
		if math.Abs(knnFirst-c.knnFirst) > 1e-9 || math.Abs(matchFirst-c.matchFrst) > 1e-9 {
			t.Errorf("knn_weight=%.1f: expected knn=%.2f match=%.2f, got knn=%.6f match=%.6f",
				c.w, c.knnFirst, c.matchFrst, knnFirst, matchFirst)
		}
	}
}

// Extreme weighting cannot throw away the results of the other path - only push them to the end without deleting them.
func TestChannelWeights_ExtremeKeepsAllResults(t *testing.T) {
	knn := channelOutcome{name: channelKnn, scored: true,
		nodes: []*interfaces.KnSearchNode{weightNode("v1")}}
	match := channelOutcome{name: channelMatch, scored: true,
		nodes: []*interfaces.KnSearchNode{weightNode("m1"), weightNode("m2")}}

	fused := fuseByRRF([]channelOutcome{knn, match}, 60, channelWeights(weightedConfig(1.0)))
	if len(fused) != 3 {
		t.Fatalf("weight 1.0 must not drop the other channel's rows, got %d", len(fused))
	}
	sortNodesByScore(fused)
	if fused[0].InstanceName != "v1" {
		t.Errorf("the vector hit must lead at weight 1.0, got %s", fused[0].InstanceName)
	}
}

// Mismatching one number should not cause the entire retrieval chain to fail - clamp it and not report an error.
func TestChannelWeights_OutOfRangeIsClamped(t *testing.T) {
	for _, w := range []float64{-1, 2, 100} {
		weights := channelWeights(weightedConfig(w))
		knn, match := weights[channelKnn], weights[channelMatch]
		if knn < 0 || knn > 1 || match < 0 || match > 1 {
			t.Errorf("weight %.1f must be clamped into [0,1], got knn=%.2f match=%.2f", w, knn, match)
		}
		if math.Abs(knn+match-1.0) > 1e-9 {
			t.Errorf("weights must still sum to 1, got %.2f + %.2f", knn, match)
		}
	}
}

// Unregistered channels are treated with equal weight: when adding a third channel in the future, you will not get 0 points because you forget to assign weights.
func TestWeightOf_UnknownChannelFallsBackToEqual(t *testing.T) {
	if got := weightOf(channelWeights(DefaultSemanticInstanceRetrievalConfig()), "future_channel"); got != 0.5 {
		t.Errorf("an unregistered channel must fall back to 0.5, got %.2f", got)
	}
}

// Bias will skew anchors across object types - this is a known cost, not a bug. Nail it here,
// This prevents someone from "fixing" it as a bug in the future and destroying the caller's declared preferences.
func TestChannelWeights_KnownCrossTypeSkew(t *testing.T) {
	// Object classes with vector fields: sent both ways.
	dual := fuseByRRF([]channelOutcome{
		{name: channelKnn, scored: true, nodes: []*interfaces.KnSearchNode{weightNode("dual-v")}},
		{name: channelMatch, scored: true, nodes: []*interfaces.KnSearchNode{weightNode("dual-m")}},
	}, 60, channelWeights(weightedConfig(0.8)))

	// Object class without vector field: only full text path.
	single := fuseByRRF([]channelOutcome{
		{name: channelMatch, scored: true, nodes: []*interfaces.KnSearchNode{weightNode("single-m")}},
	}, 60, channelWeights(weightedConfig(0.8)))

	var dualKnnFirst float64
	for _, n := range dual {
		if n.InstanceName == "dual-v" {
			dualKnnFirst = n.Score
		}
	}
	if dualKnnFirst <= single[0].Score {
		t.Errorf("at knn_weight=0.8 the vector-backed type is expected to rank higher: %.2f vs %.2f",
			dualKnnFirst, single[0].Score)
	}
	// Anchors remain consistent when weighted equally - tilt is only brought about by weights.
	dualEqual := fuseByRRF([]channelOutcome{
		{name: channelKnn, scored: true, nodes: []*interfaces.KnSearchNode{weightNode("d2")}},
	}, 60, channelWeights(DefaultSemanticInstanceRetrievalConfig()))
	singleEqual := fuseByRRF([]channelOutcome{
		{name: channelMatch, scored: true, nodes: []*interfaces.KnSearchNode{weightNode("s2")}},
	}, 60, channelWeights(DefaultSemanticInstanceRetrievalConfig()))
	if math.Abs(dualEqual[0].Score-singleEqual[0].Score) > 1e-9 {
		t.Errorf("with equal weights the anchor must stay identical across channels: %.6f vs %.6f",
			dualEqual[0].Score, singleEqual[0].Score)
	}
}
