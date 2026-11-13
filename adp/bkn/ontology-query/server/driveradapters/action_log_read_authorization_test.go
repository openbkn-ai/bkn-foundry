// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

type actionLogReadPermissionStub struct {
	requirements []interfaces.PermissionRequirement
	err          error
}

func (s *actionLogReadPermissionStub) RequirePermissions(_ context.Context, requirements []interfaces.PermissionRequirement) error {
	s.requirements = append(s.requirements, requirements...)
	return s.err
}

func actionLogReadContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Params = gin.Params{{Key: "kn_id", Value: "kn-1"}, {Key: "execution_id", Value: "exec-1"}, {Key: "log_id", Value: "exec-1"}}
	return c, w
}

func TestPublicActionLogReadsRequireKnowledgeNetworkView(t *testing.T) {
	visitor := hydra.Visitor{ID: "outsider", Type: hydra.VisitorType_User}
	for _, route := range []struct {
		name string
		path string
		call func(*restHandler, *gin.Context)
	}{
		{"execution", "/knowledge-networks/kn-1/action-executions/exec-1", func(r *restHandler, c *gin.Context) { r.GetActionExecution(c, visitor, false) }},
		{"list", "/knowledge-networks/kn-1/action-logs", func(r *restHandler, c *gin.Context) { r.QueryActionLogs(c, visitor, false) }},
		{"detail", "/knowledge-networks/kn-1/action-logs/exec-1", func(r *restHandler, c *gin.Context) { r.GetActionLog(c, visitor, false) }},
		{"results", "/knowledge-networks/kn-1/action-logs/exec-1/results", func(r *restHandler, c *gin.Context) { r.QueryActionLogResults(c, visitor, false) }},
	} {
		t.Run(route.name, func(t *testing.T) {
			denied := httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)
			permissions := &actionLogReadPermissionStub{err: denied}
			r := &restHandler{actionReadPermissions: permissions}
			c, w := actionLogReadContext(route.path)
			route.call(r, c)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
			}
			want := interfaces.PermissionRequirement{ResourceType: interfaces.PermissionResourceTypeKnowledgeNetwork,
				ResourceID: "kn-1", Operation: interfaces.PermissionOperationViewDetail}
			if len(permissions.requirements) != 1 || permissions.requirements[0] != want {
				t.Fatalf("requirements = %#v, want %#v", permissions.requirements, want)
			}
		})
	}
}

func TestActionExecutionReadAllowsOwnerAndInternalCaller(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := omock.NewMockActionSchedulerService(ctrl)
	service.EXPECT().GetExecution(gomock.Any(), "kn-1", "exec-1").Times(2).
		Return(&interfaces.ActionExecution{ID: "exec-1"}, nil)
	permissions := &actionLogReadPermissionStub{}
	r := &restHandler{ass: service, actionReadPermissions: permissions}
	for _, internal := range []bool{false, true} {
		c, w := actionLogReadContext("/knowledge-networks/kn-1/action-executions/exec-1")
		r.GetActionExecution(c, hydra.Visitor{ID: "owner", Type: hydra.VisitorType_User}, internal)
		if w.Code != http.StatusOK {
			t.Fatalf("internal = %v, status = %d, body = %s", internal, w.Code, w.Body.String())
		}
	}
	if len(permissions.requirements) != 1 {
		t.Fatalf("public read checks = %d, want 1", len(permissions.requirements))
	}
}

func TestActionExecutionReadFailsClosedWhenPermissionCheckUnavailable(t *testing.T) {
	permissions := &actionLogReadPermissionStub{err: errors.New("safe unavailable")}
	r := &restHandler{actionReadPermissions: permissions}
	c, w := actionLogReadContext("/knowledge-networks/kn-1/action-executions/exec-1")
	r.GetActionExecution(c, hydra.Visitor{ID: "owner", Type: hydra.VisitorType_User}, false)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
}
