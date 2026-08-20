// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// orderTestObjectTypes returns two object types that both carry an index-backed text field.
func orderTestObjectTypes(ids ...string) []*interfaces.KnSearchObjectType {
	out := make([]*interfaces.KnSearchObjectType, 0, len(ids))
	for _, id := range ids {
		out = append(out, &interfaces.KnSearchObjectType{
			ConceptID:   id,
			ConceptName: id,
			DataProperties: []*interfaces.KnSearchDataProperty{
				{
					Name: "name",
					Type: "text",
					ConditionOperations: []interfaces.KnOperationType{
						interfaces.KnOperationTypeKnn,
						interfaces.KnOperationTypeMatch,
					},
				},
			},
		})
	}
	return out
}

// The object type processed first used to own the top of the list no matter how its rows scored.
// A row that is first in both channels (the 2.0 anchor) has to outrank a single-channel 1.0 from an
// object type that merely happened to be recalled earlier.
func TestSemanticInstanceRetrieval_OrdersAcrossObjectTypes(t *testing.T) {
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			switch req.OtID {
			case "early":
				// Recalled by the full-text channel only: the best it can reach is 1.0.
				if condChannel(req.Cond) == channelKnn {
					return rowsToResp(nil), nil
				}
				return rowsToResp([]map[string]any{instanceRow("early-1", 31.8)}), nil
			default:
				// Top of both channels: the 2.0 anchor.
				return rowsToResp([]map[string]any{instanceRow("late-1", 0.9)}), nil
			}
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultRetrievalConfig()

	result, err := svc.semanticInstanceRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"},
		orderTestObjectTypes("early", "late"), config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Nodes) < 2 {
		t.Fatalf("expected rows from both object types, got %d", len(result.Nodes))
	}
	if result.Nodes[0].InstanceName != "late-1" {
		t.Fatalf("expected the higher-scoring row first, got %q (score=%v) before %q (score=%v)",
			result.Nodes[0].InstanceName, result.Nodes[0].Score,
			result.Nodes[1].InstanceName, result.Nodes[1].Score)
	}
	// The BM25 number is the largest in the response and must not be what decides the order.
	if result.Nodes[1].BM25Score <= result.Nodes[0].BM25Score {
		t.Fatalf("test setup lost its point: the demoted row should carry the larger bm25_score (%v vs %v)",
			result.Nodes[1].BM25Score, result.Nodes[0].BM25Score)
	}
}

// rank is stamped last, so it always agrees with the returned sequence.
func TestSemanticInstanceRetrieval_StampsRank(t *testing.T) {
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			if condChannel(req.Cond) == channelKnn {
				return rowsToResp(nil), nil
			}
			return rowsToResp([]map[string]any{
				instanceRow(req.OtID+"-a", 9),
				instanceRow(req.OtID+"-b", 8),
			}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultRetrievalConfig()

	result, err := svc.semanticInstanceRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"},
		orderTestObjectTypes("ot1", "ot2"), config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected rows")
	}
	for i, node := range result.Nodes {
		if node.Rank != i+1 {
			t.Fatalf("rank %d does not match position %d", node.Rank, i+1)
		}
	}
}
