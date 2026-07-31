package iprojectionrebuild

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

type Source interface {
	ProjectionHighWatermark(ctx context.Context) (uint64, error)
	ScanAuthoritativeProjection(
		ctx context.Context,
		afterAggregateType string,
		afterAggregateID string,
		limit int,
	) ([]iprojectionoutbox.Item, error)
	CountAuthoritativeProjection(ctx context.Context) (uint64, error)
	ScanProjectionHistory(ctx context.Context, after, through uint64, limit int) ([]iprojectionoutbox.Item, error)
	LoadProjectionCheckpoint(ctx context.Context, projectorID, indexVersion string) (uint64, error)
	SaveProjectionCheckpoint(ctx context.Context, projectorID, indexVersion string, lastOutboxID uint64) error
}

type Target interface {
	PrepareVersion(ctx context.Context, indexVersion string) error
	ProjectVersion(ctx context.Context, indexVersion string, item iprojectionoutbox.Item) error
	ValidateVersion(
		ctx context.Context,
		indexVersion string,
		items []iprojectionoutbox.Item,
	) error
	CountVersion(ctx context.Context, indexVersion string) (uint64, error)
	SwitchAlias(ctx context.Context, alias, indexVersion string) error
}
