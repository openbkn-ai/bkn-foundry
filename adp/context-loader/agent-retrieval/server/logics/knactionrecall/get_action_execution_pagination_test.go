// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knactionrecall

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func TestGetActionExecutionForwardsPaginationAndExposesTotal(t *testing.T) {
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	query := mocks.NewMockDrivenOntologyQuery(ctrl)
	service := &knActionRecallServiceImpl{logger: logger, ontologyQuery: query}
	limit, offset := 25, 100
	query.EXPECT().GetActionExecution(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *interfaces.GetActionExecutionRequest) (map[string]any, error) {
			if req.ResultsLimit != &limit || req.ResultsOffset != &offset {
				t.Fatalf("pagination was not forwarded: limit=%v offset=%v", req.ResultsLimit, req.ResultsOffset)
			}
			return map[string]any{
				"id": "exec-1", "status": "running", "results_total": 251,
				"results_offset": 100, "results_limit": 25,
				"results": []any{map[string]any{"_instance_id": "object-100"}},
			}, nil
		})
	resp, err := service.GetActionExecution(context.Background(), &interfaces.KnGetActionExecutionRequest{
		KnID: "kn-1", ExecutionID: "exec-1", ResultsLimit: &limit, ResultsOffset: &offset,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp["results_total"] != 251 || resp["results_offset"] != 100 || resp["results_limit"] != 25 {
		t.Fatalf("pagination metadata missing: %#v", resp)
	}
}

func TestGetActionExecutionIncludesZeroTotal(t *testing.T) {
	ctrl := gomock.NewController(t)
	query := mocks.NewMockDrivenOntologyQuery(ctrl)
	service := &knActionRecallServiceImpl{ontologyQuery: query}
	query.EXPECT().GetActionExecution(gomock.Any(), gomock.Any()).Return(map[string]any{
		"id": "exec-1", "results": []any{}, "results_limit": 1000,
	}, nil)
	resp, err := service.GetActionExecution(context.Background(), &interfaces.KnGetActionExecutionRequest{
		KnID: "kn-1", ExecutionID: "exec-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if total, ok := resp["results_total"]; !ok || total != 0 {
		t.Fatalf("zero total must be explicit, got %#v", resp)
	}
}

func TestGetActionExecutionRejectsMissingTotalForNonemptyPage(t *testing.T) {
	ctrl := gomock.NewController(t)
	query := mocks.NewMockDrivenOntologyQuery(ctrl)
	service := &knActionRecallServiceImpl{ontologyQuery: query}
	query.EXPECT().GetActionExecution(gomock.Any(), gomock.Any()).Return(map[string]any{
		"id": "exec-1", "results": []any{map[string]any{"status": "success"}}, "results_limit": 100,
	}, nil)
	_, err := service.GetActionExecution(context.Background(), &interfaces.KnGetActionExecutionRequest{
		KnID: "kn-1", ExecutionID: "exec-1",
	})
	if err == nil {
		t.Fatal("missing total on a nonempty page must not appear complete")
	}
}
