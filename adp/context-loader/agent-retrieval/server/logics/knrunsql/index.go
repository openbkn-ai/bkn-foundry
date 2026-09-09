// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knrunsql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const (
	// DefaultRowLimit is the rows one run_sql call returns when the caller sets no limit.
	//
	// The previous fixed cap was Vega's own 10000, which is a transport bound, not a
	// context budget: a SELECT * on a mid-sized table came back whole and landed in
	// the model's context. 200 rows is enough to see the shape of a result set and
	// far more than an aggregate needs; a caller that wants the rest pages with
	// offset, and one that wants everything should have written the aggregate.
	DefaultRowLimit = 200
	// MaxRowLimit is Vega's single-shot page bound.
	MaxRowLimit = 10000
)

var (
	// ErrSQLRequired sql input parameter is empty.
	ErrSQLRequired = errors.New("sql is required")
	// ErrNoResourcePlaceholder SQL did not reference any data resource through the {{.resource_id}} placeholder.
	ErrNoResourcePlaceholder = errors.New("sql must reference at least one data resource via the {{.resource_id}} placeholder")
	// ErrInvalidLimit limit is outside 1..MaxRowLimit.
	ErrInvalidLimit = fmt.Errorf("limit must be between 1 and %d", MaxRowLimit)
	// ErrInvalidOffset offset is negative.
	ErrInvalidOffset  = errors.New("offset must be zero or positive")
	emitRunSQLFailure = bkntrace.EmitRunSQLFailure
)

// RunSQLReq run_sql input (shared by MCP tools and internal REST endpoints).
type RunSQLReq struct {
	SQL          string `json:"sql"`           // MySQL dialect SQL, the table name uses {{.resource_id}} placeholder.
	QueryTimeout int    `json:"query_timeout"` // Query timeout (seconds), optional.
	// Limit caps the rows returned by this call: 1..MaxRowLimit, default DefaultRowLimit.
	// It applies on top of any LIMIT inside the SQL.
	Limit int `json:"limit"`
	// Offset skips that many rows of the result set; pair it with the next_offset the
	// previous page returned.
	Offset int `json:"offset"`
}

// pageBounds resolves the request's limit/offset to what is sent to Vega. It
// asks for one row beyond the page so a full page can be told apart from a
// result that happened to end on the boundary — except at MaxRowLimit, where
// Vega refuses a larger page and the caller is at the transport bound anyway.
func (r *RunSQLReq) pageBounds() (limit, probe, offset int, err error) {
	limit = r.Limit
	if limit == 0 {
		limit = DefaultRowLimit
	}
	if limit < 1 || limit > MaxRowLimit {
		return 0, 0, 0, ErrInvalidLimit
	}
	if r.Offset < 0 {
		return 0, 0, 0, ErrInvalidOffset
	}
	probe = limit + 1
	if probe > MaxRowLimit {
		probe = MaxRowLimit
	}
	return limit, probe, r.Offset, nil
}

// KnRunSQLService executes read-only SQL (forced SELECT-only) on data resources mounted on the knowledge network.
type KnRunSQLService interface {
	RunSQL(ctx context.Context, req *RunSQLReq) (*interfaces.VegaRawQueryResp, error)
}

type knRunSQLService struct {
	vega interfaces.DrivenVega
}

var (
	once     sync.Once
	instance KnRunSQLService
)

// NewKnRunSQLService create KnRunSQLService singleton.
func NewKnRunSQLService() KnRunSQLService {
	once.Do(func() {
		instance = &knRunSQLService{
			vega: drivenadapters.NewVegaAccess(),
		}
	})
	return instance
}

// NewKnRunSQLServiceWith injection dependency creation (for testing).
func NewKnRunSQLServiceWith(vega interfaces.DrivenVega) KnRunSQLService {
	return &knRunSQLService{vega: vega}
}

// RunSQL Guard → Extract resource_id → Adjust Vega by fixed Raw Query contract.
func (s *knRunSQLService) RunSQL(ctx context.Context, req *RunSQLReq) (*interfaces.VegaRawQueryResp, error) {
	if req == nil || strings.TrimSpace(req.SQL) == "" {
		sql := ""
		if req != nil {
			sql = req.SQL
		}
		emitRunSQLFailure(ctx, nil, sql, nil, bkntrace.RunSQLFailure{
			Stage: "input_validation", Code: "RUN_SQL_SQL_REQUIRED", Summary: ErrSQLRequired.Error(),
		})
		return nil, ErrSQLRequired
	}

	// Read-only guard: Deny writing to /DDL/multiple statements.
	if err := EnsureReadOnlySQL(req.SQL); err != nil {
		emitRunSQLFailure(ctx, nil, req.SQL, ExtractResourceIDs(req.SQL), bkntrace.RunSQLFailure{
			Stage: "sql_guard", Code: "RUN_SQL_READ_ONLY_REJECTED", Summary: err.Error(),
		})
		return nil, err
	}

	// The resource must be referenced through the {{.resource_id}} placeholder, otherwise vega cannot locate the data source.
	resourceIDs := ExtractResourceIDs(req.SQL)
	if len(resourceIDs) == 0 {
		emitRunSQLFailure(ctx, nil, req.SQL, nil, bkntrace.RunSQLFailure{
			Stage: "input_validation", Code: "RUN_SQL_RESOURCE_PLACEHOLDER_REQUIRED", Summary: ErrNoResourcePlaceholder.Error(),
		})
		return nil, ErrNoResourcePlaceholder
	}

	limit, probe, offset, err := req.pageBounds()
	if err != nil {
		emitRunSQLFailure(ctx, nil, req.SQL, resourceIDs, bkntrace.RunSQLFailure{
			Stage: "input_validation", Code: "RUN_SQL_PAGE_INVALID", Summary: err.Error(),
		})
		return nil, err
	}

	resp, err := s.vega.RawQuery(ctx, &interfaces.VegaRawQueryReq{
		Query:           req.SQL,
		QueryFormat:     "sql",
		InputDialect:    "mysql",
		QueryTimeoutSec: req.QueryTimeout,
		Paging: interfaces.VegaPagingRequest{
			Mode:   "single",
			Offset: offset,
			Limit:  probe,
		},
	})
	if err != nil {
		emitRunSQLFailure(ctx, nil, req.SQL, resourceIDs, bkntrace.RunSQLFailure{
			Stage: "vega_query", Code: "RUN_SQL_VEGA_QUERY_FAILED", Summary: err.Error(),
		})
		return nil, err
	}
	capRowsToPage(resp, limit, offset)
	bkntrace.EmitRunSQLEvents(ctx, nil, req.SQL, resourceIDs, resp)
	return resp, nil
}

// capRowsToPage trims the probe row and marks the page as partial. The marker
// is both structured (next_offset) and spoken (a warning): a caller that reads
// only the rows must still learn that they are not all of them, or it will
// report a count of 200 as the answer.
func capRowsToPage(resp *interfaces.VegaRawQueryResp, limit, offset int) {
	if resp == nil || len(resp.Entries) <= limit {
		return
	}
	resp.Entries = resp.Entries[:limit]
	next := offset + limit
	resp.NextOffset = &next
	resp.Warnings = append(resp.Warnings, fmt.Sprintf(
		"rows capped at %d starting from offset %d; more rows exist. Pass offset=%d to read the next page, or narrow the query (WHERE / aggregation / LIMIT) instead of paging through everything",
		limit, offset, next))
}
