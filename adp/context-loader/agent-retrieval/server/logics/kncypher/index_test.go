// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kncypher

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// recordingBkn embeds the interface so only the one method under test needs a
// body; any other call would panic, which is what we want from a stub.
type recordingBkn struct {
	interfaces.BknBackendAccess
	req  *interfaces.CypherQueryReq
	resp *interfaces.CypherQueryResp
	err  error
}

func (b *recordingBkn) RunCypherQuery(_ context.Context, req *interfaces.CypherQueryReq) (*interfaces.CypherQueryResp, error) {
	b.req = req
	if b.err != nil {
		return nil, b.err
	}
	if b.resp != nil {
		return b.resp, nil
	}
	return &interfaces.CypherQueryResp{}, nil
}

func TestRunCypherCarriesTheQueryAcrossUntouched(t *testing.T) {
	bkn := &recordingBkn{}
	service := NewKnCypherServiceWith(bkn)

	query := "MATCH (o:order)-[:rel_order_item_order]->(i:order_item) WHERE o.amount > $floor RETURN o.order_no AS no"
	_, err := service.RunCypher(context.Background(), &RunCypherReq{
		KnID:       "  kn_retail  ",
		Branch:     "  main  ",
		Query:      query,
		Parameters: map[string]any{"floor": 100},
	})
	if err != nil {
		t.Fatalf("RunCypher() error = %v", err)
	}
	if bkn.req == nil {
		t.Fatal("RunCypherQuery() was not called")
	}
	// The identifiers are trimmed because they end up in a URL; the query text
	// is not, because its positions appear in the compiler's error messages.
	if bkn.req.KnID != "kn_retail" || bkn.req.Branch != "main" {
		t.Fatalf("kn_id=%q branch=%q, want them trimmed", bkn.req.KnID, bkn.req.Branch)
	}
	if bkn.req.Query != query {
		t.Fatalf("query = %q, want it passed through unchanged", bkn.req.Query)
	}
	if got, ok := bkn.req.Parameters["floor"]; !ok || got != 100 {
		t.Fatalf("parameters = %v, want floor carried across", bkn.req.Parameters)
	}
}

func TestRunCypherRefusesIncompleteInput(t *testing.T) {
	tests := []struct {
		name string
		req  *RunCypherReq
		want error
	}{
		{name: "nil request", req: nil, want: ErrKnIDRequired},
		{name: "blank kn id", req: &RunCypherReq{KnID: "   ", Query: "MATCH (o:order) RETURN o.id"}, want: ErrKnIDRequired},
		{name: "blank query", req: &RunCypherReq{KnID: "kn_retail", Query: "  "}, want: ErrQueryRequired},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bkn := &recordingBkn{}
			if _, err := NewKnCypherServiceWith(bkn).RunCypher(context.Background(), test.req); !errors.Is(err, test.want) {
				t.Fatalf("RunCypher() error = %v, want %v", err, test.want)
			}
			if bkn.req != nil {
				t.Fatal("an incomplete request must not reach bkn-backend")
			}
		})
	}
}

func TestRunCypherReturnsTheCompilerRefusalUnwrapped(t *testing.T) {
	refusal := errors.New("line 1:0: OPTIONAL MATCH is not supported")
	service := NewKnCypherServiceWith(&recordingBkn{err: refusal})

	_, err := service.RunCypher(context.Background(), &RunCypherReq{
		KnID:  "kn_retail",
		Query: "OPTIONAL MATCH (o:order) RETURN o.id",
	})
	// The refusal names the construct it refused, and that message is the whole
	// value of it to a caller writing a query, so it must arrive intact.
	if !errors.Is(err, refusal) {
		t.Fatalf("RunCypher() error = %v, want the compiler's own error", err)
	}
}

func TestRunCypherEmitsEvidenceAtEachBoundary(t *testing.T) {
	failures := []struct {
		name      string
		req       *RunCypherReq
		bknError  error
		wantStage string
		wantCode  string
	}{
		{name: "kn id required", req: &RunCypherReq{Query: "MATCH (o:order) RETURN o.id"}, wantStage: "input_validation", wantCode: "RUN_CYPHER_KN_ID_REQUIRED"},
		{name: "query required", req: &RunCypherReq{KnID: "kn_retail"}, wantStage: "input_validation", wantCode: "RUN_CYPHER_QUERY_REQUIRED"},
		{name: "compiler refusal", req: &RunCypherReq{KnID: "kn_retail", Query: "WITH 1 AS x RETURN x"}, bknError: errors.New("WITH is not supported"), wantStage: "cypher_query", wantCode: "RUN_CYPHER_QUERY_FAILED"},
	}

	for _, test := range failures {
		t.Run(test.name, func(t *testing.T) {
			previous := emitRunCypherFailure
			t.Cleanup(func() { emitRunCypherFailure = previous })
			var got bkntrace.RunCypherFailure
			var gotQuery string
			emitRunCypherFailure = func(_ context.Context, _ interfaces.Logger, _, query string, failure bkntrace.RunCypherFailure) string {
				gotQuery = query
				got = failure
				return "event-failure"
			}

			service := NewKnCypherServiceWith(&recordingBkn{err: test.bknError})
			if _, err := service.RunCypher(context.Background(), test.req); err == nil {
				t.Fatal("RunCypher() error = nil, want failure")
			}
			if got.Stage != test.wantStage || got.Code != test.wantCode || got.Summary == "" {
				t.Fatalf("failure=%+v, want stage=%s code=%s and summary", got, test.wantStage, test.wantCode)
			}
			if gotQuery != test.req.Query {
				t.Fatalf("recorded query=%q, want %q", gotQuery, test.req.Query)
			}
		})
	}

	t.Run("success", func(t *testing.T) {
		previous := emitRunCypherEvents
		t.Cleanup(func() { emitRunCypherEvents = previous })
		var gotKnID string
		var gotRows int
		emitRunCypherEvents = func(_ context.Context, _ interfaces.Logger, knID, _ string, rowCount int) string {
			gotKnID, gotRows = knID, rowCount
			return "event-success"
		}

		bkn := &recordingBkn{resp: &interfaces.CypherQueryResp{
			Columns: []interfaces.CypherQueryColumn{{Name: "no", Type: "string"}},
			Entries: []map[string]any{{"no": "A-1"}, {"no": "A-2"}},
		}}
		if _, err := NewKnCypherServiceWith(bkn).RunCypher(context.Background(), &RunCypherReq{
			KnID:  "kn_retail",
			Query: "MATCH (o:order) RETURN o.order_no AS no",
		}); err != nil {
			t.Fatalf("RunCypher() error = %v", err)
		}
		if gotKnID != "kn_retail" || gotRows != 2 {
			t.Fatalf("evidence kn_id=%q rows=%d, want kn_retail and 2", gotKnID, gotRows)
		}
	})
}
