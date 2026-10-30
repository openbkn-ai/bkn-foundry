// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package iprojectionsource

import (
	"context"
	"errors"
)

// ErrSummaryScanBudgetExceeded never represents a complete count or page.
// Callers should narrow the time range or other constraints before retrying.
var ErrSummaryScanBudgetExceeded = errors.New("trace summary query resource budget exceeded; narrow the time range or filters and retry")

// TraceSummaryCandidate is a lightweight receipt identity, not a complete
// Trace or an Interaction authorization decision.
type TraceSummaryCandidate struct {
	TraceID, ConversationID, InteractionID string
}

// TraceSummaryCandidateFilter may reject impossible matches before expansion.
// It must retain every possible visible match. Returned IDs never grant access;
// the scanner still expands and authorizes complete Interactions before visiting.
type TraceSummaryCandidateFilter func(context.Context, []TraceSummaryCandidate) ([]string, error)

// TraceSummaryScanSourcePort enumerates complete authorized Trace batches from
// one receipt snapshot. It must not emit a Trace more than once, or treat a
// partial shard response, a truncated identity or a failed scan as completion.
// The callback must not retain a batch's raw events or artifact bodies.
type TraceSummaryScanSourcePort interface {
	ScanTraceSummaryProjection(context.Context, Query, func(Result) error) error
}
