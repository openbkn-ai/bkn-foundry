// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_logs

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"

	"ontology-query/interfaces"
)

// Per-instance results live in their own index, one document per result, so that an
// execution record stays small and results are paginated by OpenSearch (#790).

// resultsBulkSize bounds one bulk request when storing results.
const resultsBulkSize = 500

// resultsIndexBody indexes only the fields results are filtered and sorted by. Everything
// else, including arbitrary tool output, is kept in _source without being mapped, so a
// result can never add fields to the mapping or conflict with another result's types.
var resultsIndexBody = map[string]any{
	"settings": map[string]any{
		"number_of_shards":   1,
		"number_of_replicas": 0,
	},
	"mappings": map[string]any{
		"dynamic": false,
		"properties": map[string]any{
			"kn_id":        map[string]any{"type": "keyword"},
			"execution_id": map[string]any{"type": "keyword"},
			"seq":          map[string]any{"type": "integer"},
			"status":       map[string]any{"type": "keyword"},
		},
	},
}

// storedExecutionResult is one result document in the results index.
type storedExecutionResult struct {
	KNID        string `json:"kn_id"`
	ExecutionID string `json:"execution_id"`
	Seq         int    `json:"seq"` // position of the result within its execution
	interfaces.ObjectExecutionResult
}

func resultDocumentID(execID string, seq int) string {
	return fmt.Sprintf("%s_%d", execID, seq)
}

// AppendResults stores results in the results index in bulk batches.
func (s *actionLogsService) AppendResults(ctx context.Context, knID, execID string, firstSeq int,
	results []interfaces.ObjectExecutionResult) error {
	if len(results) == 0 {
		return nil
	}

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AppendResults")
	defer span.End()

	span.SetAttributes(
		attr.Key("execution_id").String(execID),
		attr.Key("kn_id").String(knID),
		attr.Key("result_count").Int(len(results)),
	)

	if err := s.ensureResultsIndex(ctx); err != nil {
		return fmt.Errorf("failed to ensure results index: %w", err)
	}

	for start := 0; start < len(results); start += resultsBulkSize {
		end := min(start+resultsBulkSize, len(results))
		docs := make([]interfaces.BulkDocument, 0, end-start)
		for i := start; i < end; i++ {
			seq := firstSeq + i
			docs = append(docs, interfaces.BulkDocument{
				ID: resultDocumentID(execID, seq),
				Body: storedExecutionResult{
					KNID:                  knID,
					ExecutionID:           execID,
					Seq:                   seq,
					ObjectExecutionResult: results[i],
				},
			})
		}
		if err := s.osAccess.BulkIndexDocuments(ctx, interfaces.ActionExecutionResultsIndex, docs); err != nil {
			return fmt.Errorf("failed to store results of execution %s: %w", execID, err)
		}
	}
	return nil
}

// QueryResults returns one page of an execution's results.
func (s *actionLogsService) QueryResults(ctx context.Context, query *interfaces.ActionResultsQuery) (*interfaces.ActionExecutionResultList, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "QueryResults")
	defer span.End()

	span.SetAttributes(
		attr.Key("execution_id").String(query.LogID),
		attr.Key("kn_id").String(query.KNID),
	)

	// The execution must exist in this knowledge network. Its full document is read because
	// executions recorded before #790 carry their results inline.
	exec, err := s.searchExecution(ctx, query.KNID, query.LogID, nil)
	if err != nil {
		return nil, err
	}
	return s.pageResults(ctx, query.KNID, exec, query.Status, query.Offset, query.Limit)
}

// pageResults returns one page of an execution's results. An execution whose document still
// embeds its results was recorded before #790 and is paged in memory; every other execution is
// paged by OpenSearch from the results index.
func (s *actionLogsService) pageResults(ctx context.Context, knID string, exec *interfaces.ActionExecution,
	status string, offset, limit int) (*interfaces.ActionExecutionResultList, error) {
	if offset < 0 {
		offset = 0
	}
	if offset+limit > interfaces.MaxActionResultsWindow {
		return nil, interfaces.ErrActionResultsWindowExceeded
	}
	if len(exec.Results) > 0 {
		return pageEmbeddedResults(exec.Results, status, offset, limit), nil
	}
	return s.queryResultsIndex(ctx, knID, exec.ID, status, offset, limit)
}

func pageEmbeddedResults(results []interfaces.ObjectExecutionResult, status string, offset, limit int) *interfaces.ActionExecutionResultList {
	if status != "" {
		filtered := make([]interfaces.ObjectExecutionResult, 0, len(results))
		for _, r := range results {
			if r.Status == status {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	page := &interfaces.ActionExecutionResultList{
		Entries:    []interfaces.ObjectExecutionResult{},
		TotalCount: len(results),
	}
	if offset < len(results) {
		page.Entries = results[offset:min(offset+limit, len(results))]
	}
	return page
}

func (s *actionLogsService) queryResultsIndex(ctx context.Context, knID, execID, status string,
	offset, limit int) (*interfaces.ActionExecutionResultList, error) {
	page := &interfaces.ActionExecutionResultList{Entries: []interfaces.ObjectExecutionResult{}}

	exists, err := s.resultsIndexExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check results index: %w", err)
	}
	if !exists {
		// No execution has stored results since the upgrade.
		return page, nil
	}

	filters := []map[string]any{
		{"term": map[string]any{"kn_id": knID}},
		{"term": map[string]any{"execution_id": execID}},
	}
	if status != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"status": status}})
	}
	query := map[string]any{"bool": map[string]any{"filter": filters}}

	if limit > 0 {
		hits, err := s.osAccess.SearchData(ctx, interfaces.ActionExecutionResultsIndex, map[string]any{
			"query": query,
			"sort":  []map[string]any{{"seq": map[string]any{"order": "asc"}}},
			"from":  offset,
			"size":  limit,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query results of execution %s: %w", execID, err)
		}
		for _, hit := range hits {
			result, err := decodeStoredResult(hit.Source)
			if err != nil {
				return nil, fmt.Errorf("failed to parse result of execution %s: %w", execID, err)
			}
			page.Entries = append(page.Entries, result)
		}
	}

	countBytes, err := s.osAccess.Count(ctx, interfaces.ActionExecutionResultsIndex, map[string]any{"query": query})
	if err != nil {
		return nil, fmt.Errorf("failed to count results of execution %s: %w", execID, err)
	}
	var countResult struct {
		Count int `json:"count"`
	}
	if err := sonic.Unmarshal(countBytes, &countResult); err != nil {
		return nil, fmt.Errorf("failed to parse result count of execution %s: %w", execID, err)
	}
	page.TotalCount = countResult.Count
	return page, nil
}

func decodeStoredResult(source map[string]any) (interfaces.ObjectExecutionResult, error) {
	data, err := sonic.Marshal(source)
	if err != nil {
		return interfaces.ObjectExecutionResult{}, err
	}
	var stored storedExecutionResult
	if err := sonic.Unmarshal(data, &stored); err != nil {
		return interfaces.ObjectExecutionResult{}, err
	}
	return stored.ObjectExecutionResult, nil
}

// ensureResultsIndex creates the results index on first use; later calls cost nothing.
func (s *actionLogsService) ensureResultsIndex(ctx context.Context) error {
	if s.resultsIndexReady.Load() {
		return nil
	}
	if err := s.createIndexIfMissing(ctx, interfaces.ActionExecutionResultsIndex, resultsIndexBody); err != nil {
		return err
	}
	s.resultsIndexReady.Store(true)
	return nil
}

func (s *actionLogsService) resultsIndexExists(ctx context.Context) (bool, error) {
	if s.resultsIndexReady.Load() {
		return true, nil
	}
	exists, err := s.osAccess.IndexExists(ctx, interfaces.ActionExecutionResultsIndex)
	if err != nil {
		return false, err
	}
	if exists {
		s.resultsIndexReady.Store(true)
	}
	return exists, nil
}
