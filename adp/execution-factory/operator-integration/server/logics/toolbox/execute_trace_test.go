package toolbox

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/bkntrace"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

type captureActionEmitter struct{ events []bkntrace.Event }

func (e *captureActionEmitter) Emit(_ context.Context, _ bkntrace.Action, events []bkntrace.Event) {
	e.events = append(e.events, events...)
}

func actionHeaders() map[string]any {
	return map[string]any{
		"traceparent":    "00-1234567890abcdef1234567890abcdef-abcdef1234567890-01",
		"bkn-request-id": "req_action_001", "bkn-interaction-id": "int_action_001",
		"bkn-operation-id": "op_action_001", "bkn-causation-event-id": "evt_claim_001",
		"bkn-claim-id": "claim_001", "bkn-action-instance-id": "action_001",
		"bkn-action-type": "monitor", "bkn-action-reversible": "true",
		"bkn-action-policy-ref":                  "e2e-monitor-auto-approve",
		"bkn-action-observed-at":                 "2026-07-25T10:00:00.000000Z",
		"bkn-action-approval-requested-event-id": "evt_action_approval_requested_001",
		"bkn-attempt":                            "2",
		"x-account-id":                           "acct-test", "x-account-type": "user",
	}
}

func TestExecuteToolRejectsActionAtRealPermissionBoundary(t *testing.T) {
	ctrl := gomock.NewController(t)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	emitter := &captureActionEmitter{}
	service := &ToolServiceImpl{AuthService: auth, Logger: logger.DefaultLogger(), ActionEvidence: emitter}
	accessor := &interfaces.AuthAccessor{ID: "user-secret"}
	auth.EXPECT().GetAccessor(gomock.Any(), "user-secret").Return(accessor, nil)
	auth.EXPECT().CheckExecutePermission(gomock.Any(), accessor, "box-secret", interfaces.AuthResourceTypeToolBox).
		Return(errors.New("permission detail"))

	resp, err := service.ExecuteTool(context.Background(), &interfaces.ExecuteToolReq{
		UserID: "user-secret", BoxID: "box-secret", ToolID: "tool-secret",
		HTTPRequestParams: interfaces.HTTPRequestParams{Headers: actionHeaders()},
	})
	if err == nil || resp != nil {
		t.Fatalf("rejected action executed: resp=%v err=%v", resp, err)
	}
	if len(emitter.events) != 1 || emitter.events[0].EventType != "action.rejected" {
		t.Fatalf("unexpected lifecycle: %#v", emitter.events)
	}
}

func TestExecuteToolRecordsApprovedFailureAsHashOnlyTerminalLifecycle(t *testing.T) {
	ctrl := gomock.NewController(t)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	toolboxDB := mocks.NewMockIToolboxDB(ctrl)
	toolDB := mocks.NewMockIToolDB(ctrl)
	metadata := mocks.NewMockIMetadataService(ctrl)
	emitter := &captureActionEmitter{}
	service := &ToolServiceImpl{
		AuthService: auth, ToolBoxDB: toolboxDB, ToolDB: toolDB, MetadataService: metadata,
		Logger: logger.DefaultLogger(), ActionEvidence: emitter,
	}
	accessor := &interfaces.AuthAccessor{ID: "user-secret"}
	auth.EXPECT().GetAccessor(gomock.Any(), "user-secret").Return(accessor, nil)
	auth.EXPECT().CheckExecutePermission(gomock.Any(), accessor, "box-secret", interfaces.AuthResourceTypeToolBox).Return(nil)
	toolboxDB.EXPECT().SelectToolBox(gomock.Any(), "box-secret").Return(true, &model.ToolboxDB{BoxID: "box-secret"}, nil)
	tool := &model.ToolDB{ToolID: "tool-secret", SourceID: "source-secret", SourceType: model.SourceTypeOpenAPI, Status: string(interfaces.ToolStatusTypeEnabled)}
	toolDB.EXPECT().SelectTool(gomock.Any(), "tool-secret").Return(true, tool, nil)
	metadata.EXPECT().GetMetadataBySource(gomock.Any(), "source-secret", model.SourceTypeOpenAPI).
		Return(false, nil, errors.New("metadata detail must not leak"))

	resp, err := service.ExecuteTool(context.Background(), &interfaces.ExecuteToolReq{
		UserID: "user-secret", BoxID: "box-secret", ToolID: "tool-secret",
		HTTPRequestParams: interfaces.HTTPRequestParams{Headers: actionHeaders()},
	})
	if err == nil || resp != nil {
		t.Fatalf("expected execution boundary failure: resp=%v err=%v", resp, err)
	}
	if len(emitter.events) != 3 || emitter.events[1].EventType != "action.executed" || emitter.events[2].EventType != "action.result_recorded" {
		t.Fatalf("unexpected terminal lifecycle: %#v", emitter.events)
	}
	if emitter.events[1].Payload["status"] != "error" || emitter.events[1].Payload["error_hash"] == "" {
		t.Fatalf("execution failure is not hash-only: %#v", emitter.events[1])
	}
}
