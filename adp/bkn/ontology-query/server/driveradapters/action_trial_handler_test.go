// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

// trialScheduler is an action scheduler that can try action types.
type trialScheduler struct {
	interfaces.ActionSchedulerService
	branch string
	action *interfaces.ActionType
}

func (s *trialScheduler) TrialAction(_ context.Context, _, branch string, action *interfaces.ActionType) (*interfaces.ActionTrialResponse, error) {
	s.branch, s.action = branch, action
	return &interfaces.ActionTrialResponse{Event: &interfaces.EventTrialResult{Subject: action.ObjectTypeID, Hits: []interfaces.EventHit{}}}, nil
}

const trialBody = `{"id":"po_overdue_recognize","object_type_id":"purchase_order","rule_version":"x",` +
	`"execution_subject":{"id":"u","type":"user"},"operation_config":{"type":"event","event_class":"po_overdue"}}`

func trialContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(trialBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-1"}}
	return c, w
}

func TestATrialNeedsTheNetworksModifyPermission(t *testing.T) {
	denied := httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)
	permissions := &actionLogReadPermissionStub{err: denied}
	scheduler := &trialScheduler{}
	r := &restHandler{ass: scheduler, actionReadPermissions: permissions}
	c, w := trialContext("/api/ontology-query/v1/knowledge-networks/kn-1/action-trial")
	r.TrialAction(c, hydra.Visitor{ID: "viewer", Type: hydra.VisitorType_User})
	if w.Code != http.StatusForbidden || scheduler.action != nil {
		t.Fatalf("status = %d, tried = %v", w.Code, scheduler.action != nil)
	}
	want := interfaces.PermissionRequirement{ResourceType: interfaces.PermissionResourceTypeKnowledgeNetwork,
		ResourceID: "kn-1", Operation: interfaces.PermissionOperationModify}
	if len(permissions.requirements) != 1 || permissions.requirements[0] != want {
		t.Fatalf("requirements = %#v", permissions.requirements)
	}
}

func TestATrialReadsTheBranchItIsGiven(t *testing.T) {
	scheduler := &trialScheduler{}
	r := &restHandler{ass: scheduler, actionReadPermissions: &actionLogReadPermissionStub{}}

	c, w := trialContext("/api/ontology-query/in/v1/knowledge-networks/kn-1/action-trial?branch=draft")
	r.TrialAction(c, hydra.Visitor{ID: "editor", Type: hydra.VisitorType_User})
	if w.Code != http.StatusOK || scheduler.branch != "draft" {
		t.Fatalf("status = %d, branch = %q, body = %s", w.Code, scheduler.branch, w.Body.String())
	}
	if scheduler.action.RuleVersion != "" || scheduler.action.ExecutionSubject != nil {
		t.Fatalf("what the platform keeps on a saved action type has no part in a trial: %+v", scheduler.action)
	}
	if !strings.Contains(w.Body.String(), `"subject":"purchase_order"`) {
		t.Fatalf("body = %s", w.Body.String())
	}

	c, _ = trialContext("/api/ontology-query/v1/knowledge-networks/kn-1/action-trial")
	r.TrialAction(c, hydra.Visitor{ID: "editor", Type: hydra.VisitorType_User})
	if scheduler.branch != interfaces.MAIN_BRANCH {
		t.Fatalf("the branch defaults to main, got %q", scheduler.branch)
	}
}

func TestActionLogTargetKeyNeedsObjectType(t *testing.T) {
	r := &restHandler{actionReadPermissions: &actionLogReadPermissionStub{}}
	c, w := actionLogReadContext("/knowledge-networks/kn-1/action-logs?target_key=1")
	r.QueryActionLogs(c, hydra.Visitor{ID: "owner", Type: hydra.VisitorType_User}, false)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "target_key needs object_type_id") {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	ctrl := gomock.NewController(t)
	logs := omock.NewMockActionLogsService(ctrl)
	var query *interfaces.ActionLogQuery
	logs.EXPECT().QueryExecutions(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q *interfaces.ActionLogQuery) (*interfaces.ActionExecutionList, error) {
			query = q
			return &interfaces.ActionExecutionList{Entries: []interfaces.ActionExecution{}}, nil
		})
	r.als = logs
	c, w = actionLogReadContext("/knowledge-networks/kn-1/action-logs?object_type_id=%20purchase_order%20&target_key=9007199254740993")
	r.QueryActionLogs(c, hydra.Visitor{ID: "owner", Type: hydra.VisitorType_User}, false)
	if w.Code != http.StatusOK || query.ObjectTypeID != "purchase_order" || query.TargetKey != "9007199254740993" {
		t.Fatalf("status = %d, query = %+v, body = %s", w.Code, query, w.Body.String())
	}
}

func TestOnlyTheInternalExecuteRouteIsInternal(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := omock.NewMockActionSchedulerService(ctrl)
	var internal []bool
	service.EXPECT().ExecuteAction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *interfaces.ActionExecutionRequest) (*interfaces.ActionExecutionResponse, error) {
			internal = append(internal, req.Internal)
			return &interfaces.ActionExecutionResponse{ExecutionID: "e"}, nil
		}).Times(2)
	r := &restHandler{ass: service}
	request := func(path string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"trigger_type":"scheduled","internal":true}`))
		c.Request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "u-subject")
		c.Request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
		c.Params = gin.Params{{Key: "kn_id", Value: "kn-1"}, {Key: "at_id", Value: "po_overdue_recognize"}}
		return c
	}
	r.ExecuteActionByIn(request("/api/ontology-query/in/v1/knowledge-networks/kn-1/action-types/po_overdue_recognize/execute"))
	r.ExecuteAction(request("/api/ontology-query/v1/knowledge-networks/kn-1/action-types/po_overdue_recognize/execute"),
		hydra.Visitor{ID: "u-subject", Type: hydra.VisitorType_User})
	if len(internal) != 2 || !internal[0] || internal[1] {
		t.Fatalf("internal = %v", internal)
	}
}

// Handling an event is the platform's: only the internal route takes trigger_type event, as the
// execution subject of the event's class.
func TestOnlyThePlatformHandlesEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := omock.NewMockActionSchedulerService(ctrl)
	var triggers []string
	service.EXPECT().ExecuteAction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *interfaces.ActionExecutionRequest) (*interfaces.ActionExecutionResponse, error) {
			triggers = append(triggers, req.TriggerType)
			return &interfaces.ActionExecutionResponse{ExecutionID: "e"}, nil
		}).Times(1)
	r := &restHandler{ass: service}
	request := func(path string) (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, path,
			strings.NewReader(`{"trigger_type":"event","_instance_identities":[{"id":"PO-1"}]}`))
		c.Request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "u-subject")
		c.Request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
		c.Params = gin.Params{{Key: "kn_id", Value: "kn-1"}, {Key: "at_id", Value: "notify_buyer"}}
		return c, w
	}
	c, w := request("/api/ontology-query/in/v1/knowledge-networks/kn-1/action-types/notify_buyer/execute")
	r.ExecuteActionByIn(c)
	if w.Code != http.StatusAccepted || len(triggers) != 1 || triggers[0] != interfaces.TriggerTypeEvent {
		t.Fatalf("internal: status %d, triggers %v, body %s", w.Code, triggers, w.Body.String())
	}
	c, w = request("/api/ontology-query/v1/knowledge-networks/kn-1/action-types/notify_buyer/execute")
	r.ExecuteAction(c, hydra.Visitor{ID: "u-subject", Type: hydra.VisitorType_User})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("external: status %d, body %s", w.Code, w.Body.String())
	}
}

// Input bindings come only with the platform's handling of an event, never with another run.
func TestInputBindingsComeOnlyWithAnEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := omock.NewMockActionSchedulerService(ctrl)
	service.EXPECT().ExecuteAction(gomock.Any(), gomock.Any()).Times(0)
	r := &restHandler{ass: service}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost,
		"/api/ontology-query/in/v1/knowledge-networks/kn-1/action-types/notify_buyer/execute",
		strings.NewReader(`{"trigger_type":"scheduled","_instance_identities":[{"id":"PO-1"}],`+
			`"input_bindings":{"reason":{"value_from":"const","value":"late"}}}`))
	c.Request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "u-subject")
	c.Request.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-1"}, {Key: "at_id", Value: "notify_buyer"}}
	r.ExecuteActionByIn(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
}
