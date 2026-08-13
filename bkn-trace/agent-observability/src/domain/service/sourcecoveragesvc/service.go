package sourcecoveragesvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isourcecoveragestore"
)

var ErrVersionConflict = errors.New("source coverage version conflict")

type Snapshot struct {
	RefusedLogs   int64
	FailedLogs    int64
	QueueSize     int64
	QueueCapacity int64
}

type Reader interface {
	Read(ctx context.Context) (Snapshot, error)
}

type Options struct {
	SourceID     string
	DeploymentID string
}

type Service struct {
	store          isourcecoveragestore.Store
	reader         Reader
	options        Options
	previous       Snapshot
	hasPrevious    bool
	healthySamples int
}

func New(store isourcecoveragestore.Store, reader Reader, options Options) *Service {
	return &Service{store: store, reader: reader, options: options}
}

// Observe records a loss signal immediately. Recovery needs two independent,
// drained samples so a transient exporter recovery cannot overstate coverage.
func (service *Service) Observe(ctx context.Context) error {
	if service.store == nil || service.reader == nil || service.options.SourceID == "" || service.options.DeploymentID == "" {
		return errors.New("source coverage monitor is not configured")
	}
	snapshot, err := service.reader.Read(ctx)
	if err != nil {
		return fmt.Errorf("read collector coverage metrics: %w", err)
	}
	if !service.hasPrevious {
		service.previous = snapshot
		service.hasPrevious = true
		return nil
	}
	dropped := counterDelta(snapshot.RefusedLogs, service.previous.RefusedLogs) +
		counterDelta(snapshot.FailedLogs, service.previous.FailedLogs)
	service.previous = snapshot
	service.hasPrevious = true
	coverage, found, err := service.store.Get(ctx, service.options.SourceID, service.options.DeploymentID)
	if err != nil {
		return fmt.Errorf("load source coverage: %w", err)
	}
	if dropped > 0 {
		service.healthySamples = 0
		if !found {
			coverage = observabilityvo.SourceCoverage{
				SourceID: service.options.SourceID, DeploymentID: service.options.DeploymentID,
				FirstObservedAt: time.Now().UTC(),
			}
		}
		coverage.State = observabilityvo.SourceCoverageDegraded
		coverage.Reason = "telemetry_dropped"
		coverage.DroppedRecords += dropped
		coverage.LastObservedAt = time.Now().UTC()
		return service.store.UpsertDegraded(ctx, coverage)
	}
	if !found || coverage.State != observabilityvo.SourceCoverageDegraded {
		service.healthySamples = 0
		return nil
	}
	service.healthySamples++
	if service.healthySamples < 2 {
		return nil
	}
	service.healthySamples = 0
	if err := service.store.MarkHealthyAfterCatchUp(ctx, coverage.SourceID, coverage.DeploymentID, coverage.Version); err != nil {
		return fmt.Errorf("mark source coverage healthy: %w", err)
	}
	return nil
}

func counterDelta(current, previous int64) int64 {
	if current < 0 {
		return 0
	}
	if current >= previous {
		return current - previous
	}
	// A Collector restart resets counters. Any post-reset nonzero value is still
	// a new loss signal; a zero value is neutral and cannot clear durable state.
	return current
}
