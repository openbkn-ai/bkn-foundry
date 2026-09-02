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

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

func TestExecuteScheduleUsesPersistedExecutionSubject(t *testing.T) {
	t.Setenv("ACTION_EXECUTION_PEP_ENABLED", "true")
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
		httpClient: server.Client(),
	}
	executionID, err := worker.executeSchedule(context.Background(), &interfaces.ActionSchedule{
		KNID: "kn-1", ActionTypeID: "at-1",
		Creator:          interfaces.AccountInfo{ID: "user-creator", Type: "user"},
		ExecutionSubject: interfaces.AccountInfo{ID: "user-current", Type: "user"},
	})
	if err != nil || executionID != "execution-1" {
		t.Fatalf("executeSchedule() = %q, %v", executionID, err)
	}
}

func TestExecuteScheduleRejectsMissingSubjectWhenPEPEnabled(t *testing.T) {
	t.Setenv("ACTION_EXECUTION_PEP_ENABLED", "true")
	worker := &ScheduleWorker{appSetting: &common.AppSetting{}, httpClient: http.DefaultClient}
	if executionID, err := worker.executeSchedule(context.Background(), &interfaces.ActionSchedule{
		Creator: interfaces.AccountInfo{ID: "user-creator", Type: "user"},
	}); err == nil || executionID != "" {
		t.Fatalf("executeSchedule() = %q, %v; want missing-subject error", executionID, err)
	}
}
