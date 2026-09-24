package toolbox

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

type captureActionEmitter struct {
	events []bkntrace.Event
	err    error
}

func (e *captureActionEmitter) Emit(_ context.Context, _ bkntrace.Action, events []bkntrace.Event) error {
	e.events = append(e.events, events...)
	return e.err
}

type completedActionGate struct{ acquireCalls int }

func (g *completedActionGate) Acquire(_ context.Context, _ bkntrace.Action) (bkntrace.ExecutionState, error) {
	g.acquireCalls++
	return bkntrace.ExecutionState{
		Completed: true,
		Result:    []byte(`{"status_code":200,"body":{"deduplicated":true}}`),
	}, nil
}

type acquiredActionGate struct{}

func (g *acquiredActionGate) Acquire(context.Context, bkntrace.Action) (bkntrace.ExecutionState, error) {
	return bkntrace.ExecutionState{Acquired: true}, nil
}

func (g *acquiredActionGate) Complete(context.Context, bkntrace.Action, []byte, bool) error {
	return nil
}

func (g *completedActionGate) Complete(context.Context, bkntrace.Action, []byte, bool) error {
	return errors.New("completed replay must not complete twice")
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

func TestExecuteToolReturnsCompletedActionAfterValidatingToolMembership(t *testing.T) {
	ctrl := gomock.NewController(t)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	toolboxDB := mocks.NewMockIToolboxDB(ctrl)
	toolDB := mocks.NewMockIToolDB(ctrl)
	gate := &completedActionGate{}
	emitter := &captureActionEmitter{err: errors.New("broker unavailable")}
	service := &ToolServiceImpl{
		AuthService: auth, ToolBoxDB: toolboxDB, ToolDB: toolDB, Logger: logger.DefaultLogger(),
		ActionEvidence: emitter, ActionExecutions: gate,
	}
	accessor := &interfaces.AuthAccessor{ID: "user-secret"}
	auth.EXPECT().GetAccessor(gomock.Any(), "user-secret").Return(accessor, nil)
	toolboxDB.EXPECT().SelectToolBox(gomock.Any(), "box-secret").
		Return(true, &model.ToolboxDB{BoxID: "box-secret", MetadataType: "openapi"}, nil)
	auth.EXPECT().CheckExecutePermission(gomock.Any(), accessor, "box-secret", interfaces.AuthResourceTypeToolBox).Return(nil)
	toolDB.EXPECT().SelectTool(gomock.Any(), "tool-secret").
		Return(true, &model.ToolDB{ToolID: "tool-secret", BoxID: "box-secret"}, nil)

	resp, err := service.ExecuteTool(context.Background(), &interfaces.ExecuteToolReq{
		UserID: "user-secret", BoxID: "box-secret", ToolID: "tool-secret",
		BKNConversationID: "conv_action_001",
		HTTPRequestParams: interfaces.HTTPRequestParams{Headers: actionHeaders()},
	})
	if err != nil || resp == nil || resp.StatusCode != 200 {
		t.Fatalf("cached action result not returned: resp=%#v err=%v", resp, err)
	}
	if gate.acquireCalls != 1 {
		t.Fatalf("gate acquire calls=%d", gate.acquireCalls)
	}
	if len(emitter.events) != 3 || emitter.events[1].EventType != "action.executed" || emitter.events[2].EventType != "action.result_recorded" {
		t.Fatalf("completed replay did not restore terminal evidence: %#v", emitter.events)
	}
}

func TestExecuteToolRejectsActionAtRealPermissionBoundary(t *testing.T) {
	ctrl := gomock.NewController(t)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	toolboxDB := mocks.NewMockIToolboxDB(ctrl)
	emitter := &captureActionEmitter{err: errors.New("broker unavailable")}
	service := &ToolServiceImpl{AuthService: auth, ToolBoxDB: toolboxDB, Logger: logger.DefaultLogger(), ActionEvidence: emitter}
	accessor := &interfaces.AuthAccessor{ID: "user-secret"}
	auth.EXPECT().GetAccessor(gomock.Any(), "user-secret").Return(accessor, nil)
	toolboxDB.EXPECT().SelectToolBox(gomock.Any(), "box-secret").
		Return(true, &model.ToolboxDB{BoxID: "box-secret", MetadataType: "openapi"}, nil)
	auth.EXPECT().CheckExecutePermission(gomock.Any(), accessor, "box-secret", interfaces.AuthResourceTypeToolBox).
		Return(errors.New("permission detail"))

	resp, err := service.ExecuteTool(context.Background(), &interfaces.ExecuteToolReq{
		UserID: "user-secret", BoxID: "box-secret", ToolID: "tool-secret",
		BKNConversationID: "conv_action_001",
		HTTPRequestParams: interfaces.HTTPRequestParams{Headers: actionHeaders()},
	})
	if err == nil || resp != nil {
		t.Fatalf("rejected action executed: resp=%v err=%v", resp, err)
	}
	if len(emitter.events) != 1 || emitter.events[0].EventType != "action.rejected" {
		t.Fatalf("unexpected lifecycle: %#v", emitter.events)
	}
}

func TestExecuteToolEvidenceAdmissionFailureDoesNotGateAuthorizedCall(t *testing.T) {
	fixture := newDebugToolFixture(t, string(interfaces.ToolStatusTypeEnabled))
	emitter := &captureActionEmitter{err: errors.New("local Evidence queue full")}
	fixture.service.ActionEvidence = emitter
	fixture.service.ActionExecutions = &acquiredActionGate{}
	req := &interfaces.ExecuteToolReq{UserID: "u1", BoxID: "b1", ToolID: "t1"}
	req.BKNConversationID = "conv_action_001"
	req.Headers = actionHeaders()

	resp, err := fixture.service.ExecuteTool(context.Background(), req)
	if err != nil || resp == nil || resp.StatusCode != 200 {
		t.Fatalf("Evidence admission failure gated authorized execution: response=%#v err=%v", resp, err)
	}
	if *fixture.captured == nil {
		t.Fatal("authorized tool was not invoked after Evidence admission failure")
	}
	if len(emitter.events) != 3 || emitter.events[0].EventType != "action.approved" || emitter.events[1].EventType != "action.executed" {
		t.Fatalf("unexpected Evidence lifecycle: %#v", emitter.events)
	}
}

func TestExecuteToolRecordsApprovedFailureAsHashOnlyTerminalLifecycle(t *testing.T) {
	ctrl := gomock.NewController(t)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	toolboxDB := mocks.NewMockIToolboxDB(ctrl)
	toolDB := mocks.NewMockIToolDB(ctrl)
	metadata := mocks.NewMockIMetadataService(ctrl)
	emitter := &captureActionEmitter{err: errors.New("broker unavailable")}
	service := &ToolServiceImpl{
		AuthService: auth, ToolBoxDB: toolboxDB, ToolDB: toolDB, MetadataService: metadata,
		Logger: logger.DefaultLogger(), ActionEvidence: emitter, ActionExecutions: &acquiredActionGate{},
	}
	accessor := &interfaces.AuthAccessor{ID: "user-secret"}
	auth.EXPECT().GetAccessor(gomock.Any(), "user-secret").Return(accessor, nil)
	auth.EXPECT().CheckExecutePermission(gomock.Any(), accessor, "box-secret", interfaces.AuthResourceTypeToolBox).Return(nil)
	toolboxDB.EXPECT().SelectToolBox(gomock.Any(), "box-secret").Return(true, &model.ToolboxDB{BoxID: "box-secret", MetadataType: "openapi", Status: string(interfaces.BizStatusPublished)}, nil).AnyTimes()
	tool := &model.ToolDB{ToolID: "tool-secret", BoxID: "box-secret", SourceID: "source-secret", SourceType: model.SourceTypeOpenAPI, Status: string(interfaces.ToolStatusTypeEnabled)}
	toolDB.EXPECT().SelectTool(gomock.Any(), "tool-secret").Return(true, tool, nil).AnyTimes()
	metadata.EXPECT().GetMetadataBySource(gomock.Any(), "source-secret", model.SourceTypeOpenAPI).
		Return(false, nil, errors.New("metadata detail must not leak"))

	resp, err := service.ExecuteTool(context.Background(), &interfaces.ExecuteToolReq{
		UserID: "user-secret", BoxID: "box-secret", ToolID: "tool-secret",
		BKNConversationID: "conv_action_001",
		HTTPRequestParams: interfaces.HTTPRequestParams{Headers: actionHeaders()},
	})
	if err == nil || resp != nil {
		t.Fatalf("expected execution boundary failure: resp=%v err=%v", resp, err)
	}
	if errors.Is(err, emitter.err) || err.Error() == emitter.err.Error() {
		t.Fatalf("evidence failure replaced the tool failure: %v", err)
	}
	if len(emitter.events) != 3 || emitter.events[1].EventType != "action.executed" || emitter.events[2].EventType != "action.result_recorded" {
		t.Fatalf("unexpected terminal lifecycle: %#v", emitter.events)
	}
	if emitter.events[1].Payload["status"] != "error" || emitter.events[1].Payload["error_hash"] == "" {
		t.Fatalf("execution failure is not hash-only: %#v", emitter.events[1])
	}
}
