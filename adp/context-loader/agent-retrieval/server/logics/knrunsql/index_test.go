// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knrunsql

import (
	"context"
	"testing"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

type recordingVega struct {
	req *interfaces.VegaRawQueryReq
}

func (v *recordingVega) RawQuery(_ context.Context, req *interfaces.VegaRawQueryReq) (*interfaces.VegaRawQueryResp, error) {
	v.req = req
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
	if vega.req.QueryFormat != "sql" || vega.req.InputDialect != "trino" || vega.req.QueryTimeoutSec != 30 {
		t.Fatalf("unexpected Raw Query contract: %#v", vega.req)
	}
	if vega.req.Paging != (interfaces.VegaPagingRequest{Mode: "single", Limit: 10000}) {
		t.Fatalf("unexpected paging: %#v", vega.req.Paging)
	}
}
