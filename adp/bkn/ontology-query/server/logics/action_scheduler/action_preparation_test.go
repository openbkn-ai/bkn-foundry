// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"
)

func TestInspectionUsesExecutionGuardsWithoutCreatingOrInvoking(t *testing.T) {
	for _, decision := range []string{"accepted", "authorization", "duplicate", "input"} {
		t.Run(decision, func(t *testing.T) {
			controller := gomock.NewController(t)
			models := omock.NewMockOntologyManagerAccess(controller)
			action := interfaces.ActionType{ATID: "action", OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"}, ActionSource: interfaces.ActionSource{Type: "tool", BoxID: "box", ToolID: "tool"}}
			if decision == "input" {
				action.Parameters = []interfaces.Parameter{{Name: "count", Type: "integer", ValueFrom: "input"}}
			}
			models.EXPECT().GetActionType(gomock.Any(), "kn", "main", "action").Return(action, map[string]any{"id": "action"}, true, nil)
			permissions := &actionPermissionStub{}
			if decision == "authorization" {
				permissions.err = httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)
			}
			service := &actionSchedulerService{omAccess: models, permissions: permissions, proxy: &actionProxyResolverStub{}}
			service.duplicateCheckHook = func(_ context.Context, request *interfaces.ActionExecutionRequest) (bool, error) {
				if decision == "duplicate" {
					request.Inspection.DuplicateCheckEnabled = true
					request.Inspection.DuplicateWindowSeconds = 60
					request.Inspection.DuplicateExecutionID = "pending"
					return false, nil
				}
				return true, nil
			}
			// No log service or source executor is configured: either would panic if called.
			inspection, err := service.InspectActionExecution(context.Background(), &interfaces.ActionExecutionRequest{KNID: "kn", ActionTypeID: "action", Branch: "main"})
			if err != nil || inspection == nil {
				t.Fatalf("inspection: %+v %v", inspection, err)
			}
			if decision == "accepted" {
				if inspection.Decision != "accepted" || inspection.Mode != "once" || inspection.TotalCount != 1 || inspection.TargetCount != 1 {
					t.Fatalf("unexpected admitted plan: %+v", inspection)
				}
			} else if inspection.Decision != "rejected" || inspection.Phase != decision || inspection.ErrorCode == "" {
				t.Fatalf("unexpected rejection: %+v", inspection)
			}
		})
	}
}
