// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func (s *Store) SetEvidenceReader(reader func([]string) ([]ledgervo.Event, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evidenceReader = reader
}

var _ isessionstore.EvidenceConfirmationTransaction = memoryTransaction{}

func (tx memoryTransaction) ReadEvidenceEventsByIDs(ids []string) ([]ledgervo.Event, error) {
	if len(ids) == 0 {
		return []ledgervo.Event{}, nil
	}
	if tx.s.evidenceReader == nil {
		return nil, errors.New("memory evidence confirmation reader is not bound")
	}
	return tx.s.evidenceReader(ids)
}
