// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionsvc_test

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type readOnlyOwnerStore struct {
	isessionstore.Store
	t     *testing.T
	peeks int
}

func (s *readOnlyOwnerStore) WithinTransaction(ctx context.Context, fn func(isessionstore.Transaction) error) error {
	return s.Store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		return fn(readOnlyOwnerTransaction{Transaction: tx, store: s})
	})
}

type readOnlyOwnerTransaction struct {
	isessionstore.Transaction
	store *readOnlyOwnerStore
}

func (tx readOnlyOwnerTransaction) FindConversation(string) (sessionvo.Conversation, bool) {
	tx.store.t.Error("lifecycle read must not acquire the conversation write lock")
	return sessionvo.Conversation{}, false
}

func (tx readOnlyOwnerTransaction) PeekConversation(id string) (sessionvo.Conversation, bool) {
	tx.store.peeks++
	return tx.Transaction.PeekConversation(id)
}

func TestLifecycleReadsAuthorizeWithoutConversationWriteLock(t *testing.T) {
	base, _, owner, conversation, interaction, operation, receipt := mustCreateOperationWithStore(t)
	store := &readOnlyOwnerStore{Store: base, t: t}
	service := sessionsvc.New(store, sessionsvc.Options{})
	readers := []struct {
		name string
		read func(sessionvo.Owner) error
	}{
		{"conversation", func(o sessionvo.Owner) error {
			_, err := service.GetConversation(context.Background(), o, conversation.ID)
			return err
		}},
		{"interaction", func(o sessionvo.Owner) error {
			_, err := service.GetInteraction(context.Background(), o, interaction.ID)
			return err
		}},
		{"operation", func(o sessionvo.Owner) error {
			_, err := service.GetOperation(context.Background(), o, operation.ID)
			return err
		}},
		{"receipt", func(o sessionvo.Owner) error {
			_, err := service.GetReceipt(context.Background(), o, receipt.ID)
			return err
		}},
		{"call_facts", func(o sessionvo.Owner) error {
			_, err := service.ListOperationCallFacts(context.Background(), o, interaction.ID)
			return err
		}},
		{"call_summaries", func(o sessionvo.Owner) error {
			_, err := service.ListOperationCallSummaries(context.Background(), o, interaction.ID)
			return err
		}},
		{"call_fact", func(o sessionvo.Owner) error {
			_, err := service.GetOperationCallFact(context.Background(), o, operation.ID, operation.Attempt)
			return err
		}},
		{"assembly_revisions", func(o sessionvo.Owner) error {
			_, err := service.ListAssemblyRevisions(context.Background(), o, interaction.ID)
			return err
		}},
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			store.t = t
			before := store.peeks
			if err := reader.read(owner); err != nil {
				t.Fatalf("authorized read: %v", err)
			}
			if store.peeks != before+1 {
				t.Fatal("owner must be checked using the non-locking conversation lookup")
			}
			owners := []sessionvo.Owner{owner, owner, owner, owner}
			owners[0].ApplicationPrincipalID += "-other"
			owners[1].EffectiveSubjectType = sessionvo.SubjectService
			owners[2].EffectiveSubjectID += "-other"
			owners[3].DelegationID += "-other"
			for _, other := range owners {
				if err := reader.read(other); !sessionsvc.IsCode(err, sessionsvc.CodeResourceNotDisclosed) {
					t.Fatalf("owner mismatch must remain undisclosed: %v", err)
				}
			}
		})
	}
}
