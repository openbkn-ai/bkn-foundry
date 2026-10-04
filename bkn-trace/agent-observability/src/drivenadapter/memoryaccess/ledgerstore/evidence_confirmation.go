// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ledgerstore

import (
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func (s *Store) ReadEvidenceEventsByIDs(ids []string) ([]ledgervo.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) > sessionvo.MaxExpectedEvidenceEvents {
		return nil, isessionstore.ErrEvidenceConfirmationLimit
	}
	result := make([]ledgervo.Event, 0, len(ids))
	bytes := 0
	for _, id := range ids {
		if record, found := s.ledger[id]; found {
			bytes += record.storedBytes
			if bytes > sessionvo.MaxEvidenceConfirmationBytes {
				return nil, isessionstore.ErrEvidenceConfirmationLimit
			}
			result = append(result, record.event)
		}
	}
	return result, nil
}
