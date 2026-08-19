// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knrunsql

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

var (
	// ErrSQLRequired sql input parameter is empty.
	ErrSQLRequired = errors.New("sql is required")
	// ErrNoResourcePlaceholder SQL did not reference any data resource through the {{.resource_id}} placeholder.
	ErrNoResourcePlaceholder = errors.New("sql must reference at least one data resource via the {{.resource_id}} placeholder")
	emitRunSQLFailure        = bkntrace.EmitRunSQLFailure
)

// RunSQLReq run_sql input (shared by MCP tools and internal REST endpoints).
type RunSQLReq struct {
	SQL          string `json:"sql"`           // MySQL dialect SQL, the table name uses {{.resource_id}} placeholder.
	QueryTimeout int    `json:"query_timeout"` // Query timeout (seconds), optional.
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

	resp, err := s.vega.RawQuery(ctx, &interfaces.VegaRawQueryReq{
		Query:           req.SQL,
		QueryFormat:     "sql",
		InputDialect:    "mysql",
		QueryTimeoutSec: req.QueryTimeout,
		Paging: interfaces.VegaPagingRequest{
			Mode:  "single",
			Limit: 10000,
		},
	})
	if err != nil {
		emitRunSQLFailure(ctx, nil, req.SQL, resourceIDs, bkntrace.RunSQLFailure{
			Stage: "vega_query", Code: "RUN_SQL_VEGA_QUERY_FAILED", Summary: err.Error(),
		})
		return nil, err
	}
	bkntrace.EmitRunSQLEvents(ctx, nil, req.SQL, resourceIDs, resp)
	return resp, nil
}
