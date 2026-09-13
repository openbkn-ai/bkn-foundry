// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_logs

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"

	"ontology-query/common"
	"ontology-query/interfaces"
)

// Retention of action execution logs (#790). Scheduled actions keep adding executions, so
// finished executions older than the retention period are deleted together with their
// results. Nothing is deleted unless a retention period is configured.

const (
	envRetentionCleanupOnly = "ACTION_EXECUTION_LOG_CLEANUP_ONLY"
	envRetentionDays        = "ACTION_EXECUTION_LOG_RETENTION_DAYS"
	envCleanupBatchSize     = "ACTION_EXECUTION_LOG_CLEANUP_BATCH_SIZE"
	envCleanupMaxBatches    = "ACTION_EXECUTION_LOG_CLEANUP_MAX_BATCHES"
	envCleanupDryRun        = "ACTION_EXECUTION_LOG_CLEANUP_DRY_RUN"

	defaultCleanupBatchSize  = 500
	defaultCleanupMaxBatches = 20
	maxCleanupBatchSize      = 1000
)

// RetentionConfig controls one cleanup run.
type RetentionConfig struct {
	RetentionDays int  // executions that ended more than this many days ago are deleted; 0 disables cleanup
	BatchSize     int  // executions deleted per batch
	MaxBatches    int  // batches per run; the next run continues where this one stopped
	DryRun        bool // count what would be deleted without deleting anything
}

// RetentionResult reports one cleanup run.
type RetentionResult struct {
	Cutoff     int64 // executions that ended before this time (Unix ms) were eligible
	Executions int64 // executions deleted, or that would be deleted in a dry run
	Results    int64 // documents deleted from the results index, or that would be
	Batches    int
	Truncated  bool // stopped at MaxBatches while more executions were eligible
}

// RetentionCleanupOnly reports whether the process should run one retention cleanup and exit,
// as the retention CronJob does.
func RetentionCleanupOnly() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(envRetentionCleanupOnly)), "true")
}

// LoadRetentionConfig reads the cleanup configuration from the environment. A missing or zero
// retention period disables cleanup.
func LoadRetentionConfig() (RetentionConfig, error) {
	cfg := RetentionConfig{BatchSize: defaultCleanupBatchSize, MaxBatches: defaultCleanupMaxBatches}
	var err error
	if cfg.RetentionDays, err = nonNegativeEnv(envRetentionDays, 0); err != nil {
		return cfg, err
	}
	if cfg.BatchSize, err = nonNegativeEnv(envCleanupBatchSize, defaultCleanupBatchSize); err != nil {
		return cfg, err
	}
	if cfg.MaxBatches, err = nonNegativeEnv(envCleanupMaxBatches, defaultCleanupMaxBatches); err != nil {
		return cfg, err
	}
	if cfg.BatchSize == 0 || cfg.BatchSize > maxCleanupBatchSize {
		return cfg, fmt.Errorf("%s must be between 1 and %d", envCleanupBatchSize, maxCleanupBatchSize)
	}
	if cfg.MaxBatches == 0 {
		return cfg, fmt.Errorf("%s must be at least 1", envCleanupMaxBatches)
	}
	cfg.DryRun = strings.EqualFold(strings.TrimSpace(os.Getenv(envCleanupDryRun)), "true")
	return cfg, nil
}

func nonNegativeEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer, got %q", name, raw)
	}
	return n, nil
}

// CleanupExpiredExecutions runs one retention cleanup over every knowledge network.
func CleanupExpiredExecutions(ctx context.Context, appSetting *common.AppSetting, cfg RetentionConfig) (*RetentionResult, error) {
	svc, ok := NewActionLogsService(appSetting).(*actionLogsService)
	if !ok {
		return nil, fmt.Errorf("unexpected action logs service implementation")
	}
	return svc.cleanupExpired(ctx, cfg, time.Now())
}

// cleanupExpired deletes, batch by batch, finished executions that ended before the cutoff:
// first their documents in the results index, then the executions themselves, so a run that
// stops midway leaves executions that the next run selects again. Pending and running
// executions are never selected, however old.
func (s *actionLogsService) cleanupExpired(ctx context.Context, cfg RetentionConfig, now time.Time) (*RetentionResult, error) {
	result := &RetentionResult{}
	if cfg.RetentionDays <= 0 {
		return result, nil
	}

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CleanupExpiredExecutions")
	defer span.End()

	result.Cutoff = now.Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour).UnixMilli()
	span.SetAttributes(
		attr.Key("retention_days").Int(cfg.RetentionDays),
		attr.Key("cutoff").Int64(result.Cutoff),
		attr.Key("dry_run").Bool(cfg.DryRun),
	)

	expired := expiredExecutionsQuery(result.Cutoff)
	executionIndices := interfaces.ActionExecutionIndexPrefix + "*"
	var searchAfter []any

	for result.Batches < cfg.MaxBatches {
		query := map[string]any{
			"query":   expired,
			"size":    cfg.BatchSize,
			"_source": []string{"id"},
			"sort": []map[string]any{
				{"start_time": map[string]any{"order": "asc"}},
				{"id": map[string]any{"order": "asc"}},
			},
		}
		if len(searchAfter) > 0 {
			query["search_after"] = searchAfter
		}
		hits, err := s.osAccess.SearchData(ctx, executionIndices, query)
		if err != nil {
			return result, fmt.Errorf("failed to select expired executions: %w", err)
		}
		if len(hits) == 0 {
			break
		}

		ids := make([]string, 0, len(hits))
		for _, hit := range hits {
			if id, ok := hit.Source["id"].(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
		searchAfter = hits[len(hits)-1].Sort
		result.Batches++

		resultsQuery := map[string]any{"query": map[string]any{"terms": map[string]any{"execution_id": ids}}}
		if cfg.DryRun {
			n, err := s.countResults(ctx, resultsQuery)
			if err != nil {
				return result, err
			}
			result.Results += n
			result.Executions += int64(len(ids))
		} else {
			n, err := s.osAccess.DeleteByQuery(ctx, interfaces.ActionExecutionResultsIndex, resultsQuery)
			result.Results += n
			if err != nil {
				return result, fmt.Errorf("failed to delete results of expired executions: %w", err)
			}
			// The expiry filter is repeated so only executions still eligible are deleted.
			n, err = s.osAccess.DeleteByQuery(ctx, executionIndices, map[string]any{
				"query": map[string]any{"bool": map[string]any{"filter": []map[string]any{
					{"terms": map[string]any{"id": ids}},
					expired,
				}}},
			})
			result.Executions += n
			if err != nil {
				return result, fmt.Errorf("failed to delete expired executions: %w", err)
			}
		}

		if len(hits) < cfg.BatchSize {
			break
		}
		if result.Batches == cfg.MaxBatches {
			result.Truncated = true
		}
	}

	span.SetAttributes(
		attr.Key("executions").Int64(result.Executions),
		attr.Key("results").Int64(result.Results),
		attr.Key("batches").Int(result.Batches),
	)
	return result, nil
}

// expiredExecutionsQuery matches finished executions that ended before cutoff. A finished
// execution without an end time falls back to its start time.
func expiredExecutionsQuery(cutoff int64) map[string]any {
	return map[string]any{"bool": map[string]any{
		"filter": []map[string]any{
			{"terms": map[string]any{"status": []string{
				interfaces.ExecutionStatusCompleted,
				interfaces.ExecutionStatusFailed,
				interfaces.ExecutionStatusCancelled,
			}}},
		},
		"should": []map[string]any{
			{"range": map[string]any{"end_time": map[string]any{"lt": cutoff}}},
			{"bool": map[string]any{
				"must_not": []map[string]any{{"exists": map[string]any{"field": "end_time"}}},
				"filter":   []map[string]any{{"range": map[string]any{"start_time": map[string]any{"lt": cutoff}}}},
			}},
		},
		"minimum_should_match": 1,
	}}
}

func (s *actionLogsService) countResults(ctx context.Context, query map[string]any) (int64, error) {
	exists, err := s.resultsIndexExists(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to check results index: %w", err)
	}
	if !exists {
		return 0, nil
	}
	countBytes, err := s.osAccess.Count(ctx, interfaces.ActionExecutionResultsIndex, query)
	if err != nil {
		return 0, fmt.Errorf("failed to count results of expired executions: %w", err)
	}
	var count struct {
		Count int64 `json:"count"`
	}
	if err := sonic.Unmarshal(countBytes, &count); err != nil {
		return 0, fmt.Errorf("failed to parse result count: %w", err)
	}
	return count.Count, nil
}
