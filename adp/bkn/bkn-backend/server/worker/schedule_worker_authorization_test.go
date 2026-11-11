// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func TestExecuteScheduleUsesPersistedExecutionSubject(t *testing.T) {
	ctrl := gomock.NewController(t)
	actionTypes := bmock.NewMockActionTypeAccess(ctrl)
	actionTypes.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"at-1"}).
		Return([]*interfaces.ActionType{{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "at-1", ActionType: interfaces.ACTION_TYPE_MODIFY,
			OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationObject},
		}}}, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get(interfaces.HTTP_HEADER_ACCOUNT_ID); got != "user-current" {
			t.Errorf("account ID = %q, want user-current", got)
		}
		if got := request.Header.Get(interfaces.HTTP_HEADER_ACCOUNT_TYPE); got != "user" {
			t.Errorf("account type = %q, want user", got)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"execution_id": "execution-1"})
	}))
	defer server.Close()

	worker := &ScheduleWorker{
		appSetting: &common.AppSetting{OntologyQueryUrl: server.URL},
		ata:        actionTypes,
		httpClient: server.Client(),
	}
	executionID, err := worker.executeSchedule(context.Background(), &interfaces.ActionSchedule{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ActionTypeID: "at-1",
		InstanceIdentities: []map[string]any{{"id": "target-1"}},
		Creator:            interfaces.AccountInfo{ID: "user-creator", Type: "user"},
		ExecutionSubject:   interfaces.AccountInfo{ID: "user-current", Type: "user"},
	})
	if err != nil || executionID != "execution-1" {
		t.Fatalf("executeSchedule() = %q, %v", executionID, err)
	}
}

func TestExecuteScheduleRejectsUnsupportedActionBeforeHTTP(t *testing.T) {
	ctrl := gomock.NewController(t)
	actionTypes := bmock.NewMockActionTypeAccess(ctrl)
	actionTypes.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"at-tool"}).
		Return([]*interfaces.ActionType{{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "at-tool", OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationTool},
		}}}, nil)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unsupported schedule called ontology-query")
	}))
	defer server.Close()
	worker := &ScheduleWorker{appSetting: &common.AppSetting{OntologyQueryUrl: server.URL}, ata: actionTypes, httpClient: server.Client()}
	_, err := worker.executeSchedule(context.Background(), &interfaces.ActionSchedule{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ActionTypeID: "at-tool",
		ExecutionSubject:   interfaces.AccountInfo{ID: "user-current", Type: "user"},
		InstanceIdentities: []map[string]any{{"id": "target-1"}},
	})
	if err == nil {
		t.Fatal("unsupported tool schedule must be rejected before HTTP")
	}
}

func TestExecuteScheduleRejectsMissingSubjectWhenAuthenticationEnabled(t *testing.T) {
	worker := &ScheduleWorker{appSetting: &common.AppSetting{}, httpClient: http.DefaultClient}
	if executionID, err := worker.executeSchedule(context.Background(), &interfaces.ActionSchedule{
		Creator: interfaces.AccountInfo{ID: "user-creator", Type: "user"},
	}); err == nil || executionID != "" {
		t.Fatalf("executeSchedule() = %q, %v; want missing-subject error", executionID, err)
	}
}
