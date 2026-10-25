// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package audit

import (
	"encoding/json"
	"net/http"
	"time"
)

// PublishStatus describes only this Safe process, not other replicas or the
// consumer. Counters reset when InstanceID changes; delivery recovery does not
// erase previously dropped records. No business identities are retained.
type PublishStatus struct {
	SchemaVersion   string     `json:"schema_version"`
	InstanceID      string     `json:"instance_id"`
	ObservedAt      time.Time  `json:"observed_at"`
	Status          string     `json:"status"`
	Reason          string     `json:"reason"`
	DroppedRecords  uint64     `json:"dropped_records"`
	LastDeliveredAt *time.Time `json:"last_delivered_at"`
	LastFailureAt   *time.Time `json:"last_failure_at"`
}

func (t *PublishTelemetry) observeStatusLocked(source, reason string, now time.Time) {
	switch source {
	case "bkn-safe-admin", "bkn-safe-access", "bkn-safe-security":
	default:
		return
	}
	switch reason {
	case "delivered":
		t.status.Status, t.status.Reason = "healthy", "delivery_observed"
		t.status.LastDeliveredAt = &now
	case "dropped_unavailable", "dropped_invalid", "dropped_queue_full", "dropped_closed", "dropped_retry_exhausted", "dropped_shutdown_timeout":
		t.status.DroppedRecords++
		t.status.Status, t.status.Reason = "degraded", reason
		t.status.LastFailureAt = &now
	}
}

// MarkUnavailable records runtime initialization failure, not a dropped event.
func (t *PublishTelemetry) MarkUnavailable(reason string) {
	if t == nil {
		return
	}
	if reason != "kafka_not_configured" {
		reason = "publisher_unavailable"
	}
	now := time.Now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.Status, t.status.Reason = "degraded", reason
	t.status.LastFailureAt = &now
}

func (t *PublishTelemetry) Snapshot() PublishStatus {
	if t == nil {
		return PublishStatus{SchemaVersion: "1.0", ObservedAt: time.Now().UTC(), Status: "unknown", Reason: "delivery_not_observed"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	result := t.status
	result.ObservedAt = time.Now().UTC()
	if result.LastDeliveredAt != nil {
		v := *result.LastDeliveredAt
		result.LastDeliveredAt = &v
	}
	if result.LastFailureAt != nil {
		v := *result.LastFailureAt
		result.LastFailureAt = &v
	}
	return result
}

func (t *PublishTelemetry) ServeStatusHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(t.Snapshot())
}
