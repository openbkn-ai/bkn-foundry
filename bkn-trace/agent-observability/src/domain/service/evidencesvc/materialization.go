// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func storedRecordIntegrity(interaction sessionvo.Interaction) (*evidencevo.RecordIntegrity, bool, error) {
	if interaction.ExecutionStatus == sessionvo.InteractionActive {
		return nil, false, nil
	}
	stored := interaction.StoredRecordIntegrity
	if stored == nil || stored.SourceVersion != interaction.IntegritySourceVersion {
		// A terminal round without a current check is unknown, not proof that no
		// registered calls exist. It must not certify the whole conversation.
		return nil, true, nil
	}
	if !stored.Applicable {
		if stored.Report != nil {
			return nil, false, errors.New("non-applicable integrity record contains a verdict")
		}
		return nil, false, nil
	}
	report := stored.Report
	if report == nil || report.CheckedAt.IsZero() || report.Scope != "registered_call_records" ||
		(report.Status != "complete" && report.Status != "missing") ||
		(report.Status == "complete" && len(report.Missing) != 0) || (report.Status == "missing" && len(report.Missing) == 0) {
		return nil, true, errors.New("invalid stored record integrity verdict")
	}
	return sessionvo.CopyStoredRecordIntegrity(stored).Report, true, nil
}

// PersistRecordIntegrityBatch is a bounded background pass, independent of
// business requests and page reads. A failed candidate remains uncomputed and
// is retried on the next cursor pass; it does not prevent later candidates.
func (s *Service) PersistRecordIntegrityBatch(ctx context.Context, afterID string, limit int) (string, error) {
	if !s.currentRecordIntegrity {
		return "", nil
	}
	store, ok := s.sessionStore.(isessionstore.RecordIntegrityStore)
	if !ok {
		return "", errors.New("record integrity persistence source unavailable")
	}
	if limit <= 0 {
		return "", errors.New("record integrity batch limit must be positive")
	}
	if limit > 100 {
		limit = 100
	}
	candidates, err := store.ListRecordIntegrityCandidates(ctx, afterID, limit)
	if err != nil {
		return "", err
	}
	var nextID string
	var failures []error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nextID, errors.Join(append(failures, err)...)
		}
		nextID = candidate.ID
		var interaction sessionvo.Interaction
		var owner sessionvo.Owner
		found := false
		err := s.sessionStore.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			var exists bool
			interaction, exists = tx.PeekInteraction(candidate.ID)
			if !exists || interaction.ExecutionStatus == sessionvo.InteractionActive {
				return nil
			}
			conversation, exists := tx.PeekConversation(interaction.ConversationID)
			if !exists {
				return errors.New("record integrity candidate conversation unavailable")
			}
			owner, found = conversation.Owner, true
			return nil
		})
		if err == nil && found {
			// Materialization is an internal read, but artifact access must still use
			// the canonical owner boundary. Artifact stores identify service-owned
			// records as app records, so AccountType cannot represent this owner.
			profile := &evidencevo.AccessProfile{
				ActorID: owner.EffectiveSubjectID, EffectiveSubjectID: owner.EffectiveSubjectID,
				ApplicationPrincipalID: owner.ApplicationPrincipalID, DelegationID: owner.DelegationID,
				AccountActive: true,
			}
			scope := evidencevo.QueryScope{AccessProfile: profile}
			// Every interaction retains the existing strict 128-read/32MiB bounds.
			// A large failed interaction cannot consume another candidate's budget.
			checkContext := context.WithValue(ctx, recordIntegrityBudgetKey{}, &recordIntegrityReadBudget{})
			report, applicable, version, checkErr := s.inspectAuthorizedLiveRecordIntegrity(checkContext, interaction.ID, interaction.ConversationID, owner, scope)
			err = checkErr
			if err == nil && (!applicable || report != nil) {
				_, err = store.SaveStoredRecordIntegrity(ctx, interaction.ID, sessionvo.StoredRecordIntegrity{
					SourceVersion: version, Applicable: applicable, Report: report,
				})
			}
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("materialize record integrity %s: %w", candidate.ID, err))
		}
	}
	return nextID, errors.Join(failures...)
}
