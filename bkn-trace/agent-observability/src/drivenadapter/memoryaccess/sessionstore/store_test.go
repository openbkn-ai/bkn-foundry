package sessionstore

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestListFirstOperationSourceModulesByTraceIDsReturnsEarliestNonEmptyModule(t *testing.T) {
	store := New()
	firstAt := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(time.Second)
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op-a-late", Attempt: 1, TraceID: "trace-a", SourceModule: "later", StartedAt: secondAt})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op-a-empty", Attempt: 1, TraceID: "trace-a", StartedAt: firstAt})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op-a-early", Attempt: 1, TraceID: "trace-a", SourceModule: "earliest", StartedAt: firstAt.Add(time.Millisecond)})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op-b", Attempt: 1, TraceID: "trace-b", SourceModule: "module-b", StartedAt: firstAt})
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "op-other", Attempt: 1, TraceID: "trace-other", SourceModule: "other", StartedAt: firstAt})
		return nil
	}); err != nil {
		t.Fatalf("seed operation facts: %v", err)
	}

	var sourceModules map[string]string
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		sourceModules = tx.ListFirstOperationSourceModulesByTraceIDs([]string{"trace-b", "", "trace-a", "trace-a"})
		return nil
	}); err != nil {
		t.Fatalf("read operation source modules: %v", err)
	}

	if got, want := sourceModules["trace-a"], "earliest"; got != want {
		t.Fatalf("trace-a source module = %q, want %q", got, want)
	}
	if got, want := sourceModules["trace-b"], "module-b"; got != want {
		t.Fatalf("trace-b source module = %q, want %q", got, want)
	}
	if _, found := sourceModules["trace-other"]; found {
		t.Fatalf("unexpected source module for unrequested trace")
	}
}

func TestListInteractionPageUsesNewestOrdinalAndReportsHasMore(t *testing.T) {
	store := New()
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		for ordinal, interactionID := range []string{"first", "second", "third", "fourth"} {
			tx.SaveInteraction(sessionvo.Interaction{
				ID:             interactionID,
				ConversationID: "conversation-page",
				Ordinal:        uint64(ordinal + 1),
			})
		}
		return nil
	}); err != nil {
		t.Fatalf("seed interactions: %v", err)
	}

	var first, second isessionstore.InteractionPage
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		first = tx.ListInteractionPage(isessionstore.InteractionPageQuery{
			ConversationID: "conversation-page", Limit: 2,
		})
		second = tx.ListInteractionPage(isessionstore.InteractionPageQuery{
			ConversationID: "conversation-page", Limit: 2, AfterOrdinal: first.Entries[len(first.Entries)-1].Ordinal,
		})
		return nil
	}); err != nil {
		t.Fatalf("read interaction pages: %v", err)
	}
	if first.Total != 4 || !first.HasMore || len(first.Entries) != 2 || first.Entries[0].ID != "fourth" || first.Entries[1].ID != "third" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	if second.Total != 4 || second.HasMore || len(second.Entries) != 2 || second.Entries[0].ID != "second" || second.Entries[1].ID != "first" {
		t.Fatalf("unexpected final page: %+v", second)
	}
}
