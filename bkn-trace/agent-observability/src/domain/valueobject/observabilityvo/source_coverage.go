package observabilityvo

import "time"

const (
	SourceCoverageHealthy  = "healthy"
	SourceCoverageDegraded = "degraded"
)

// SourceCoverage is the durable collection-coverage fact for a log source.
// It intentionally excludes log payloads and business data.
type SourceCoverage struct {
	SourceID        string
	DeploymentID    string
	State           string
	Reason          string
	DroppedRecords  int64
	FirstObservedAt time.Time
	LastObservedAt  time.Time
	RecoveredAt     *time.Time
	Version         uint64
}

// Merge applies coverage completeness without changing source availability.
func (coverage SourceCoverage) Merge(status SourceStatus) SourceStatus {
	if coverage.SourceID == "" || coverage.SourceID != status.SourceID || coverage.State != SourceCoverageDegraded {
		return status
	}
	dropped := coverage.DroppedRecords
	status.Status = SourceCoverageDegraded
	status.Reason = coverage.Reason
	status.DroppedRecords = &dropped
	status.CountAccuracy = "partial"
	return status
}
