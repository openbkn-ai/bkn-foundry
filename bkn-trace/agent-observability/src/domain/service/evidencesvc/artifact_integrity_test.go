// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	sessions "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"testing"
)

type artifactInvalidationProbe struct {
	*sessions.Store
	calls   int
	owner   sessionvo.Owner
	failure error
}

func (p *artifactInvalidationProbe) SaveStoredRecordIntegrity(context.Context, string, sessionvo.StoredRecordIntegrity) (bool, error) {
	return false, nil
}
func (p *artifactInvalidationProbe) ListRecordIntegrityCandidates(context.Context, string, int) ([]sessionvo.Interaction, error) {
	return nil, nil
}
func (p *artifactInvalidationProbe) InvalidateRecordIntegrity(_ context.Context, id string, owner sessionvo.Owner) error {
	if id != "interaction_001" {
		return errors.New("wrong interaction")
	}
	p.calls++
	p.owner = owner
	return p.failure
}
func TestArtifactACKDoesNotDependOnDerivedIntegrityMetadata(t *testing.T) {
	for _, account := range []string{"acct_demo", "foreign"} {
		t.Run(account, func(t *testing.T) {
			sessionsStore := &artifactInvalidationProbe{Store: sessions.New()}
			owner := sessionvo.Owner{ApplicationPrincipalID: account, EffectiveSubjectID: account, EffectiveSubjectType: sessionvo.SubjectService}
			if err := sessionsStore.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
				tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner})
				tx.SaveInteraction(sessionvo.Interaction{ID: "interaction_001", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			evidenceStore := evidencestore.New()
			service := New(evidenceStore, WithSessionStore(sessionsStore), WithCurrentRecordIntegrity())
			body := validArtifactBody(t, "artifact_late_content", map[string]any{"text": "question"})
			sessionsStore.failure = errors.New("metadata temporarily unavailable")
			response, validation, err := service.IngestArtifact(context.Background(), body)
			if err != nil || len(validation) > 0 || !response.Created || sessionsStore.calls != 0 {
				t.Fatalf("artifact ACK depended on derived metadata: %+v %v %v calls=%d", response, validation, err, sessionsStore.calls)
			}
			response, validation, err = service.IngestArtifact(context.Background(), body)
			if err != nil || len(validation) > 0 || response.Created || sessionsStore.calls != 0 {
				t.Fatalf("artifact replay changed ACK or invoked derived invalidation: %+v %v %v calls=%d", response, validation, err, sessionsStore.calls)
			}
		})
	}
}
