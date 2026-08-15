package archivesvc

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

// Router keeps the two archive populations isolated while preserving one small
// archive workflow and task store.
type Router struct {
	Log   Source
	Trace Source
}

// TraceBundleSource keeps the core Interaction package and its OpenSearch
// technical traces in one frozen archive unit.  It never recalculates a time
// window during cleanup: the enriched candidate is the cleanup contract.
type TraceBundleSource struct {
	Core      Source
	Technical TraceTechnicalStore
}

type TraceTechnicalStore interface {
	Enrich(context.Context, []Candidate) ([]Candidate, error)
	Purge(context.Context, []Candidate) error
}

func (source TraceBundleSource) Freeze(ctx context.Context, kind observabilityvo.ArchiveKind, tenantID string, archiveRange observabilityvo.ArchiveRange) ([]Candidate, error) {
	candidates, err := source.Core.Freeze(ctx, kind, tenantID, archiveRange)
	if err != nil || len(candidates) == 0 || source.Technical == nil {
		return candidates, err
	}
	return source.Technical.Enrich(ctx, candidates)
}
func (source TraceBundleSource) Purge(ctx context.Context, kind observabilityvo.ArchiveKind, tenantID string, candidates []Candidate) error {
	if source.Technical != nil {
		if err := source.Technical.Purge(ctx, candidates); err != nil {
			return err
		}
	}
	return source.Core.Purge(ctx, kind, tenantID, candidates)
}

func (router Router) source(kind observabilityvo.ArchiveKind) (Source, error) {
	if kind == observabilityvo.ArchiveKindLog && router.Log != nil {
		return router.Log, nil
	}
	if kind == observabilityvo.ArchiveKindTrace && router.Trace != nil {
		return router.Trace, nil
	}
	return nil, fmt.Errorf("archive source for %s is unavailable", kind)
}

func (router Router) Freeze(ctx context.Context, kind observabilityvo.ArchiveKind, tenantID string, archiveRange observabilityvo.ArchiveRange) ([]Candidate, error) {
	source, err := router.source(kind)
	if err != nil {
		return nil, err
	}
	return source.Freeze(ctx, kind, tenantID, archiveRange)
}
func (router Router) Purge(ctx context.Context, kind observabilityvo.ArchiveKind, tenantID string, candidates []Candidate) error {
	source, err := router.source(kind)
	if err != nil {
		return err
	}
	return source.Purge(ctx, kind, tenantID, candidates)
}
