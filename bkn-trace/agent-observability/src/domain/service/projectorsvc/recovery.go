// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package projectorsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

// IsSearchProjectionEvent distinguishes the worker's search sink from handlers
// with their own artifact, revision or enterprise lifecycle semantics.
func IsSearchProjectionEvent(eventType string) bool {
	switch eventType {
	case "artifact.persist_requested", "revision.input.sealed", historicalProvenanceBuildRequested:
		return false
	default:
		return true
	}
}

func (w *Worker) recoveryDeadline(ctx context.Context, item iprojectionoutbox.Item, err error) (time.Time, bool, error) {
	var dependency *iprojectionoutbox.DependencyError
	recoverable := IsSearchProjectionEvent(item.EventType) && errors.As(err, &dependency) && dependency.Recoverable && !iprojectionoutbox.IsPermanent(err)
	if item.CreatedAt.IsZero() {
		return time.Time{}, false, nil
	} // Unknown age must retain the attempt bound.
	until := item.CreatedAt.Add(w.options.MaxRetryAge)
	if recoverable && !until.After(w.options.Now().UTC()) {
		if reader, ok := w.store.(iprojectionoutbox.ReplayTimeReader); ok {
			readTimeout := w.options.LeaseIOTimeout
			if readTimeout <= 0 {
				readTimeout = 2 * time.Second
			}
			readCtx, cancel := ioContext(ctx, readTimeout)
			replayed, readErr := reader.ProjectionReplayTime(readCtx, item.EventID)
			cancel()
			if readErr != nil {
				return time.Time{}, false, fmt.Errorf("read projection replay time: %w", readErr)
			}
			if replayed.After(item.CreatedAt) {
				until = replayed.Add(w.options.MaxRetryAge)
			}
		}
	}
	return until, recoverable, nil
}

func projectionFailureDetails(err error) (string, int) {
	var dependency *iprojectionoutbox.DependencyError
	if errors.As(err, &dependency) {
		switch dependency.Kind {
		case "opensearch_http_error", "opensearch_timeout", "opensearch_transport_error", "opensearch_projection_error":
			status := dependency.HTTPStatus
			if status < 100 || status > 599 {
				status = 0
			}
			return dependency.Kind, status
		}
	}
	return "projection_failed", 0
}

func logProjectionFailure(ctx context.Context, item iprojectionoutbox.Item, err error, decision string) {
	attempt := uint64(item.Attempts) + 1
	// Log retry attempts 1,2,4,8,... and every final decision. Counters retain
	// every failure/confirmed transition without payload/ID metric cardinality.
	if decision == "retry" && attempt&(attempt-1) != 0 {
		return
	}
	kind, status := projectionFailureDetails(err)
	slog.WarnContext(ctx, "projection attempt failed",
		"outbox_id", item.ID, "aggregate_type", boundedProjectionID(item.AggregateType),
		"aggregate_id", boundedProjectionID(item.AggregateID), "event_id", boundedProjectionID(item.EventID),
		"event_type", boundedProjectionID(item.EventType), "attempt", attempt,
		"failure_kind", kind, "http_status", status, "decision", decision)
}
func boundedProjectionID(value string) string {
	if len(value) > 128 {
		return "invalid"
	}
	for _, ch := range value {
		if ch < 32 || ch > 126 {
			return "invalid"
		}
	}
	return value
}

func projectionFailureCode(err error) string {
	kind, status := projectionFailureDetails(err)
	if status != 0 {
		return fmt.Sprintf("opensearch_http_%d", status)
	}
	return kind
}
