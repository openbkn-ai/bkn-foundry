// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

// Package capturepolicysnapshot signs the pull snapshot consumed by the
// Trace Admission Gateway. Its key is independent from projection grants.
package capturepolicysnapshot

import (
	"crypto/ed25519"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceadmissionsvc"
)

var ErrNotConfigured = errors.New("capture policy signer is not configured")

type Signer struct {
	PrivateKey ed25519.PrivateKey
	KeyID      string
	Audience   string
	TTL        time.Duration
	Now        func() time.Time
}

func (s Signer) Sign(revision uint64, state capturepolicysvc.State) (traceadmissionsvc.SignedSnapshot, error) {
	if len(s.PrivateKey) != ed25519.PrivateKeySize || s.KeyID == "" || s.Audience == "" || s.TTL <= 0 {
		return traceadmissionsvc.SignedSnapshot{}, ErrNotConfigured
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	mode := traceadmissionsvc.ModeDisabled
	if state == capturepolicysvc.StateEnabled || state == capturepolicysvc.StateEnabling {
		mode = traceadmissionsvc.ModeEnabled
	}
	return traceadmissionsvc.SignSnapshot(traceadmissionsvc.SignedSnapshot{
		Revision: revision, Mode: mode, IssuedAt: now, ExpiresAt: now.Add(s.TTL), KeyID: s.KeyID, Audience: s.Audience,
	}, s.PrivateKey)
}
