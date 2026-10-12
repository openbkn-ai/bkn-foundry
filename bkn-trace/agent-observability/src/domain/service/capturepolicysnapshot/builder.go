// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

// Package capturepolicysnapshot builds the unsigned internal policy snapshot.
package capturepolicysnapshot

import (
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceadmissionsvc"
)

var ErrNotConfigured = errors.New("capture policy snapshot builder is not configured")

type Builder struct {
	TTL time.Duration
	Now func() time.Time
}

func (s Builder) Build(revision uint64, state capturepolicysvc.State) (traceadmissionsvc.Snapshot, error) {
	if s.TTL <= 0 {
		return traceadmissionsvc.Snapshot{}, ErrNotConfigured
	}
	if revision == 0 {
		return traceadmissionsvc.Snapshot{}, traceadmissionsvc.ErrInvalidSnapshot
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	mode := traceadmissionsvc.ModeDisabled
	if state == capturepolicysvc.StateEnabled || state == capturepolicysvc.StateEnabling {
		mode = traceadmissionsvc.ModeEnabled
	}
	return traceadmissionsvc.Snapshot{
		ContractVersion: traceadmissionsvc.ContractVersion,
		Revision:        revision, TraceAdmission: mode, EvidenceAdmission: mode,
		IssuedAt: now, ExpiresAt: now.Add(s.TTL),
	}, nil
}
