// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var _ isessionstore.EvidenceSnapshotReader = (*Store)(nil)

func (s *Store) ReadEvidenceSnapshot(ctx context.Context, id string) (sessionvo.EvidenceSnapshot, bool, error) {
	if err := ctx.Err(); err != nil {
		return sessionvo.EvidenceSnapshot{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return sessionvo.EvidenceSnapshot{}, false, err
	}
	tx := memoryTransaction{s: s}
	interaction, found := tx.PeekInteraction(id)
	if !found {
		return sessionvo.EvidenceSnapshot{}, false, nil
	}
	result, err := sessionvo.CopyEvidenceSnapshot(interaction, tx.ListOperations(id), tx.ListReceipts(id),
		tx.ListOperationCallFacts(id), tx.ListAssemblyRevisions(id))
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return sessionvo.EvidenceSnapshot{}, false, err
	}
	return result, true, nil
}
