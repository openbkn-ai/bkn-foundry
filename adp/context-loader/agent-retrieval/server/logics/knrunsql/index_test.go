// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knrunsql

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type recordingVega struct {
	req  *interfaces.VegaRawQueryReq
	resp *interfaces.VegaRawQueryResp
	err  error
}

func (v *recordingVega) RawQuery(_ context.Context, req *interfaces.VegaRawQueryReq) (*interfaces.VegaRawQueryResp, error) {
	v.req = req
	if v.err != nil {
		return nil, v.err
	}
	if v.resp != nil {
		return v.resp, nil
	}
	return &interfaces.VegaRawQueryResp{}, nil
}

func (v *recordingVega) GetResourceConnectorType(context.Context, string) (string, error) {
	return "postgresql", nil
}

func (v *recordingVega) ListResources(context.Context, *interfaces.VegaListResourcesReq) (*interfaces.VegaListResourcesResp, error) {
	return nil, nil
}

func (v *recordingVega) GetResource(context.Context, string) (*interfaces.VegaResource, error) {
	return nil, nil
}

func TestRunSQLUsesRawQueryContract(t *testing.T) {
	vega := &recordingVega{}
	service := NewKnRunSQLServiceWith(vega)

	_, err := service.RunSQL(context.Background(), &RunSQLReq{
		SQL:          "SELECT * FROM {{.resource1}}",
		QueryTimeout: 30,
	})
	if err != nil {
		t.Fatalf("RunSQL() error = %v", err)
	}
	if vega.req == nil {
		t.Fatal("RawQuery() was not called")
	}
	if vega.req.QueryFormat != "sql" || vega.req.InputDialect != "mysql" || vega.req.QueryTimeoutSec != 30 {
		t.Fatalf("unexpected Raw Query contract: %#v", vega.req)
	}
	// One row beyond the default page, so a full page can be told from the end of the result.
	if vega.req.Paging != (interfaces.VegaPagingRequest{Mode: "single", Limit: DefaultRowLimit + 1}) {
		t.Fatalf("unexpected paging: %#v", vega.req.Paging)
	}
}

func rowsOf(n int) []map[string]any {
	rows := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]any{"id": i})
	}
	return rows
}

func TestRunSQLCapsRowsAndPointsAtTheNextPage(t *testing.T) {
	vega := &recordingVega{resp: &interfaces.VegaRawQueryResp{Entries: rowsOf(4)}}
	service := NewKnRunSQLServiceWith(vega)

	resp, err := service.RunSQL(context.Background(), &RunSQLReq{SQL: "SELECT * FROM {{.r}}", Limit: 3, Offset: 6})
	if err != nil {
		t.Fatalf("RunSQL() error = %v", err)
	}
	if vega.req.Paging != (interfaces.VegaPagingRequest{Mode: "single", Offset: 6, Limit: 4}) {
		t.Fatalf("unexpected paging: %#v", vega.req.Paging)
	}
	if len(resp.Entries) != 3 {
		t.Fatalf("probe row must be trimmed, got %d rows", len(resp.Entries))
	}
	if resp.NextOffset == nil || *resp.NextOffset != 9 {
		t.Fatalf("next_offset = %v, want 9", resp.NextOffset)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "offset=9") {
		t.Fatalf("a cut page must say so in warnings, got %v", resp.Warnings)
	}
}

func TestRunSQLLeavesAFullResultUnmarked(t *testing.T) {
	vega := &recordingVega{resp: &interfaces.VegaRawQueryResp{Entries: rowsOf(3)}}
	service := NewKnRunSQLServiceWith(vega)

	resp, err := service.RunSQL(context.Background(), &RunSQLReq{SQL: "SELECT * FROM {{.r}}", Limit: 3})
	if err != nil {
		t.Fatalf("RunSQL() error = %v", err)
	}
	if len(resp.Entries) != 3 || resp.NextOffset != nil || len(resp.Warnings) != 0 {
		t.Fatalf("a result within the page must not be marked: %#v", resp)
	}
}

func TestRunSQLProbeStopsAtTheTransportBound(t *testing.T) {
	vega := &recordingVega{}
	service := NewKnRunSQLServiceWith(vega)
	if _, err := service.RunSQL(context.Background(), &RunSQLReq{SQL: "SELECT * FROM {{.r}}", Limit: MaxRowLimit}); err != nil {
		t.Fatalf("RunSQL() error = %v", err)
	}
	if vega.req.Paging.Limit != MaxRowLimit {
		t.Fatalf("Vega refuses pages above %d, asked for %d", MaxRowLimit, vega.req.Paging.Limit)
	}
}

func TestRunSQLRejectsBadPageBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  *RunSQLReq
		want error
	}{
		{"limit too large", &RunSQLReq{SQL: "SELECT * FROM {{.r}}", Limit: MaxRowLimit + 1}, ErrInvalidLimit},
		{"negative limit", &RunSQLReq{SQL: "SELECT * FROM {{.r}}", Limit: -1}, ErrInvalidLimit},
		{"negative offset", &RunSQLReq{SQL: "SELECT * FROM {{.r}}", Offset: -1}, ErrInvalidOffset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vega := &recordingVega{}
			_, err := NewKnRunSQLServiceWith(vega).RunSQL(context.Background(), tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if vega.req != nil {
				t.Fatalf("an invalid page must not reach Vega")
			}
		})
	}
}

func TestRunSQLEmitsStructuredFailureAtEachBoundary(t *testing.T) {
	tests := []struct {
		name      string
		req       *RunSQLReq
		vegaError error
		wantStage string
		wantCode  string
	}{
		{name: "sql required", req: &RunSQLReq{}, wantStage: "input_validation", wantCode: "RUN_SQL_SQL_REQUIRED"},
		{name: "read only guard", req: &RunSQLReq{SQL: "DELETE FROM {{.inventory}}"}, wantStage: "sql_guard", wantCode: "RUN_SQL_READ_ONLY_REJECTED"},
		{name: "resource placeholder", req: &RunSQLReq{SQL: "SELECT 1"}, wantStage: "input_validation", wantCode: "RUN_SQL_RESOURCE_PLACEHOLDER_REQUIRED"},
		{name: "vega query", req: &RunSQLReq{SQL: "SELECT * FROM {{.inventory}}"}, vegaError: errors.New("unknown column available_qty"), wantStage: "vega_query", wantCode: "RUN_SQL_VEGA_QUERY_FAILED"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			previous := emitRunSQLFailure
			t.Cleanup(func() { emitRunSQLFailure = previous })
			var got bkntrace.RunSQLFailure
			var gotSQL string
			emitRunSQLFailure = func(_ context.Context, _ interfaces.Logger, sql string, _ []string, failure bkntrace.RunSQLFailure) string {
				gotSQL = sql
				got = failure
				return "event-failure"
			}

			service := NewKnRunSQLServiceWith(&recordingVega{err: test.vegaError})
			if _, err := service.RunSQL(context.Background(), test.req); err == nil {
				t.Fatal("RunSQL() error = nil, want failure")
			}
			if got.Stage != test.wantStage || got.Code != test.wantCode || got.Summary == "" {
				t.Fatalf("failure=%+v, want stage=%s code=%s and summary", got, test.wantStage, test.wantCode)
			}
			if test.req != nil && gotSQL != test.req.SQL {
				t.Fatalf("recorded SQL=%q, want %q", gotSQL, test.req.SQL)
			}
		})
	}
}
