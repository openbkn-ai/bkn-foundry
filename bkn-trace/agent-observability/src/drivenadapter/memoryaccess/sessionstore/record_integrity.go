// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var _ isessionstore.RecordIntegrityStore = (*Store)(nil)

func (s *Store) SaveStoredRecordIntegrity(ctx context.Context, id string, stored sessionvo.StoredRecordIntegrity) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := isessionstore.MarshalStoredRecordIntegrity(stored); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	value, found := s.interactions[id]
	if !found || !value.IsTerminal() || value.IntegritySourceVersion != stored.SourceVersion || value.StoredRecordIntegrity != nil {
		return false, nil
	}
	value.StoredRecordIntegrity = sessionvo.CopyStoredRecordIntegrity(&stored)
	s.interactions[id] = value
	return true, nil
}

func (s *Store) ListRecordIntegrityCandidates(ctx context.Context, afterID string, limit int) ([]sessionvo.Interaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []sessionvo.Interaction{}, nil
	}
	if limit > 100 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := []sessionvo.Interaction{}
	for _, value := range s.interactions {
		if value.ID > afterID && value.IsTerminal() && value.StoredRecordIntegrity == nil {
			result = append(result, sessionvo.Interaction{ID: value.ID, IntegritySourceVersion: value.IntegritySourceVersion})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *Store) InvalidateRecordIntegrity(ctx context.Context, id string, owner sessionvo.Owner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	value, found := s.interactions[id]
	if !found || !value.IsTerminal() {
		return nil
	}
	conversation, found := s.conversations[value.ConversationID]
	if !found || !conversation.Owner.Equal(owner) {
		return nil
	}
	value.IntegritySourceVersion++
	value.StoredRecordIntegrity = nil
	s.interactions[id] = value
	return nil
}

func (tx memoryTransaction) markIntegrityDirty(id string) {
	if id != "" && tx.integrityDirty != nil {
		tx.integrityDirty[id] = struct{}{}
	}
}

func (tx memoryTransaction) flushIntegrityDirty() {
	for id := range tx.integrityDirty {
		value, found := tx.s.interactions[id]
		if !found || !value.IsTerminal() {
			continue
		}
		value.IntegritySourceVersion++
		value.StoredRecordIntegrity = nil
		tx.s.interactions[id] = value
	}
}

func copyInteractionIntegrity(value sessionvo.Interaction) sessionvo.Interaction {
	value.StoredRecordIntegrity = sessionvo.CopyStoredRecordIntegrity(value.StoredRecordIntegrity)
	return value
}
