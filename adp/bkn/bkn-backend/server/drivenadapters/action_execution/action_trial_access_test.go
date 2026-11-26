// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_execution

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	restmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func trialAccess(t *testing.T) (*actionExecutionAccess, *restmock.MockHTTPClient) {
	t.Helper()
	httpClient := restmock.NewMockHTTPClient(gomock.NewController(t))
	return &actionExecutionAccess{baseURL: "http://ontology-query:13014", httpClient: httpClient}, httpClient
}

func trialAction() *interfaces.ActionType {
	return &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "at-1"}, KNID: "kn-1"}
}

func saverContext() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "user-1", Type: "user"})
}

func TestTrialRunsAsTheSaverInTheBranch(t *testing.T) {
	access, httpClient := trialAccess(t)
	at := trialAction()
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(),
		"http://ontology-query:13014/api/ontology-query/in/v1/knowledge-networks/kn-1/action-trial?branch=draft",
		map[string]string{
			interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
			interfaces.HTTP_HEADER_ACCOUNT_ID:   "user-1",
			interfaces.HTTP_HEADER_ACCOUNT_TYPE: "user",
		}, at).
		Return(http.StatusOK, []byte(`{"event":{"subject":"order","hits":[{"key":"o1"}],"hit_count":1,"limited":false}}`), nil)
	trial, err := access.TrialEventAction(saverContext(), "draft", at)
	if err != nil || trial.Subject != "order" || trial.HitCount != 1 || len(trial.Hits) != 1 {
		t.Fatalf("trial %+v err %v", trial, err)
	}
}

func TestTrialRelaysWhyTheDefinitionIsRefused(t *testing.T) {
	access, httpClient := trialAccess(t)
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusBadRequest, []byte(`{"error_code":"OntologyQuery.InvalidParameter","description":"bad","error_details":"the query returns no object identity"}`), nil)
	_, err := access.TrialEventAction(saverContext(), "", trialAction())
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest {
		t.Fatalf("%v, want 400", err)
	}
	if details, _ := httpErr.ErrorDetails.(string); !strings.Contains(details, "the query returns no object identity") {
		t.Fatalf("details %v, want the reason", httpErr.ErrorDetails)
	}
}

func TestTrialFailsClosed(t *testing.T) {
	access, _ := trialAccess(t)
	_, err := access.TrialEventAction(context.Background(), "", trialAction())
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("%v, want 403 without an account", err)
	}

	access, httpClient := trialAccess(t)
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusInternalServerError, []byte(`oops`), nil)
	_, err = access.TrialEventAction(saverContext(), "", trialAction())
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusServiceUnavailable {
		t.Fatalf("%v, want 503", err)
	}
}
