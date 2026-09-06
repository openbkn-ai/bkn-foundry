// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package iprojectionsource

import (
	"context"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

type Query struct {
	Scope           evidencevo.QueryScope
	From            time.Time
	To              time.Time
	Status          string
	RequestID       string
	TraceID         string
	TraceIDs        []string
	ConversationIDs []string
	// InteractionIDs narrows artifact reads to canonical Interaction facts while
	// retaining the caller's ordinary scope filters. It is intentionally distinct
	// from AuthorizedInteractionIDs, which represents a pre-authorized Core handoff.
	InteractionIDs []string
	// ArtifactTypes bounds an artifact read to the contract types required by the
	// caller, for example the terminal question/result previews in a list page.
	ArtifactTypes []evidencevo.ArtifactType
	InteractionID string
	// AuthorizedInteractionIDs is an internal handoff from the Core projection.
	// It carries an Interaction-level authorization decision to its artifact reader.
	AuthorizedInteractionIDs []string
	Limit                    int
}

type Result struct {
	Traces    []evidencevo.NormalizedTrace
	Artifacts []evidencevo.EvidenceArtifact
	Truncated bool
}

type ProjectionSourcePort interface {
	LoadExecutionProjection(ctx context.Context, query Query) (Result, error)
}
