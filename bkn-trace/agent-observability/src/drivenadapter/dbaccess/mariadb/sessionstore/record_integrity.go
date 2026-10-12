// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var _ isessionstore.RecordIntegrityStore = (*Store)(nil)

func (s *Store) SaveStoredRecordIntegrity(ctx context.Context, id string, stored sessionvo.StoredRecordIntegrity) (bool, error) {
	raw, err := isessionstore.MarshalStoredRecordIntegrity(stored)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE bkn_trace_interactions SET record_integrity_json=?
		WHERE interaction_id=? AND record_integrity_version=? AND record_integrity_json IS NULL AND execution_status<>?`,
		string(raw), id, stored.SourceVersion, sessionvo.InteractionActive)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *Store) ListRecordIntegrityCandidates(ctx context.Context, afterID string, limit int) ([]sessionvo.Interaction, error) {
	if limit <= 0 {
		return []sessionvo.Interaction{}, nil
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT interaction_id, record_integrity_version FROM bkn_trace_interactions
		WHERE record_integrity_pending=1 AND interaction_id>? ORDER BY interaction_id ASC LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []sessionvo.Interaction{}
	for rows.Next() {
		var value sessionvo.Interaction
		if err := rows.Scan(&value.ID, &value.IntegritySourceVersion); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) InvalidateRecordIntegrity(ctx context.Context, id string, owner sessionvo.Owner) error {
	_, err := s.db.ExecContext(ctx, `UPDATE bkn_trace_interactions i JOIN bkn_trace_conversations c ON c.conversation_id=i.conversation_id
		SET i.record_integrity_version=i.record_integrity_version+1, i.record_integrity_json=NULL
		WHERE i.interaction_id=? AND i.execution_status<>? AND c.application_principal_id=? AND c.effective_subject_type=? AND c.effective_subject_id=? AND c.delegation_id=?`,
		id, sessionvo.InteractionActive, owner.ApplicationPrincipalID, owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID)
	return err
}

func (t *transaction) markIntegrityDirty(id string) {
	if id == "" || t.err != nil {
		return
	}
	if t.integrityDirty == nil {
		t.integrityDirty = make(map[string]struct{})
	}
	t.integrityDirty[id] = struct{}{}
	if len(t.integrityDirty) > isessionstore.MaxRecordIntegrityDirtyIDs {
		t.err = isessionstore.ErrRecordIntegrityDirtyLimit
	}
}

func (t *transaction) flushIntegrityDirty() {
	if t.err != nil || len(t.integrityDirty) == 0 {
		return
	}
	ids := make([]string, 0, len(t.integrityDirty))
	for id := range t.integrityDirty {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	args := make([]any, 0, len(ids)+1)
	args = append(args, sessionvo.InteractionActive)
	for _, id := range ids {
		args = append(args, id)
	}
	// One bounded update per transaction; all source writes roll back together
	// if invalidation fails. Source versions never enter lifecycle projections.
	_, t.err = t.tx.ExecContext(t.ctx, `UPDATE bkn_trace_interactions SET record_integrity_version=record_integrity_version+1, record_integrity_json=NULL
		WHERE execution_status<>? AND interaction_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
}
