// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type materializationPhaseFaultStore struct {
	*integritySnapshotStore
	phase string
}

func (s *materializationPhaseFaultStore) ListRecordIntegrityCandidates(ctx context.Context, after string, limit int) ([]sessionvo.Interaction, error) {
	if s.phase == "query" {
		return nil, errors.New("private-query-record-id")
	}
	return s.integritySnapshotStore.ListRecordIntegrityCandidates(ctx, after, limit)
}
func (s *materializationPhaseFaultStore) ClaimRecordIntegrityRetry(ctx context.Context, id string, version uint64) (isessionstore.RecordIntegrityRetry, bool, error) {
	if s.phase == "claim" {
		return isessionstore.RecordIntegrityRetry{}, false, errors.New("private-claim-record-id")
	}
	return s.integritySnapshotStore.ClaimRecordIntegrityRetry(ctx, id, version)
}
func (s *materializationPhaseFaultStore) FinishRecordIntegrityRetry(ctx context.Context, claim isessionstore.RecordIntegrityRetry, stored *sessionvo.StoredRecordIntegrity, reason string) (bool, error) {
	if s.phase == "persist" {
		return false, errors.New("private-persist-record-id")
	}
	return s.integritySnapshotStore.FinishRecordIntegrityRetry(ctx, claim, stored, reason)
}

func TestMaterializationFailurePhasesAreSafeSentinels(t *testing.T) {
	categories := []error{ErrRecordIntegrityCandidateQueryFailed, ErrRecordIntegrityClaimFailed, ErrRecordIntegrityResultPersistenceFailed, ErrRecordIntegrityInspectionDeferred}
	for index, phase := range []string{"query", "claim", "persist", "inspection"} {
		t.Run(phase, func(t *testing.T) {
			snapshot, _, _ := integrityFixture()
			service := integrityServiceForSnapshot(t, snapshot)
			store := service.sessionStore.(*integritySnapshotStore)
			alignIntegrityFixtureVersions(t, store)
			if phase == "inspection" {
				store.readErr = errors.New("private-inspection-record-id")
			}
			service.sessionStore = &materializationPhaseFaultStore{integritySnapshotStore: store, phase: phase}
			_, err := service.PersistRecordIntegrityBatch(context.Background(), "", 10)
			if !errors.Is(err, categories[index]) {
				t.Fatalf("missing %s sentinel: %v", phase, err)
			}
			// Callers may wrap/join a batch error without losing its safe category.
			wrapped := fmt.Errorf("batch: %w", errors.Join(err, context.DeadlineExceeded))
			for i, category := range categories {
				if errors.Is(wrapped, category) != (i == index) {
					t.Fatalf("wrong %s category %d: %v", phase, i, wrapped)
				}
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatalf("raw dependency error retained: %v", err)
			}
		})
	}
}
