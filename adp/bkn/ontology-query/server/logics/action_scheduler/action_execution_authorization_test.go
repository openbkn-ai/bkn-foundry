// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"
)

type actionPermissionStub struct {
	requirements []interfaces.PermissionRequirement
	err          error
}

func (s *actionPermissionStub) RequirePermissions(_ context.Context,
	requirements []interfaces.PermissionRequirement) error {
	s.requirements = append([]interfaces.PermissionRequirement(nil), requirements...)
	return s.err
}

func TestAuthorizeActionTypeResolvesTrustedRequirements(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "true")
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "ot-input").
		Return(interfaces.ObjectType{KNID: "kn-1"}, true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "ot-output").
		Return(interfaces.ObjectType{KNID: "kn-1"}, true, nil)
	permissions := &actionPermissionStub{}
	service := &actionSchedulerService{omAccess: models, permissions: permissions}

	got, err := service.authorizeActionType(context.Background(), "kn-1", &interfaces.ActionType{
		ATID:         "at-1",
		ObjectTypeID: "ot-input",
		Affect:       &interfaces.ActionAffect{ObjectTypeID: "ot-output"},
		ImpactContracts: []interfaces.ImpactContractItem{
			{ObjectTypeID: "ot-input"},
		},
		ActionSource: interfaces.ActionSource{Type: interfaces.ActionSourceTypeTool, BoxID: "box-1", ToolID: "tool-1"},
	})
	if err != nil {
		t.Fatalf("authorizeActionType() error = %v", err)
	}
	want := []interfaces.PermissionRequirement{
		{ResourceType: "action_type", ResourceID: "kn-1/at-1", Operation: "execute"},
		{ResourceType: "object_type", ResourceID: "kn-1/ot-input", Operation: "query_data"},
		{ResourceType: "object_type", ResourceID: "kn-1/ot-output", Operation: "query_data"},
		{ResourceType: "tool_box", ResourceID: "box-1", Operation: "execute"},
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(permissions.requirements, want) {
		t.Fatalf("requirements = %#v, checked = %#v, want %#v", got, permissions.requirements, want)
	}
}

func TestExecuteActionChecksPermissionsBeforeInstanceData(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "true")
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	models.EXPECT().GetActionType(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "at-1").Return(
		interfaces.ActionType{
			ATID:         "at-1",
			ActionSource: interfaces.ActionSource{Type: interfaces.ActionSourceTypeMCP, McpID: "mcp-1", ToolName: "run"},
		}, nil, true, nil)
	permissionErr := errors.New("permission denied")
	permissions := &actionPermissionStub{err: permissionErr}
	service := &actionSchedulerService{omAccess: models, permissions: permissions}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "user-1", Type: "user"})

	resp, err := service.ExecuteAction(ctx, &interfaces.ActionExecutionRequest{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ActionTypeID: "at-1",
	})
	if !errors.Is(err, permissionErr) || resp != nil {
		t.Fatalf("ExecuteAction() = %#v, %v; want permission error before data access", resp, err)
	}
	if len(permissions.requirements) != 2 {
		t.Fatalf("checked requirements = %#v", permissions.requirements)
	}
}

func TestAuthorizeExecutionRequiresSnapshotWhenEnabled(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "true")
	err := (&actionSchedulerService{permissions: &actionPermissionStub{}}).authorizeExecution(context.Background(), nil)
	if err == nil {
		t.Fatal("authorizeExecution() accepted a missing permission snapshot")
	}
}

func TestInvokeActionSourceDoesNotCallExternalServiceAfterRevocation(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "true")
	revoked := errors.New("permission revoked")
	service := &actionSchedulerService{permissions: &actionPermissionStub{err: revoked}}
	params, result, err := service.invokeActionSource(context.Background(), []interfaces.PermissionRequirement{
		{ResourceType: "tool_box", ResourceID: "box-1", Operation: "execute"},
	}, &interfaces.ActionType{
		ActionSource: interfaces.ActionSource{Type: interfaces.ActionSourceTypeTool, BoxID: "box-1", ToolID: "tool-1"},
	}, map[string]any{"value": 1}, nil)
	if !errors.Is(err, revoked) || result != nil || params["value"] != 1 {
		t.Fatalf("invokeActionSource() = %#v, %#v, %v", params, result, err)
	}
}

func TestActionExecutionAuthorizationBypassesWhenAuthenticationIsDisabled(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "false")
	service := &actionSchedulerService{}
	if requirements, err := service.authorizeActionType(context.Background(), "", nil); err != nil || requirements != nil {
		t.Fatalf("authorizeActionType() = %#v, %v", requirements, err)
	}
	if err := service.authorizeExecution(context.Background(), nil); err != nil {
		t.Fatalf("authorizeExecution() error = %v", err)
	}
}
