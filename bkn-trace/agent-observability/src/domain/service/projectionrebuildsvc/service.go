package projectionrebuildsvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionrebuild"
)

var ErrProjectionValidation = errors.New("rebuilt projection does not match authoritative state")

type Options struct {
	BatchSize int
}

type Service struct {
	source  iprojectionrebuild.Source
	target  iprojectionrebuild.Target
	options Options
}

type Result struct {
	IndexVersion   string
	LastOutboxID   uint64
	ProjectedCount uint64
}

func New(source iprojectionrebuild.Source, target iprojectionrebuild.Target, options Options) *Service {
	if options.BatchSize <= 0 {
		options.BatchSize = 500
	}
	return &Service{source: source, target: target, options: options}
}

func (s *Service) Rebuild(ctx context.Context, projectorID, alias, indexVersion string) (Result, error) {
	if projectorID == "" || alias == "" || indexVersion == "" {
		return Result{}, errors.New("projector ID, alias and index version are required")
	}
	if err := s.target.PrepareVersion(ctx, indexVersion); err != nil {
		return Result{}, err
	}
	rebuildStart, err := s.source.ProjectionHighWatermark(ctx)
	if err != nil {
		return Result{}, err
	}
	afterType, afterID := "", ""
	for {
		items, err := s.source.ScanAuthoritativeProjection(
			ctx, afterType, afterID, s.options.BatchSize,
		)
		if err != nil {
			return Result{}, err
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			if err := s.target.ProjectVersion(ctx, indexVersion, item); err != nil {
				return Result{}, err
			}
		}
		if err := s.target.ValidateVersion(ctx, indexVersion, items); err != nil {
			return Result{}, fmt.Errorf("validate authoritative projection: %w", err)
		}
		last := items[len(items)-1]
		afterType, afterID = last.AggregateType, last.AggregateID
	}
	checkpoint := rebuildStart
	if err := s.source.SaveProjectionCheckpoint(
		ctx, projectorID, indexVersion, checkpoint,
	); err != nil {
		return Result{}, err
	}
	highWatermark := rebuildStart
	for {
		for checkpoint < highWatermark {
			items, err := s.source.ScanProjectionHistory(
				ctx, checkpoint, highWatermark, s.options.BatchSize,
			)
			if err != nil {
				return Result{}, err
			}
			if len(items) == 0 {
				return Result{}, ErrProjectionValidation
			}
			for _, item := range items {
				if err := s.target.ProjectVersion(ctx, indexVersion, item); err != nil {
					return Result{}, err
				}
				checkpoint = item.ID
			}
			if err := s.target.ValidateVersion(ctx, indexVersion, items); err != nil {
				return Result{}, fmt.Errorf("validate projection history: %w", err)
			}
			if err := s.source.SaveProjectionCheckpoint(
				ctx, projectorID, indexVersion, checkpoint,
			); err != nil {
				return Result{}, err
			}
		}

		latestHighWatermark, err := s.source.ProjectionHighWatermark(ctx)
		if err != nil {
			return Result{}, err
		}
		if latestHighWatermark > highWatermark {
			highWatermark = latestHighWatermark
			continue
		}

		expected, err := s.source.CountAuthoritativeProjection(ctx)
		if err != nil {
			return Result{}, err
		}
		actual, err := s.target.CountVersion(ctx, indexVersion)
		if err != nil {
			return Result{}, err
		}
		if actual != expected {
			return Result{}, ErrProjectionValidation
		}

		latestHighWatermark, err = s.source.ProjectionHighWatermark(ctx)
		if err != nil {
			return Result{}, err
		}
		if latestHighWatermark > highWatermark {
			highWatermark = latestHighWatermark
			continue
		}
		if err := s.target.SwitchAlias(ctx, alias, indexVersion); err != nil {
			return Result{}, err
		}
		return Result{
			IndexVersion: indexVersion, LastOutboxID: highWatermark,
			ProjectedCount: actual,
		}, nil
	}
}
