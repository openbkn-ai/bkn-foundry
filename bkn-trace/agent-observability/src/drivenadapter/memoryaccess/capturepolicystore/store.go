// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package capturepolicystore

import (
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
)

// Store is a deterministic adapter for unit and contract tests. The durable
// MariaDB adapter is a shared integration patch and must implement the same
// Reader contract without changing the API model.
type Store struct {
	mu       sync.RWMutex
	snapshot capturepolicysvc.Snapshot
}

func New(snapshot capturepolicysvc.Snapshot) *Store { return &Store{snapshot: snapshot} }

func (s *Store) Read(ctx context.Context) (capturepolicysvc.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return capturepolicysvc.Snapshot{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot, nil
}

func (s *Store) Replace(snapshot capturepolicysvc.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = snapshot
}
