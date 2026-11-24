// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_logs

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

var indexSyncMapping = map[string]any{"properties": map[string]any{
	"index_sync": map[string]any{"dynamic": false, "properties": map[string]any{
		"status":          map[string]any{"type": "keyword"},
		"next_attempt_at": map[string]any{"type": "long"},
		"lease_until":     map[string]any{"type": "long"},
	}},
}}

const claimIndexSyncScript = `def s = ctx._source.index_sync;
if (s != null && params.states.contains(s.status) && s.revision == params.revision && s.lease_until <= params.now && s.next_attempt_at <= params.now) {
 s.lease_token = params.token; s.lease_until = params.until; s.revision += 1;
} else { ctx.op = 'noop'; }`
const saveIndexSyncScript = `def s = ctx._source.index_sync;
if (s != null && s.lease_token == params.token && s.revision == params.revision) {
 ctx._source.index_sync = params.state;
} else { ctx.op = 'noop'; }`
const retryIndexSyncScript = `def s = ctx._source.index_sync;
if (s != null && s.status == 'failed' && s.lease_until <= params.now) {
 s.status = 'pending'; s.attempts = 0; s.error = ''; s.next_attempt_at = 0; s.lease_until = 0; s.revision += 1;
 for (def task : s.tasks) { if (task.status == 'stopped' || task.status == 'cancelled') { task.status = 'retry_requested'; } }
} else { ctx.op = 'noop'; }`

var _ interfaces.ActionIndexSyncStore = (*actionLogsService)(nil)

// NewIndexSyncStore shares the result store and its established retention policy.
func NewIndexSyncStore(setting *common.AppSetting) interfaces.ActionIndexSyncStore {
	return NewActionLogsService(setting).(*actionLogsService)
}

func (s *actionLogsService) ensureIndexSyncMapping(ctx context.Context) error {
	if s.syncMappingReady.Load() {
		return nil
	}
	exists, err := s.osAccess.IndexExists(ctx, interfaces.ActionExecutionResultsIndex)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := s.osAccess.PutMapping(ctx, interfaces.ActionExecutionResultsIndex, indexSyncMapping); err != nil {
		return err
	}
	s.syncMappingReady.Store(true)
	return nil
}

func (s *actionLogsService) ListIndexSync(ctx context.Context, now int64, limit int) ([]interfaces.ActionIndexSyncJob, error) {
	if err := s.ensureIndexSyncMapping(ctx); err != nil {
		return nil, err
	}
	// A missing results index simply means no invocation has been recorded yet.
	if !s.syncMappingReady.Load() {
		return nil, nil
	}
	hits, err := s.osAccess.SearchData(ctx, interfaces.ActionExecutionResultsIndex, map[string]any{
		"size": limit, "sort": []any{map[string]any{"index_sync.next_attempt_at": "asc"}, map[string]any{"execution_id": "asc"}, map[string]any{"seq": "asc"}},
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"status": interfaces.ObjectStatusSuccess}},
			map[string]any{"terms": map[string]any{"index_sync.status": []string{"pending", "submitted", "retrying"}}},
			map[string]any{"range": map[string]any{"index_sync.next_attempt_at": map[string]any{"lte": now}}},
			map[string]any{"range": map[string]any{"index_sync.lease_until": map[string]any{"lte": now}}},
		}}},
	})
	if err != nil {
		return nil, err
	}
	jobs := make([]interfaces.ActionIndexSyncJob, 0, len(hits))
	for _, hit := range hits {
		data, err := sonic.Marshal(hit.Source)
		if err != nil {
			return nil, err
		}
		var job interfaces.ActionIndexSyncJob
		if err := common.UnmarshalJSON(data, &job); err != nil {
			return nil, err
		}
		if job.Sync == nil || job.KNID == "" || job.ExecutionID == "" || job.Seq < 0 {
			return nil, fmt.Errorf("invalid index synchronization result")
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *actionLogsService) ClaimIndexSync(ctx context.Context, job *interfaces.ActionIndexSyncJob, token string, now, until int64) (bool, error) {
	result, err := s.osAccess.UpdateData(ctx, interfaces.ActionExecutionResultsIndex, resultDocumentID(job.ExecutionID, job.Seq), map[string]any{
		"script": map[string]any{"source": claimIndexSyncScript, "params": map[string]any{
			"states": []string{"pending", "submitted", "retrying"}, "revision": job.Sync.Revision, "now": now, "until": until, "token": token,
		}},
	})
	if err != nil || result != interfaces.UpdateResultUpdated {
		return false, err
	}
	job.Sync.LeaseToken = token
	job.Sync.LeaseUntil = until
	job.Sync.Revision++
	return true, nil
}

func (s *actionLogsService) SaveIndexSync(ctx context.Context, job *interfaces.ActionIndexSyncJob) error {
	state := *job.Sync
	state.Revision++
	result, err := s.osAccess.UpdateData(ctx, interfaces.ActionExecutionResultsIndex, resultDocumentID(job.ExecutionID, job.Seq), map[string]any{
		"script": map[string]any{"source": saveIndexSyncScript, "params": map[string]any{
			"revision": job.Sync.Revision, "token": job.Sync.LeaseToken, "state": state,
		}},
	})
	if err != nil {
		return err
	}
	if result != interfaces.UpdateResultUpdated {
		return fmt.Errorf("index synchronization lease was lost")
	}
	job.Sync.Revision = state.Revision
	return nil
}

func (s *actionLogsService) RetryIndexSync(ctx context.Context, knID, execID string, now int64) (int, error) {
	if err := s.ensureIndexSyncMapping(ctx); err != nil {
		return 0, err
	}
	if !s.syncMappingReady.Load() {
		return 0, nil
	}
	count := 0
	var after []any
	for {
		query := map[string]any{"size": 100, "sort": []any{map[string]any{"seq": "asc"}}, "query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"kn_id": knID}}, map[string]any{"term": map[string]any{"execution_id": execID}},
			map[string]any{"term": map[string]any{"index_sync.status": "failed"}},
		}}}}
		if after != nil {
			query["search_after"] = after
		}
		hits, err := s.osAccess.SearchData(ctx, interfaces.ActionExecutionResultsIndex, query)
		if err != nil {
			return count, err
		}
		for _, hit := range hits {
			data, err := sonic.Marshal(hit.Source)
			if err != nil {
				return count, err
			}
			var job interfaces.ActionIndexSyncJob
			if err := common.UnmarshalJSON(data, &job); err != nil {
				return count, err
			}
			if job.KNID != knID || job.ExecutionID != execID {
				return count, fmt.Errorf("index synchronization result scope mismatch")
			}
			result, err := s.osAccess.UpdateData(ctx, interfaces.ActionExecutionResultsIndex, resultDocumentID(execID, job.Seq), map[string]any{
				"script": map[string]any{"source": retryIndexSyncScript, "params": map[string]any{"now": now}},
			})
			if err != nil {
				return count, err
			}
			if result == interfaces.UpdateResultUpdated {
				count++
			}
		}
		if len(hits) < 100 {
			return count, nil
		}
		after = hits[len(hits)-1].Sort
		if len(after) == 0 {
			return count, fmt.Errorf("index synchronization retry cursor is missing")
		}
	}
}

// A retried result write must preserve a worker's newer synchronization state.
// The successful business result and the initial intent share one document write.
func (s *actionLogsService) storeIndexSyncResult(ctx context.Context, knID, execID string, seq int, result interfaces.ObjectExecutionResult) error {
	if err := s.ensureIndexSyncMapping(ctx); err != nil {
		return err
	}
	document := storedExecutionResult{KNID: knID, ExecutionID: execID, Seq: seq, ObjectExecutionResult: result}
	_, err := s.osAccess.UpdateData(ctx, interfaces.ActionExecutionResultsIndex, resultDocumentID(execID, seq), map[string]any{
		"scripted_upsert": true, "upsert": map[string]any{},
		"script": map[string]any{"source": `if (ctx.op == 'create') { ctx._source = params.document; }
else { def sync = ctx._source.index_sync; ctx._source = params.document; if (sync != null) { ctx._source.index_sync = sync; } }`,
			"params": map[string]any{"document": document}},
	})
	return err
}

// Keep failed synchronization recoverable until it is retried successfully.
// Collapse bounds the lookup to one hit per candidate execution, even when one
// execution changed many rows. The cleanup cursor still advances past held rows.
func (s *actionLogsService) withoutPendingIndexSync(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return ids, nil
	}
	exists, err := s.resultsIndexExists(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return ids, nil
	}
	hits, err := s.osAccess.SearchData(ctx, interfaces.ActionExecutionResultsIndex, map[string]any{
		"size": len(ids), "_source": []string{"execution_id"}, "collapse": map[string]any{"field": "execution_id"},
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"terms": map[string]any{"execution_id": ids}},
			map[string]any{"terms": map[string]any{"index_sync.status": []string{"pending", "submitted", "retrying", "failed"}}},
		}}},
	})
	if err != nil {
		return nil, err
	}
	blocked := make(map[string]bool, len(hits))
	for _, hit := range hits {
		if id, ok := hit.Source["execution_id"].(string); ok {
			blocked[id] = true
		}
	}
	ready := make([]string, 0, len(ids))
	for _, id := range ids {
		if !blocked[id] {
			ready = append(ready, id)
		}
	}
	return ready, nil
}
