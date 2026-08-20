// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func fanoutObjectTypes(n int) []*interfaces.KnSearchObjectType {
	out := make([]*interfaces.KnSearchObjectType, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, &interfaces.KnSearchObjectType{
			ConceptID:   fmt.Sprintf("ot_%02d", i),
			ConceptName: fmt.Sprintf("ot_%02d", i),
			DataProperties: []*interfaces.KnSearchDataProperty{{
				Name:                "name",
				Type:                "text",
				ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeMatch},
			}},
		})
	}
	return out
}

// A slow downstream makes the difference visible: serially, eight object types cost eight round
// trips end to end.
func TestSemanticInstanceRetrieval_QueriesObjectTypesConcurrently(t *testing.T) {
	const delay = 60 * time.Millisecond
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			time.Sleep(delay)
			return rowsToResp([]map[string]any{instanceRow(req.OtID+"-a", 9)}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultRetrievalConfig()
	config.SemanticInstanceRetrieval.ObjectTypeConcurrency = 4

	start := time.Now()
	result, err := svc.semanticInstanceRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, fanoutObjectTypes(8), config)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Nodes) != 8 {
		t.Fatalf("expected one row per object type, got %d", len(result.Nodes))
	}
	// Serial would need 8 delays; with four at a time, two waves are enough.
	if elapsed > 5*delay {
		t.Errorf("object types still look serial: %v for 8 types at %v each", elapsed, delay)
	}
}

// Never more in flight than the bound: the fan-out lands on ontology-query, OpenSearch and the
// embedding model, none of which should meet twenty simultaneous conversations from one query.
func TestSemanticInstanceRetrieval_ConcurrencyIsBounded(t *testing.T) {
	var mu sync.Mutex
	var inFlight, peak int
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			return rowsToResp([]map[string]any{instanceRow(req.OtID, 9)}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultRetrievalConfig()
	config.SemanticInstanceRetrieval.ObjectTypeConcurrency = 3

	if _, err := svc.semanticInstanceRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, fanoutObjectTypes(12), config); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > 3 {
		t.Errorf("expected at most 3 object types in flight, saw %d", peak)
	}
}

// Whoever answers first must not decide the order of the response.
func TestSemanticInstanceRetrieval_OrderIsIndependentOfCompletion(t *testing.T) {
	var calls int32
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			// The first object type answers last.
			if atomic.AddInt32(&calls, 1) == 1 {
				time.Sleep(80 * time.Millisecond)
			}
			return rowsToResp([]map[string]any{instanceRow(req.OtID+"-row", 9)}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultRetrievalConfig()

	result, err := svc.semanticInstanceRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, fanoutObjectTypes(4), config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Nodes) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(result.Nodes))
	}
	if got := result.Nodes[0].InstanceName; got != "ot_00-row" {
		t.Errorf("response order followed completion order: first row is %q", got)
	}
}

// One object type failing keeps the rest.
func TestSemanticInstanceRetrieval_FailureIsIsolated(t *testing.T) {
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			if req.OtID == "ot_01" {
				return nil, fmt.Errorf("index missing")
			}
			return rowsToResp([]map[string]any{instanceRow(req.OtID, 9)}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}

	result, err := svc.semanticInstanceRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, fanoutObjectTypes(4), DefaultRetrievalConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Nodes) != 3 {
		t.Fatalf("expected the three healthy object types to survive, got %d rows", len(result.Nodes))
	}
}

// max_object_types is what the caller pays for: concept recall hands over more so its schema has no
// dangling relation endpoints, and instance recall must not inherit that.
func TestSemanticInstanceRetrieval_RespectsMaxObjectTypes(t *testing.T) {
	mockQuery := &mockOntologyQuery{
		instancesFunc: func(req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
			return rowsToResp([]map[string]any{instanceRow(req.OtID, 9)}), nil
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: mockQuery}
	config := DefaultRetrievalConfig()
	config.ConceptRetrieval.TopK = 5

	// Concept recall selected twice the requested number, the way endpoint completion does.
	result, err := svc.semanticInstanceRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, fanoutObjectTypes(10), config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Nodes) != 5 {
		t.Fatalf("expected instance recall to stop at 5 object types, got %d rows", len(result.Nodes))
	}
	if calls := mockQuery.calls(); calls != 5 {
		t.Errorf("expected 5 downstream queries, got %d", calls)
	}
}
