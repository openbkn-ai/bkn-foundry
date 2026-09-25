// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package observabilityvo

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSourceCoverageStatusDisclosesDroppedTelemetry(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	coverage := SourceCoverage{
		SourceID:        "otel-runtime",
		DeploymentID:    "observability/otelcol-contrib",
		State:           SourceCoverageDegraded,
		Reason:          "telemetry_dropped",
		DroppedRecords:  3,
		FirstObservedAt: now.Add(-time.Minute),
		LastObservedAt:  now,
	}

	status := coverage.Merge(SourceStatus{SourceID: "otel-runtime", Status: "healthy", Reliability: "best_effort"})
	if status.Status != "degraded" || status.Reason != "telemetry_dropped" {
		t.Fatalf("expected durable degradation, got %+v", status)
	}
	if status.DroppedRecords == nil || *status.DroppedRecords != 3 {
		t.Fatalf("expected dropped record count, got %+v", status.DroppedRecords)
	}
	if status.DroppedRecordsSince == nil || !status.DroppedRecordsSince.Equal(coverage.FirstObservedAt) {
		t.Fatalf("expected dropped record window start, got %+v", status.DroppedRecordsSince)
	}
}

func TestSourceStatusDropWindowJSONShape(t *testing.T) {
	t.Run("unaffected source omits drop pair", func(t *testing.T) {
		assertDropWindowJSON(t, SourceStatus{SourceID: "runtime", CollectionMethod: "direct_otlp"}, false, nil, nil)
	})
	t.Run("Kafka Audit unknown pair is explicit null", func(t *testing.T) {
		assertDropWindowJSON(t, SourceStatus{SourceID: "audit-ledger", CollectionMethod: "kafka_audit"}, true, nil, nil)
	})
	t.Run("observed drop pair contains count and start", func(t *testing.T) {
		count := int64(3)
		since := time.Date(2026, time.August, 7, 11, 59, 0, 0, time.UTC)
		assertDropWindowJSON(t, SourceStatus{SourceID: "runtime", CollectionMethod: "direct_otlp", DroppedRecords: &count, DroppedRecordsSince: &since}, true, float64(3), since.Format(time.RFC3339))
	})
}

func assertDropWindowJSON(t *testing.T, status SourceStatus, wantPresent bool, wantCount, wantSince any) {
	t.Helper()
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	count, countPresent := decoded["dropped_records"]
	since, sincePresent := decoded["dropped_records_since"]
	if countPresent != wantPresent || sincePresent != wantPresent {
		t.Fatalf("drop pair presence = (%t, %t), want %t: %s", countPresent, sincePresent, wantPresent, payload)
	}
	if wantPresent && (count != wantCount || since != wantSince) {
		t.Fatalf("drop pair = (%#v, %#v), want (%#v, %#v): %s", count, since, wantCount, wantSince, payload)
	}
}

func TestSourceCoverageStatusDoesNotOverrideHealthyWhenRecovered(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	coverage := SourceCoverage{
		SourceID: "otel-runtime", DeploymentID: "observability/otelcol-contrib",
		State: SourceCoverageHealthy, RecoveredAt: &now,
	}

	status := coverage.Merge(SourceStatus{SourceID: "otel-runtime", Status: "healthy", Reliability: "best_effort"})
	if status.Status != "healthy" || status.Reason != "" || status.DroppedRecords != nil {
		t.Fatalf("expected healthy source status, got %+v", status)
	}
}
