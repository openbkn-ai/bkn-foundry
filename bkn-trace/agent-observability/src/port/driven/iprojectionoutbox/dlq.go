// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package iprojectionoutbox

import "time"

// DLQEntry is a single-ID operator preview. It deliberately omits payload,
// credentials, lease tokens and arbitrary dependency error bodies.
type DLQEntry struct {
	ID               uint64     `json:"dlq_id"`
	SourceKind       string     `json:"source_kind"`
	EventID          string     `json:"event_id"`
	FailureClass     string     `json:"failure_class"`
	LastErrorCode    string     `json:"last_error_code"`
	FailedAt         time.Time  `json:"failed_at"`
	ReplayedAt       *time.Time `json:"replayed_at,omitempty"`
	OutboxID         uint64     `json:"outbox_id,omitempty"`
	OutboxStatus     string     `json:"outbox_status,omitempty"`
	AggregateType    string     `json:"aggregate_type,omitempty"`
	AggregateID      string     `json:"aggregate_id,omitempty"`
	AggregateVersion uint64     `json:"aggregate_version,omitempty"`
	EventType        string     `json:"event_type,omitempty"`
	FailedAttempts   uint32     `json:"failed_attempts"`
	Ambiguous        bool       `json:"ambiguous,omitempty"`
}
