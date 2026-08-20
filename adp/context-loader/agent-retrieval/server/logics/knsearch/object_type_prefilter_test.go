// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func scoredObjectTypes(scores ...float64) []*interfaces.KnSearchObjectType {
	out := make([]*interfaces.KnSearchObjectType, 0, len(scores))
	for i, score := range scores {
		out = append(out, &interfaces.KnSearchObjectType{
			ConceptID:   fmt.Sprintf("ot_%02d", i),
			ConceptName: fmt.Sprintf("ot_%02d", i),
			Score:       score,
			DataProperties: []*interfaces.KnSearchDataProperty{{
				Name:                "name",
				Type:                "text",
				ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeMatch},
			}},
		})
	}
	return out
}

func keptConceptIDs(objectTypes []*interfaces.KnSearchObjectType) []string {
	out := make([]string, 0, len(objectTypes))
	for _, objType := range objectTypes {
		out = append(out, objType.ConceptID)
	}
	return out
}

func TestFilterObjectTypesByScore_DropsTheFarBehind(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	// 9.0 is the best; 0.2 and 0.1 are an order of magnitude behind it.
	kept := svc.filterObjectTypesByScore(context.Background(), scoredObjectTypes(9.0, 4.5, 0.2, 0.1), 0.25)

	if got := keptConceptIDs(kept); len(got) != 2 || got[0] != "ot_00" || got[1] != "ot_01" {
		t.Fatalf("expected the two object types above a quarter of the best, got %v", got)
	}
}

// No scores means concept recall had no signal to give, not that nothing is relevant.
func TestFilterObjectTypesByScore_NoSignalKeepsEverything(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	kept := svc.filterObjectTypesByScore(context.Background(), scoredObjectTypes(0, 0, 0), 0.5)

	if len(kept) != 3 {
		t.Fatalf("expected all three object types, got %v", keptConceptIDs(kept))
	}
}

// The filter may narrow a query. It may not empty one.
func TestFilterObjectTypesByScore_AlwaysKeepsTheBest(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	kept := svc.filterObjectTypesByScore(context.Background(), scoredObjectTypes(0.4, 0.3, 0.2), 10)

	if got := keptConceptIDs(kept); len(got) != 1 || got[0] != "ot_00" {
		t.Fatalf("expected the best-scoring object type to survive any threshold, got %v", got)
	}
}

// Off by default: the ratio has no portable value, so a deployment opts in after looking at the
// spread this logs.
func TestFilterObjectTypesByScore_DisabledByDefault(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	config := DefaultSemanticInstanceRetrievalConfig()
	if config.MinObjectTypeScoreRatio != 0 {
		t.Fatalf("expected the pre-filter off by default, got ratio %v", config.MinObjectTypeScoreRatio)
	}
	kept := svc.filterObjectTypesByScore(context.Background(),
		scoredObjectTypes(9.0, 0.1), config.MinObjectTypeScoreRatio)
	if len(kept) != 2 {
		t.Fatalf("default config dropped object types: %v", keptConceptIDs(kept))
	}
}

// An object type the query has nothing to do with costs a downstream query; the point of the filter
// is that the query is never issued.
func TestSemanticInstanceRetrieval_PreFilterSavesDownstreamQueries(t *testing.T) {
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			return rowsToResp([]map[string]any{instanceRow(req.OtID, 9)}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultRetrievalConfig()
	config.SemanticInstanceRetrieval.MinObjectTypeScoreRatio = 0.25

	if _, err := svc.semanticInstanceRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"},
		scoredObjectTypes(9.0, 4.5, 0.2, 0.1), config); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls := mockQuery.calls(); calls != 2 {
		t.Errorf("expected 2 downstream queries for the 2 surviving object types, got %d", calls)
	}
}

// The repeatable half of the performance claim: downstream request count stays one query per object
// type (single channel here), and wall clock stays flat rather than summing them.
func TestSemanticInstanceRetrieval_CostScalesWithObjectTypes(t *testing.T) {
	const delay = 40 * time.Millisecond
	for _, count := range []int{1, 3, 5, 10} {
		count := count
		t.Run(fmt.Sprintf("object_types=%d", count), func(t *testing.T) {
			mockQuery := &mockOntologyQuery{
				instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
					time.Sleep(delay)
					return rowsToResp([]map[string]any{instanceRow(req.OtID, 9)}), nil
				},
			}
			svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
			config := DefaultRetrievalConfig()
			config.ConceptRetrieval.TopK = count

			start := time.Now()
			result, err := svc.semanticInstanceRetrieval(context.Background(),
				&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"},
				fanoutObjectTypes(count), config)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			t.Logf("object_types=%2d downstream_queries=%2d elapsed=%v", count, mockQuery.calls(), elapsed)

			if got := mockQuery.calls(); got != count {
				t.Errorf("expected one downstream query per object type, got %d for %d types", got, count)
			}
			if len(result.Nodes) != count {
				t.Errorf("expected one row per object type, got %d", len(result.Nodes))
			}
			// With a concurrency of 6, ten object types take two waves — nothing near ten delays.
			waves := (count + DefaultSemanticInstanceRetrievalConfig().ObjectTypeConcurrency - 1) /
				DefaultSemanticInstanceRetrievalConfig().ObjectTypeConcurrency
			if budget := time.Duration(waves+1) * delay; elapsed > budget {
				t.Errorf("elapsed %v exceeds %v for %d object types", elapsed, budget, count)
			}
		})
	}
}

// Zero means concept recall never scored this object type, not that it scored badly. It reaches the
// candidate set unscored in ordinary ways: concept search scores only what it matched, relation
// endpoints are completed in afterwards, and object_types pinned by the caller are applied before
// scoring runs. Dropping those would silently skip an object type the caller named by hand.
func TestFilterObjectTypesByScore_KeepsUnscoredObjectTypes(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	types := scoredObjectTypes(5.0, 0, 0.2)
	types[1].ConceptID = "shipment" // pulled in as a relation endpoint, never scored

	kept := svc.filterObjectTypesByScore(context.Background(), types, 0.3)

	var sawShipment bool
	for _, objType := range kept {
		if objType.ConceptID == "shipment" {
			sawShipment = true
		}
	}
	if !sawShipment {
		t.Fatalf("an unscored object type was dropped as if it had scored low: %v", keptConceptIDs(kept))
	}
	// The genuinely low-scoring one still goes.
	for _, objType := range kept {
		if objType.ConceptID == "ot_02" {
			t.Errorf("expected the scored-but-low object type to be dropped: %v", keptConceptIDs(kept))
		}
	}
}

// End to end: an unscored object type still gets its downstream query.
func TestSemanticInstanceRetrieval_PreFilterQueriesUnscoredObjectTypes(t *testing.T) {
	queried := map[string]bool{}
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			queried[req.OtID] = true
			return rowsToResp([]map[string]any{instanceRow(req.OtID, 9)}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultRetrievalConfig()
	config.SemanticInstanceRetrieval.MinObjectTypeScoreRatio = 0.3

	types := scoredObjectTypes(5.0, 0)
	types[1].ConceptID = "shipment"

	if _, err := svc.semanticInstanceRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, types, config); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !queried["shipment"] {
		t.Fatal("no instance query was issued for the unscored object type")
	}
}
