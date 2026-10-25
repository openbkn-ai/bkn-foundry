// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package audit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublishStatusLifecycle(t *testing.T) {
	telemetry := NewPublishTelemetry()
	initial := telemetry.Snapshot()
	if initial.Status != "unknown" || initial.Reason != "delivery_not_observed" || initial.InstanceID == "" || initial.LastDeliveredAt != nil {
		t.Fatalf("initial=%+v", initial)
	}
	telemetry.Observe("accepted")
	if telemetry.Snapshot().Status != "unknown" {
		t.Fatal("enqueue is not delivery")
	}
	telemetry.MarkUnavailable("kafka_not_configured")
	if s := telemetry.Snapshot(); s.Status != "degraded" || s.Reason != "kafka_not_configured" || s.DroppedRecords != 0 {
		t.Fatalf("unconfigured=%+v", s)
	}
	telemetry.MarkUnavailable("publisher_unavailable")
	if telemetry.Snapshot().Reason != "publisher_unavailable" {
		t.Fatal("startup failure missing")
	}
	telemetry.ObserveForSource("bkn-safe-access", "dropped_queue_full")
	telemetry.Observe("accepted")
	if s := telemetry.Snapshot(); s.Status != "degraded" || s.DroppedRecords != 1 {
		t.Fatalf("accepted cleared failure: %+v", s)
	}
	telemetry.ObserveForSource("bkn-safe-security", "delivered")
	recovered := telemetry.Snapshot()
	if recovered.Status != "healthy" || recovered.DroppedRecords != 1 || recovered.LastDeliveredAt == nil || recovered.LastFailureAt == nil || recovered.InstanceID != initial.InstanceID {
		t.Fatalf("recovery=%+v", recovered)
	}
	// Merely reading an idle publisher cannot turn it into an outage.
	if telemetry.Snapshot().Status != "healthy" {
		t.Fatal("idle status changed")
	}
	telemetry.Observe("dropped_retry_exhausted")
	if s := telemetry.Snapshot(); s.Status != "degraded" || s.DroppedRecords != 2 {
		t.Fatalf("later failure=%+v", s)
	}
	telemetry.ObserveForSource("unregistered", "delivered")
	if telemetry.Snapshot().Status != "degraded" {
		t.Fatal("unregistered source changed aggregate")
	}
	if NewPublishTelemetry().Snapshot().InstanceID == initial.InstanceID {
		t.Fatal("instance id reused")
	}
}

func TestPublishStatusHTTPContainsOnlyAggregateFields(t *testing.T) {
	telemetry := NewPublishTelemetry()
	telemetry.Observe("dropped_invalid")
	response := httptest.NewRecorder()
	telemetry.ServeStatusHTTP(response, httptest.NewRequest(http.MethodGet, "/internal/audit/publish-status", nil))
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	expected := []string{"schema_version", "instance_id", "observed_at", "status", "reason", "dropped_records", "last_delivered_at", "last_failure_at"}
	if len(fields) != len(expected) {
		t.Fatalf("unexpected fields: %v", fields)
	}
	for _, name := range expected {
		if _, ok := fields[name]; !ok {
			t.Fatalf("missing %s", name)
		}
	}
	response = httptest.NewRecorder()
	telemetry.ServeStatusHTTP(response, httptest.NewRequest(http.MethodPost, "/internal/audit/publish-status", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatal(response.Code)
	}
}
