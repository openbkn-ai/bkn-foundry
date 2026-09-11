// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package iartifactstore

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

var ErrCaptureReadBudget = errors.New("BKN_TRACE_CAPTURE_READ_BUDGET_EXCEEDED")
var ErrCaptureIdentityMismatch = errors.New("capture artifact identity mismatch")
var ErrInvalidCaptureBudget = errors.New("BKN_TRACE_INVALID_CAPTURE_READ_BUDGET")

// CaptureReadResult counts response bytes consumed, including probe bytes and error bodies.
// An error or inaccessible document never returns artifact content.
type CaptureReadResult struct {
	Artifact  evidencevo.EvidenceArtifact
	Found     bool
	ReadBytes int64
}

// CaptureReader is a read-only, bounded source for evidence capture. Budgets must
// be positive and below MaxInt64; at most maxResponseBytes+1 bytes are consumed.
type CaptureReader interface {
	ReadArtifactForCapture(ctx context.Context, artifactID string, scope evidencevo.QueryScope, maxResponseBytes int64) (CaptureReadResult, error)
}
