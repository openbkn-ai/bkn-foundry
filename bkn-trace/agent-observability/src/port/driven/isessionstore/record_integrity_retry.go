// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package isessionstore

import (
	"context"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

const RecordIntegrityMaxAttempts = 8
const RecordIntegrityRetryDeadline = 30 * time.Minute
const RecordIntegrityLeaseDuration = 60 * time.Second
const (
	RecordIntegritySuperseded = "superseded"
	RecordIntegrityRunning    = "running"
	RecordIntegrityRetryWait  = "retry_wait"
	RecordIntegrityExhausted  = "exhausted"
	RecordIntegritySucceeded  = "succeeded"
)

type RecordIntegrityRetry struct {
	InteractionID     string    `json:"interaction_id"`
	SourceVersion     uint64    `json:"source_version"`
	Token             string    `json:"-"`
	Status            string    `json:"status"`
	Attempts          int       `json:"attempts"`
	FirstAttemptAt    time.Time `json:"first_attempt_at"`
	LastAttemptAt     time.Time `json:"last_attempt_at"`
	NextAttemptAt     time.Time `json:"next_attempt_at"`
	LeaseUntil        time.Time `json:"lease_until"`
	LastFailureReason string    `json:"last_failure_reason"`
	Reason            string    `json:"reason"`
}
type RecordIntegrityRetryStore interface {
	ClaimRecordIntegrityRetry(context.Context, string, uint64) (RecordIntegrityRetry, bool, error)
	FinishRecordIntegrityRetry(context.Context, RecordIntegrityRetry, *sessionvo.StoredRecordIntegrity, string) (bool, error)
	GetRecordIntegrityRetry(context.Context, string, uint64) (RecordIntegrityRetry, bool, error)
}

func RecordIntegrityBackoff(attempt int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < attempt && d < 5*time.Minute; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}
func ValidRecordIntegrityRetryReason(reason string) bool {
	switch reason {
	case "source_changed", "inspection_failed", "snapshot_unavailable", "persistence_failed", "attempt_timeout", "attempt_canceled", "lease_expired", "attempts_exhausted", "retry_deadline_exceeded":
		return true
	}
	return false
}
func ValidateRecordIntegrityRetryResult(claim RecordIntegrityRetry, stored *sessionvo.StoredRecordIntegrity, reason string) error {
	if stored == nil {
		if !ValidRecordIntegrityRetryReason(reason) {
			return errors.New("invalid record integrity retry reason")
		}
		return nil
	}
	if stored.SourceVersion != claim.SourceVersion {
		return errors.New("record integrity retry source mismatch")
	}
	if !stored.Applicable {
		if stored.Report != nil {
			return errors.New("invalid non-applicable integrity verdict")
		}
		return nil
	}
	r := stored.Report
	if r == nil || r.CheckedAt.IsZero() || r.Scope != "registered_call_records" || (r.Status != "complete" && r.Status != "missing") || (r.Status == "complete" && len(r.Missing) != 0) || (r.Status == "missing" && len(r.Missing) == 0) {
		return errors.New("invalid strict record integrity verdict")
	}
	return nil
}
func RecordIntegrityRetryDue(r RecordIntegrityRetry, now time.Time) bool {
	switch r.Status {
	case RecordIntegrityRunning:
		return !now.Before(r.LeaseUntil)
	case RecordIntegrityRetryWait:
		return !now.Before(r.NextAttemptAt) || !now.Before(r.FirstAttemptAt.Add(RecordIntegrityRetryDeadline))
	}
	return false
}
func RecordIntegrityExhaustion(r RecordIntegrityRetry, now time.Time) string {
	if !now.Before(r.FirstAttemptAt.Add(RecordIntegrityRetryDeadline)) {
		return "retry_deadline_exceeded"
	}
	if r.Attempts >= RecordIntegrityMaxAttempts {
		return "attempts_exhausted"
	}
	return ""
}
