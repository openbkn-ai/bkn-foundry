// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ledgersvc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
)

func attachDurableObserver(t *testing.T, service *ledgersvc.Service, callback func(context.Context, ledgervo.Event) error) {
	t.Helper()
	binder, ok := any(service).(interface {
		SetDurableObserver(func(context.Context, ledgervo.Event) error)
	})
	if !ok {
		t.Fatal("durable Ledger decisions lack the receipt confirmation observer")
	}
	binder.SetDurableObserver(callback)
}
func TestDurableObserverRunsAfterCommitAndOnHTTPReplay(t *testing.T) {
	store := ledgerstore.New()
	service := ledgersvc.New(store)
	calls := 0
	temporary := errors.New("confirmation database unavailable")
	attachDurableObserver(t, service, func(ctx context.Context, event ledgervo.Event) error {
		calls++
		stored, err := store.ListInteractionEvents(ctx, event.Owner, event.InteractionID)
		if err != nil || len(stored) != 1 {
			t.Fatalf("observer ran before Ledger commit: %v %v", stored, err)
		}
		if calls == 1 {
			return temporary
		}
		return nil
	})
	if _, err := service.Ingest(context.Background(), testEvent()); !errors.Is(err, temporary) {
		t.Fatalf("observer error must remain retryable: %v", err)
	}
	ack, err := service.Ingest(context.Background(), testEvent())
	if err != nil || !ack.Durable || !ack.Replayed || calls != 2 {
		t.Fatalf("durable dedup did not retry observer: %#v %v calls=%d", ack, err, calls)
	}
}
func TestDurableObserverRunsOnKafkaAcceptedAndDeduplicated(t *testing.T) {
	for _, decision := range []string{"accepted", "deduplicated", "conflict"} {
		t.Run(decision, func(t *testing.T) {
			store := &kafkaLedgerStore{result: ievidenceledger.KafkaResult{Ack: ledgervo.DurableAck{Durable: true}}}
			switch decision {
			case "accepted":
				store.result.Decision = ievidenceledger.KafkaAccepted
			case "deduplicated":
				store.result.Decision = ievidenceledger.KafkaDeduplicated
			case "conflict":
				store.result.Decision = ievidenceledger.KafkaConflict
			}
			service := ledgersvc.New(store)
			calls := 0
			temporary := errors.New("confirmation write unavailable")
			attachDurableObserver(t, service, func(context.Context, ledgervo.Event) error { calls++; return temporary })
			_, err := service.IngestKafka(context.Background(), testEvent(), ievidenceledger.KafkaCoordinate{Topic: "openbkn.evidence.v1", Partition: 0, Offset: 1})
			if decision == "conflict" {
				if calls != 0 || err != nil {
					t.Fatalf("conflict ran confirmation: %d %v", calls, err)
				}
				return
			}
			if calls != 1 || !errors.Is(err, temporary) || ledgersvc.IsCode(err, ledgersvc.CodeInvalidEvent) {
				t.Fatalf("temporary confirmation failed to retry: calls=%d err=%v", calls, err)
			}
		})
	}
}
