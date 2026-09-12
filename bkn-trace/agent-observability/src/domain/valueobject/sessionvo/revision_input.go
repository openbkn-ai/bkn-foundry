// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

// SealedRevisionInput is internal storage, not a lifecycle or MCP response.
// Package must be produced by the revision input codec from the same transaction.
type SealedRevisionInput struct {
	RevisionID, InteractionID, Hash string
	Package                         []byte
}

// RevisionInputNotice is an internal outbox payload, not an API response.
type RevisionInputNotice struct {
	Version       string `json:"version"`
	InteractionID string `json:"interaction_id"`
	RevisionID    string `json:"revision_id"`
	Hash          string `json:"input_hash"`
}

const RevisionInputSealedEvent = "revision.input.sealed"
