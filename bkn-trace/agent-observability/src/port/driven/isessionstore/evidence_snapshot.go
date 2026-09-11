// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package isessionstore

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

// ErrInvalidEvidenceJSON distinguishes unreadable stored metadata from absent evidence.
var ErrInvalidEvidenceJSON = errors.New("invalid stored evidence JSON")

// ErrEvidenceIdentityMismatch prevents inconsistent ledger rows entering a snapshot.
var ErrEvidenceIdentityMismatch = errors.New("stored evidence identity mismatch")

// EvidenceSnapshotReader is a separate Core-only port: existing Store and the
// enterprise route Reader do not change. Core must resolve the authorized
// interaction before using this port, exactly as for other lifecycle stores.
// Implementations return a consistent, detached session-store read or an error;
// they must not use a read-committed multi-query transaction as a snapshot.
type EvidenceSnapshotReader interface {
	ReadEvidenceSnapshot(context.Context, string) (sessionvo.EvidenceSnapshot, bool, error)
}
