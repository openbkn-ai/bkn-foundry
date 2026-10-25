// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package rdto

import "time"

// RecordIntegrityRetryStatusResponse is read-only diagnostic metadata, not an
// integrity verdict or a worker claim. Lease ownership tokens are never exposed.
type RecordIntegrityRetryStatusResponse struct {
	InteractionID     string    `json:"interaction_id"`
	SourceVersion     uint64    `json:"source_version"`
	Status            string    `json:"status" enums:"pending,active,running,retry_wait,exhausted,succeeded,superseded"`
	Attempts          int       `json:"attempts"`
	FirstAttemptAt    time.Time `json:"first_attempt_at"`
	LastAttemptAt     time.Time `json:"last_attempt_at"`
	NextAttemptAt     time.Time `json:"next_attempt_at"`
	LeaseUntil        time.Time `json:"lease_until"`
	LastFailureReason string    `json:"last_failure_reason"`
	Reason            string    `json:"reason"`
}
