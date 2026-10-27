// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// These categories intentionally omit dependency errors and record identifiers.
// Supervisors can distinguish failure phases through wrapping or errors.Join.
var (
	ErrRecordIntegrityCandidateQueryFailed    = errors.New("record integrity candidate query failed")
	ErrRecordIntegrityClaimFailed             = errors.New("record integrity claim failed")
	ErrRecordIntegrityResultPersistenceFailed = errors.New("record integrity result persistence failed")
	ErrRecordIntegrityInspectionDeferred      = errors.New("record integrity inspection deferred or exhausted")
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

// PersistRecordIntegrityBatch is a bounded background pass. Retry history and
// claim ownership live in the store, independently of service/process lifetime.
// No external inspection runs under a database transaction or on a page read.
func (s *Service) PersistRecordIntegrityBatch(ctx context.Context, afterID string, limit int) (string, error) {
	if !s.currentRecordIntegrity {
		return "", nil
	}
	candidatesStore, ok := s.sessionStore.(isessionstore.RecordIntegrityStore)
	if !ok {
		return "", errors.New("record integrity persistence source unavailable")
	}
	retries, ok := s.sessionStore.(isessionstore.RecordIntegrityRetryStore)
	if !ok {
		return "", errors.New("record integrity durable retry source unavailable")
	}
	if limit <= 0 {
		return "", errors.New("record integrity batch limit must be positive")
	}
	candidates, err := candidatesStore.ListRecordIntegrityCandidates(ctx, afterID, min(limit, 100))
	if err != nil {
		return "", errors.Join(ctx.Err(), ErrRecordIntegrityCandidateQueryFailed)
	}
	var nextID string
	var failures []error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nextID, errors.Join(append(failures, err)...)
		}
		nextID = candidate.ID
		claim, acquired, err := retries.ClaimRecordIntegrityRetry(ctx, candidate.ID, candidate.IntegritySourceVersion)
		if err != nil {
			failures = append(failures, ErrRecordIntegrityClaimFailed)
			continue
		}
		if !acquired {
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, 25*time.Second)
		stored, reason := s.materializeClaimedRecordIntegrity(attempt, candidate)
		if errors.Is(attempt.Err(), context.Canceled) {
			stored, reason = nil, "attempt_canceled"
		}
		if errors.Is(attempt.Err(), context.DeadlineExceeded) {
			stored, reason = nil, "attempt_timeout"
		}
		cancel()
		// Cancellation must not erase a counted attempt. A short cleanup context
		// records the outcome; a database failure leaves the lease recoverable.
		finishContext, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, finishErr := retries.FinishRecordIntegrityRetry(finishContext, claim, stored, reason)
		if finishErr != nil && stored != nil {
			// A failed/ambiguous commit is never certified by the worker. If the
			// first commit succeeded, its cleared token rejects this failure write.
			_, _ = retries.FinishRecordIntegrityRetry(finishContext, claim, nil, "persistence_failed")
		}
		finishCancel()
		if finishErr != nil {
			failures = append(failures, ErrRecordIntegrityResultPersistenceFailed)
		}
		if reason != "" && reason != "source_changed" {
			failures = append(failures, ErrRecordIntegrityInspectionDeferred)
		}
	}
	return nextID, errors.Join(append(failures, ctx.Err())...)
}

func (s *Service) materializeClaimedRecordIntegrity(ctx context.Context, candidate sessionvo.Interaction) (*sessionvo.StoredRecordIntegrity, string) {
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
	if err != nil || !found {
		return nil, "snapshot_unavailable"
	}
	if interaction.IntegritySourceVersion != candidate.IntegritySourceVersion {
		return nil, "source_changed"
	}
	// Artifact access retains the canonical owner boundary. AccountType cannot
	// stand in for this service-owned application's identity.
	profile := &evidencevo.AccessProfile{
		ActorID: owner.EffectiveSubjectID, EffectiveSubjectID: owner.EffectiveSubjectID,
		ApplicationPrincipalID: owner.ApplicationPrincipalID, DelegationID: owner.DelegationID,
		AccountActive: true,
	}
	scope := evidencevo.QueryScope{AccessProfile: profile}
	// Each interaction retains the strict 128-read/32MiB inspection limits.
	checkContext := context.WithValue(ctx, recordIntegrityBudgetKey{}, &recordIntegrityReadBudget{})
	report, applicable, version, err := s.inspectAuthorizedLiveRecordIntegrity(checkContext, interaction.ID, interaction.ConversationID, owner, scope)
	if errors.Is(err, errRecordIntegrityInspectionBudget) || errors.Is(err, iartifactstore.ErrCaptureContentReadBudget) {
		return nil, isessionstore.RecordIntegrityBudgetExceeded
	}
	if err != nil || (applicable && report == nil) {
		return nil, "inspection_failed"
	}
	if version != candidate.IntegritySourceVersion {
		return nil, "source_changed"
	}
	return &sessionvo.StoredRecordIntegrity{SourceVersion: version, Applicable: applicable, Report: report}, ""
}

// GetRecordIntegrityRetryStatus is a diagnostic read of the current generation.
// It does not inspect evidence or add work to the ordinary list/detail queries.
// The authorization decision is identical to the persisted verdict read path.
func (s *Service) GetRecordIntegrityRetryStatus(ctx context.Context, id string, scope evidencevo.QueryScope) (isessionstore.RecordIntegrityRetry, bool, error) {
	interaction, _, authorized, err := s.authorizeRecordIntegrityInteraction(ctx, id, scope)
	if err != nil || !authorized {
		return isessionstore.RecordIntegrityRetry{}, false, err
	}
	store, ok := s.sessionStore.(isessionstore.RecordIntegrityRetryStore)
	if !ok {
		return isessionstore.RecordIntegrityRetry{}, false, errors.New("record integrity durable retry source unavailable")
	}
	state, exists, err := store.GetRecordIntegrityRetry(ctx, id, interaction.IntegritySourceVersion)
	if err != nil {
		return isessionstore.RecordIntegrityRetry{}, false, errors.New("record integrity retry status unavailable")
	}
	if !exists {
		state = isessionstore.RecordIntegrityRetry{InteractionID: id, SourceVersion: interaction.IntegritySourceVersion, Status: "pending"}
		if interaction.ExecutionStatus == sessionvo.InteractionActive {
			state.Status = "active"
		} else if interaction.StoredRecordIntegrity != nil {
			// Validate historical values without re-reading live evidence.
			if _, _, err := storedRecordIntegrity(interaction); err != nil {
				return state, false, err
			}
			if interaction.StoredRecordIntegrity.SourceVersion == interaction.IntegritySourceVersion {
				state.Status = isessionstore.RecordIntegritySucceeded
			}
		}
	}
	state.Token = ""
	return state, true, nil
}

// RecheckHistoricalRecordIntegrity is an explicit maintenance read, never a page
// read or an automatic retry. It streams output artifacts one bounded object at
// a time and returns a verdict only after every existing integrity rule passes.
// The caller must save with a source-version CAS; this method writes nothing.
func (s *Service) RecheckHistoricalRecordIntegrity(ctx context.Context, id string, scope evidencevo.QueryScope) (*sessionvo.StoredRecordIntegrity, error) {
	if !s.currentRecordIntegrity {
		return nil, errors.New("record integrity is disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ctx = context.WithValue(ctx, historicalIntegrityRecheckKey{}, true)
	report, applicable, version, err := s.inspectLiveRecordIntegrityWithScope(ctx, id, scope)
	if err != nil {
		return nil, err
	}
	if applicable && report == nil {
		return nil, errors.New("historical integrity recheck incomplete")
	}
	// Missing/unauthorized identities must never produce a non-applicable CAS.
	if report == nil && !applicable {
		interaction, _, authorized, err := s.authorizeRecordIntegrityInteraction(ctx, id, scope)
		if err != nil {
			return nil, err
		}
		if !authorized || interaction.ExecutionStatus == sessionvo.InteractionActive {
			return nil, errors.New("historical integrity recheck unavailable")
		}
	}
	return &sessionvo.StoredRecordIntegrity{SourceVersion: version, Applicable: applicable, Report: report}, nil
}
