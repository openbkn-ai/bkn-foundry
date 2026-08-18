package sessionstore

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestListOperationCallFactsByTraceIDsReturnsRequestedFactsInTraceOrder(t *testing.T) {
	store := New()
	firstAt := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(time.Second)
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op-a-late", Attempt: 1, TraceID: "trace-a", StartedAt: secondAt})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op-a-early", Attempt: 1, TraceID: "trace-a", StartedAt: firstAt})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op-b", Attempt: 1, TraceID: "trace-b", StartedAt: firstAt})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op-other", Attempt: 1, TraceID: "trace-other", StartedAt: firstAt})
		return nil
	}); err != nil {
		t.Fatalf("seed operation facts: %v", err)
	}

	var facts []sessionvo.OperationCallFact
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		facts = tx.ListOperationCallFactsByTraceIDs([]string{"trace-b", "", "trace-a", "trace-a"})
		return nil
	}); err != nil {
		t.Fatalf("read operation facts: %v", err)
	}

	if len(facts) != 3 {
		t.Fatalf("facts = %+v, want only three requested facts", facts)
	}
	byTrace := map[string][]sessionvo.OperationCallFact{}
	for _, fact := range facts {
		byTrace[fact.TraceID] = append(byTrace[fact.TraceID], fact)
	}
	if len(byTrace["trace-a"]) != 2 || byTrace["trace-a"][0].OperationID != "op-a-early" ||
		byTrace["trace-a"][1].OperationID != "op-a-late" {
		t.Fatalf("trace-a facts must remain chronological: %+v", byTrace["trace-a"])
	}
	if len(byTrace["trace-b"]) != 1 || byTrace["trace-b"][0].OperationID != "op-b" {
		t.Fatalf("trace-b facts = %+v", byTrace["trace-b"])
	}
}
