// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// downstreamErrorForStatus reproduces what the shared HTTP client hands back for a
// non-2xx ontology-query response: the downstream status code, the generic
// external-server code, and the raw response body inside the details string.
func downstreamErrorForStatus(status int, body string) error {
	return infraErr.NewHTTPError(context.Background(), status, infraErr.ErrExtCommonExternalServerError,
		fmt.Sprintf("Exception(http do error, method: POST, url: %s,  http status: %d, error: %s)",
			"http://ontology-query/api", status, body))
}

func newActionErrorClient(t *testing.T) (*ontologyQueryClient, *mocks.MockHTTPClient, *gomock.Controller) {
	t.Helper()
	ctrl := gomock.NewController(t)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
	return &ontologyQueryClient{
		logger:     mockLogger,
		baseURL:    "http://ontology-query/api/ontology-query",
		httpClient: mockHTTPClient,
	}, mockHTTPClient, ctrl
}

func asHTTPError(t *testing.T, err error) *infraErr.HTTPError {
	t.Helper()
	var he *infraErr.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("expected *infraErr.HTTPError, got %T: %v", err, err)
	}
	return he
}

// A modelling error downstream is the caller's problem, not an outage: an action type
// whose object type has no data source makes ontology-query answer 400, and reporting
// that as 502 "dependency unavailable" sent triage to pod health instead of to the
// knowledge network. See #1225.
func TestQueryActions_DownstreamClientErrorIsNotReportedAsBadGateway(t *testing.T) {
	client, mockHTTPClient, ctrl := newActionErrorClient(t)
	defer ctrl.Finish()

	const body = `{"error_code":"OntologyQuery.ObjectType.InvalidParameter",` +
		`"description":"请求参数不合法","error_details":"对象类 contract 未绑定数据源。"}`
	mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusBadRequest, nil, downstreamErrorForStatus(http.StatusBadRequest, body))

	_, err := client.QueryActions(context.Background(), &interfaces.QueryActionsRequest{
		KnID: "crm-management",
		AtID: "contract_to_order",
	})

	he := asHTTPError(t, err)
	if he.HTTPCode != http.StatusBadRequest {
		t.Fatalf("downstream 400 must stay a 400, got %d", he.HTTPCode)
	}
	details := fmt.Sprintf("%v", he.ErrorDetails)
	if !strings.Contains(details, "对象类 contract 未绑定数据源。") {
		t.Fatalf("downstream cause must reach the caller, got details: %s", details)
	}
}

// A downstream 404 (unknown kn_id/at_id) must keep its own status rather than
// collapsing into 400, so the caller can tell "no such action type" from
// "this request is malformed".
func TestQueryActions_DownstreamNotFoundKeepsItsStatus(t *testing.T) {
	client, mockHTTPClient, ctrl := newActionErrorClient(t)
	defer ctrl.Finish()

	mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusNotFound, nil, downstreamErrorForStatus(http.StatusNotFound,
			`{"error_code":"OntologyQuery.ObjectType.ObjectTypeNotFound"}`))

	_, err := client.QueryActions(context.Background(), &interfaces.QueryActionsRequest{
		KnID: "kn-1", AtID: "missing_action",
	})

	if code := asHTTPError(t, err).HTTPCode; code != http.StatusNotFound {
		t.Fatalf("downstream 404 must stay a 404, got %d", code)
	}
}

// A 5xx really is a dependency fault, so it keeps the operation-specific 502 detail.
func TestQueryActions_DownstreamServerErrorStaysBadGateway(t *testing.T) {
	client, mockHTTPClient, ctrl := newActionErrorClient(t)
	defer ctrl.Finish()

	mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusInternalServerError, nil,
			downstreamErrorForStatus(http.StatusInternalServerError, `{"error_code":"OntologyQuery.InternalError"}`))

	_, err := client.QueryActions(context.Background(), &interfaces.QueryActionsRequest{
		KnID: "kn-1", AtID: "at-1",
	})

	if code := asHTTPError(t, err).HTTPCode; code != http.StatusBadGateway {
		t.Fatalf("downstream 500 must stay a 502, got %d", code)
	}
}

// A transport error carries no HTTP code at all and must also stay a 502.
func TestQueryActions_TransportErrorStaysBadGateway(t *testing.T) {
	client, mockHTTPClient, ctrl := newActionErrorClient(t)
	defer ctrl.Finish()

	mockHTTPClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(0, nil, errors.New("dial tcp: connection refused"))

	_, err := client.QueryActions(context.Background(), &interfaces.QueryActionsRequest{
		KnID: "kn-1", AtID: "at-1",
	})

	if code := asHTTPError(t, err).HTTPCode; code != http.StatusBadGateway {
		t.Fatalf("transport failure must stay a 502, got %d", code)
	}
}

// get_action_execution and list_action_executions call the same downstream family and
// had the same blanket mapping, so they are held to the same contract.
func TestActionExecutionLookups_PreserveDownstreamClientError(t *testing.T) {
	t.Run("GetActionExecution", func(t *testing.T) {
		client, mockHTTPClient, ctrl := newActionErrorClient(t)
		defer ctrl.Finish()

		mockHTTPClient.EXPECT().GetBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusNotFound, nil, downstreamErrorForStatus(http.StatusNotFound,
				`{"error_code":"OntologyQuery.ActionExecution.NotFound"}`))

		_, err := client.GetActionExecution(context.Background(), &interfaces.GetActionExecutionRequest{
			KnID: "kn-1", ExecutionID: "exec-1",
		})

		if code := asHTTPError(t, err).HTTPCode; code != http.StatusNotFound {
			t.Fatalf("downstream 404 must stay a 404, got %d", code)
		}
	})

	t.Run("ListActionExecutions", func(t *testing.T) {
		client, mockHTTPClient, ctrl := newActionErrorClient(t)
		defer ctrl.Finish()

		mockHTTPClient.EXPECT().GetBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusBadRequest, nil, downstreamErrorForStatus(http.StatusBadRequest,
				`{"error_code":"OntologyQuery.InvalidParameter"}`))

		_, err := client.ListActionExecutions(context.Background(), &interfaces.ListActionExecutionsRequest{
			KnID: "kn-1",
		})

		if code := asHTTPError(t, err).HTTPCode; code != http.StatusBadRequest {
			t.Fatalf("downstream 400 must stay a 400, got %d", code)
		}
	})
}
