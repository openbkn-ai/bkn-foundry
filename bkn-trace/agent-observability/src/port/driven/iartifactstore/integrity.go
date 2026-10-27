// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package iartifactstore

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

// IntegrityReadResult contains only hash-verified metadata, never a retained body.
// A missing or inaccessible object has no verified lengths.
type IntegrityReadResult struct {
	Artifact                           evidencevo.EvidenceArtifact
	Found, Exists                      bool
	CanonicalLength, HTMLEscapedLength int
	ReadBytes                          int64
}

// IntegrityReader is used by explicit historical rechecks. Each object is a
// separate bounded work unit; ordinary page reads and capture are unchanged.
type IntegrityReader interface {
	VerifyArtifactForIntegrity(context.Context, string, evidencevo.QueryScope) (IntegrityReadResult, error)
}
