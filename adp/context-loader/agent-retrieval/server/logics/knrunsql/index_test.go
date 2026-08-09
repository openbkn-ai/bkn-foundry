// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knrunsql

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type recordingVega struct {
	req *interfaces.VegaRawQueryReq
	err error
}

func (v *recordingVega) RawQuery(_ context.Context, req *interfaces.VegaRawQueryReq) (*interfaces.VegaRawQueryResp, error) {
	v.req = req
	if v.err != nil {
		return nil, v.err
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
	if vega.req.Paging != (interfaces.VegaPagingRequest{Mode: "single", Limit: 10000}) {
		t.Fatalf("unexpected paging: %#v", vega.req.Paging)
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
