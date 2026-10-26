// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// The existing fixture creates and drops a fresh database on an explicitly
// approved loopback-only MariaDB admin DSN. No business database is used.
func TestDerivedEvidenceAtomicWithRevisionMariaDB(t *testing.T) {
	db := durableAnswerDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := New(db)
	service, owner, interaction, _, _ := durableAnswerSession(t, ctx, db)
	input, err := sessionvo.InlineJSONPayload(json.RawMessage(`{"check":true}`))
	if err != nil {
		t.Fatal(err)
	}
	operation, receipt, err := service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{Owner: owner, ConversationID: interaction.ConversationID, InteractionID: interaction.ID, OperationKey: "quality", ToolName: "quality_check", Input: input, Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch})
	if err != nil {
		t.Fatal(err)
	}
	event := derivedClaimEvent()
	event.Owner = owner
	event.ConversationID = interaction.ConversationID
	event.InteractionID = interaction.ID
	event.OperationID = operation.ID
	event.Attempt = operation.Attempt
	event.EvidenceRefs[0].SourceInteractionID = interaction.ID
	event.EvidenceRefs[0].SourceOperationID = operation.ID
	event.Claims[0].Supports[0].SourceInteractionID = interaction.ID
	event.Claims[0].Supports[0].SourceOperationID = operation.ID
	revision := sessionvo.AssemblyRevision{ID: "rev", RevisionNo: 1, InteractionID: interaction.ID, CompletionManifestVersion: "3.0.0", IncludedReceiptIDs: []string{receipt.ID}, IncludedEventIDs: []string{event.EventID}, ArtifactManifestHash: event.PayloadHash, Completeness: sessionvo.EvidenceComplete, Trigger: "completion", CreatedAt: time.Now().UTC()}
	appendEvent := func(tx isessionstore.Transaction, e ledgervo.Event) (ledgervo.DurableAck, error) {
		return tx.(isessionstore.DerivedEvidenceTransaction).AppendDerivedEvidence(e)
	}
	count := func(table, column, value string) int {
		t.Helper()
		return durableAnswerCount(t, db, "SELECT COUNT(*) FROM "+table+" WHERE "+column+"=?", value)
	}
	t.Run("later failure rolls back event revision and projection", func(t *testing.T) {
		injected := errors.New("injected seal failure")
		err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			if _, err := appendEvent(tx, event); err != nil {
				return err
			}
			tx.SaveAssemblyRevision(revision)
			return injected
		})
		if !errors.Is(err, injected) {
			t.Fatal(err)
		}
		if count("bkn_trace_evidence_event_ledger", "event_id", event.EventID) != 0 || count("bkn_trace_projection_outbox", "event_id", event.EventID) != 0 || count("bkn_trace_assembly_revisions", "revision_id", revision.ID) != 0 {
			t.Fatal("rollback leaked derived evidence")
		}
	})
	t.Run("successful seal and exact replay", func(t *testing.T) {
		err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			ack, err := appendEvent(tx, event)
			if err != nil {
				return err
			}
			if !ack.Durable || ack.Replayed {
				return errors.New("unexpected first ack")
			}
			tx.SaveAssemblyRevision(revision)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		err = store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			ack, err := appendEvent(tx, event)
			if err == nil && !ack.Replayed {
				return errors.New("replay was not detected")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if count("bkn_trace_evidence_event_ledger", "event_id", event.EventID) != 1 || count("bkn_trace_projection_outbox", "event_id", event.EventID) != 1 || count("bkn_trace_assembly_revisions", "revision_id", revision.ID) != 1 {
			t.Fatal("seal or replay was not atomic and unique")
		}
	})
	t.Run("summary refresh after fact update", func(t *testing.T) {
		err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			before := tx.ListOperationCallSummaries(interaction.ID)
			if len(before) != 1 {
				return errors.New("missing initial summary")
			}
			fact, found := tx.FindOperationCallFact(operation.ID, operation.Attempt)
			if !found {
				return errors.New("missing fact")
			}
			fact.Status = sessionvo.AttemptCompleted
			tx.SaveOperationCallFact(fact)
			after := tx.ListOperationCallSummaries(interaction.ID)
			if len(after) != 1 || after[0].Status != sessionvo.AttemptCompleted {
				return errors.New("stale summary after write")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("same transaction causal metadata includes earlier inserts", func(t *testing.T) {
		first, second := event, event
		first.EventID, first.ProducerStreamID, first.CausationEventIDs = "evt-cache-a", "cache-a", []string{"evt-cache-b"}
		second.EventID, second.ProducerStreamID, second.CausationEventIDs = "evt-cache-b", "cache-b", []string{first.EventID}
		err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			if _, err := appendEvent(tx, first); err != nil {
				return err
			}
			_, err := appendEvent(tx, second)
			return err
		})
		if !errors.Is(err, ievidenceledger.ErrCausalityConflict) {
			t.Fatalf("cached graph missed cycle: %v", err)
		}
		if count("bkn_trace_evidence_event_ledger", "event_id", first.EventID) != 0 || count("bkn_trace_evidence_event_ledger", "event_id", second.EventID) != 0 {
			t.Fatal("failed batch leaked a Claim")
		}
	})
	t.Run("identity hash sequence and attempt conflicts", func(t *testing.T) {
		tests := []struct {
			name   string
			change func(*ledgervo.Event)
			want   error
		}{
			{"payload", func(e *ledgervo.Event) {
				e.Envelope = json.RawMessage(`{"source":"different"}`)
				e.PayloadHash = ledgervo.CanonicalPayloadHash(e.Envelope)
			}, ievidenceledger.ErrPayloadConflict},
			{"owner", func(e *ledgervo.Event) { e.EventID = "evt-other-owner"; e.Owner.EffectiveSubjectID = "other" }, ievidenceledger.ErrOwnerMismatch},
			{"sequence", func(e *ledgervo.Event) { e.EventID = "evt-sequence" }, ievidenceledger.ErrSequenceConflict},
			{"attempt", func(e *ledgervo.Event) { e.EventID = "evt-attempt"; e.Attempt = 999 }, ievidenceledger.ErrMissingScope},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				e := event
				test.change(&e)
				err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { _, err := appendEvent(tx, e); return err })
				if !errors.Is(err, test.want) {
					t.Fatalf("got %v want %v", err, test.want)
				}
			})
		}
	})
	t.Run("causality cycle rejected without reading output bodies", func(t *testing.T) {
		source := event
		source.EventID = "evt-source"
		source.ProducerID = "agent"
		source.ProducerStreamID = "source-stream"
		source.EventType = "retrieval.completed"
		source.Claims = nil
		source.EvidenceRefs = nil
		source.CausationEventIDs = []string{"evt-cycle"}
		if _, err := store.Commit(ctx, source); err != nil {
			t.Fatal(err)
		}
		e := event
		e.EventID = "evt-cycle"
		e.ProducerStreamID = "cycle-stream"
		e.CausationEventIDs = []string{source.EventID}
		err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { _, err := appendEvent(tx, e); return err })
		if !errors.Is(err, ievidenceledger.ErrCausalityConflict) {
			t.Fatal(err)
		}
		if count("bkn_trace_evidence_event_ledger", "event_id", e.EventID) != 0 {
			t.Fatal("cyclic event persisted")
		}
	})
	t.Run("projection failure poisons transaction even if caller ignores it", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `CREATE TRIGGER reject_derived_projection BEFORE INSERT ON bkn_trace_projection_outbox FOR EACH ROW BEGIN IF NEW.event_type = 'evidence.project' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'injected derived projection failure'; END IF; END`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := db.ExecContext(context.Background(), `DROP TRIGGER reject_derived_projection`); err != nil {
				t.Error(err)
			}
		}()
		e := event
		e.EventID = "evt-projection-failed"
		e.ProducerStreamID = "failed-stream"
		err = store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { _, _ = appendEvent(tx, e); return nil })
		if err == nil {
			t.Fatal("projection write failure did not roll back outer transaction")
		}
		if count("bkn_trace_evidence_event_ledger", "event_id", e.EventID) != 0 || count("bkn_trace_projection_outbox", "event_id", e.EventID) != 0 {
			t.Fatal("projection failure leaked event")
		}
	})
}
