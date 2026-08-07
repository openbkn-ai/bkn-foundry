package observabilityvo

import (
	"testing"
	"time"
)

func TestSourceCoverageStatusDisclosesDroppedTelemetry(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	coverage := SourceCoverage{
		SourceID:          "otel-runtime",
		DeploymentID:      "observability/otelcol-contrib",
		State:             SourceCoverageDegraded,
		Reason:            "telemetry_dropped",
		DroppedRecords:    3,
		FirstObservedAt:   now.Add(-time.Minute),
		LastObservedAt:    now,
	}

	status := coverage.Merge(SourceStatus{SourceID: "otel-runtime", Status: "healthy", Reliability: "best_effort"})
	if status.Status != "degraded" || status.Reason != "telemetry_dropped" {
		t.Fatalf("expected durable degradation, got %+v", status)
	}
	if status.DroppedRecords == nil || *status.DroppedRecords != 3 {
		t.Fatalf("expected dropped record count, got %+v", status.DroppedRecords)
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
