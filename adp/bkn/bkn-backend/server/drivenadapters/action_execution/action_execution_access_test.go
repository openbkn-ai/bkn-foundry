// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_execution

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	restmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
)

func TestCheckActionExecutionForwardsCurrentSubject(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := restmock.NewMockHTTPClient(ctrl)
	access := &actionExecutionAccess{baseURL: "http://ontology-query:13014", httpClient: httpClient}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "user-1", Type: "user"})
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(),
		"http://ontology-query:13014/api/ontology-query/in/v1/knowledge-networks/kn-1/action-types/at-1/execute/check",
		map[string]string{
			interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
			interfaces.HTTP_HEADER_ACCOUNT_ID:   "user-1",
			interfaces.HTTP_HEADER_ACCOUNT_TYPE: "user",
		}, map[string]any{"dynamic_params": map[string]any{"threshold": 3}}).
		Return(http.StatusNoContent, nil, nil)

	err := access.CheckActionExecution(ctx, interfaces.ActionExecutionCheckRequest{
		KNID: "kn-1", ActionTypeID: "at-1", DynamicParams: map[string]any{"threshold": 3},
	})
	if err != nil {
		t.Fatalf("CheckActionExecution() error = %v", err)
	}
}

func TestCheckActionExecutionFailsClosed(t *testing.T) {
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "user-1", Type: "user"})

	t.Run("permission denial remains forbidden", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		httpClient := restmock.NewMockHTTPClient(ctrl)
		httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusForbidden, nil, nil)
		err := (&actionExecutionAccess{baseURL: "http://ontology-query", httpClient: httpClient}).
			CheckActionExecution(ctx, interfaces.ActionExecutionCheckRequest{KNID: "kn-1", ActionTypeID: "at-1"})
		assertActionExecutionStatus(t, err, http.StatusForbidden)
	})

	t.Run("transport error is unavailable", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		httpClient := restmock.NewMockHTTPClient(ctrl)
		httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("timeout"))
		err := (&actionExecutionAccess{baseURL: "http://ontology-query", httpClient: httpClient}).
			CheckActionExecution(ctx, interfaces.ActionExecutionCheckRequest{KNID: "kn-1", ActionTypeID: "at-1"})
		assertActionExecutionStatus(t, err, http.StatusServiceUnavailable)
	})
}

func assertActionExecutionStatus(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != want {
		t.Fatalf("error = %T %v, want HTTP %d", err, err, want)
	}
}
