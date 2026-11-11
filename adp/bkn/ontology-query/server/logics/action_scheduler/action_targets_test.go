// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"reflect"
	"strings"
	"testing"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"go.uber.org/mock/gomock"
)

func actionTargetObject(id, key string) interfaces.ObjectType {
	return interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id, PrimaryKeys: []string{key}, DataProperties: []cond.DataProperty{{Name: key, Type: "string"}, {Name: "status", Type: "string"}}}}
}

func TestActionTargetsRejectUnmigratedAndUnspecified(t *testing.T) {
	svc := &actionSchedulerService{}
	_, _, err := svc.getInstancesForAction(context.Background(), new(interfaces.ActionType), &interfaces.ActionExecutionRequest{})
	if err == nil || !strings.Contains(err.Error(), "operation_config") {
		t.Fatalf("expected error containing %s, got %v", "operation_config", err)
	}
	tool := &interfaces.ActionType{OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"}}
	_, _, err = svc.getInstancesForAction(context.Background(), tool, &interfaces.ActionExecutionRequest{InstanceIdentities: []map[string]any{{"id": "1"}}})
	if err == nil {
		t.Fatal("expected an error")
	}
	relation := &interfaces.ActionType{ObjectTypeID: "orders", OperationConfig: &interfaces.ActionOperationConfig{Type: "relation", RelationTypeID: "supplier", RelationOperation: "create", AnchorRole: "source"}}
	_, _, err = svc.getInstancesForAction(context.Background(), relation, &interfaces.ActionExecutionRequest{})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestActionCreateUsesInputWithoutScanning(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	object := actionTargetObject("orders", "order_id")
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "orders").Return(object, true, nil)
	svc := &actionSchedulerService{omAccess: models}
	action := &interfaces.ActionType{ActionType: "add", ObjectTypeID: "orders", OperationConfig: &interfaces.ActionOperationConfig{Type: "object"}}
	instances, data, err := svc.getInstancesForAction(context.Background(), action, &interfaces.ActionExecutionRequest{KNID: "kn", Branch: "main", ObjectInput: map[string]any{"order_id": "new-order"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual("new-order", data[0]["order_id"]) {
		t.Fatalf("expected %v, got %v", "new-order", data[0]["order_id"])
	}
	if !reflect.DeepEqual(map[string]any{"order_id": "new-order"}, instances[0].InstanceIdentity) {
		t.Fatalf("expected %v, got %v", map[string]any{"order_id": "new-order"}, instances[0].InstanceIdentity)
	}
}

func TestActionModifyRequiresIdentity(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "orders").Return(actionTargetObject("orders", "order_id"), true, nil)
	svc := &actionSchedulerService{omAccess: models}
	action := &interfaces.ActionType{ActionType: "modify", ObjectTypeID: "orders", OperationConfig: &interfaces.ActionOperationConfig{Type: "object"}}
	_, _, err := svc.getInstancesForAction(context.Background(), action, &interfaces.ActionExecutionRequest{KNID: "kn", Branch: "main"})
	if err == nil || !strings.Contains(err.Error(), "explicit _instance_identities") {
		t.Fatalf("expected error containing %s, got %v", "explicit _instance_identities", err)
	}
}

func TestActionRelationResolvesDirectedPairAndReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	objects := omock.NewMockObjectTypeService(ctrl)
	relation := interfaces.RelationType{RTID: "supplier", SourceObjectTypeID: "orders", TargetObjectTypeID: "suppliers"}
	models.EXPECT().GetRelationType(gomock.Any(), "kn", "main", "supplier").Return(relation, true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "orders").Return(actionTargetObject("orders", "order_id"), true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "suppliers").Return(actionTargetObject("suppliers", "supplier_id"), true, nil)
	objects.EXPECT().GetObjectsByObjectTypeID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
		if !reflect.DeepEqual("orders", query.ObjectTypeID) {
			t.Fatalf("expected %v, got %v", "orders", query.ObjectTypeID)
		}
		if query.ActualCondition == nil {
			t.Fatal("expected a non-nil value")
		}
		return interfaces.Objects{Datas: []map[string]any{{"order_id": "O1", "status": "pending"}}}, nil
	})
	objects.EXPECT().GetObjectsByObjectTypeID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
		if !reflect.DeepEqual("suppliers", query.ObjectTypeID) {
			t.Fatalf("expected %v, got %v", "suppliers", query.ObjectTypeID)
		}
		return interfaces.Objects{Datas: []map[string]any{{"supplier_id": "S1", "status": "active"}}}, nil
	})
	svc := &actionSchedulerService{omAccess: models, ots: objects}
	action := &interfaces.ActionType{ObjectTypeID: "suppliers", OperationConfig: &interfaces.ActionOperationConfig{Type: "relation", RelationTypeID: "supplier", RelationOperation: "create", AnchorRole: "target", OtherObjectCondition: &cond.CondCfg{Name: "status", Operation: "==", Value: "pending"}}, Condition: &cond.CondCfg{Name: "status", Operation: "==", ValueFrom: "input", Value: "status"}, Parameters: []interfaces.Parameter{{Name: "order_id", ValueFrom: "source_identity"}, {Name: "supplier_id", ValueFrom: "target_identity"}}}
	req := &interfaces.ActionExecutionRequest{KNID: "kn", Branch: "main", DynamicParams: map[string]any{"status": "active"}, RelationTarget: &interfaces.ActionRelationTarget{SourceIdentity: map[string]any{"order_id": "O1"}, TargetIdentity: map[string]any{"supplier_id": "S1"}}}
	instances, data, err := svc.getInstancesForAction(context.Background(), action, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual("S1", data[0]["supplier_id"]) {
		t.Fatalf("expected %v, got %v", "S1", data[0]["supplier_id"])
	}
	params, err := svc.buildExecutionParams(action, data[0], req.DynamicParams, instances[0].InstanceIdentity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(map[string]any{"order_id": "O1", "supplier_id": "S1"}, params) {
		t.Fatalf("expected %v, got %v", map[string]any{"order_id": "O1", "supplier_id": "S1"}, params)
	}
	other := []interfaces.ObjectSystemInfo{{InstanceIdentity: map[string]any{"source": req.RelationTarget.SourceIdentity, "target": map[string]any{"supplier_id": "S2"}}}}
	firstHash, err := computeDuplicateFingerprint(instances, req.DynamicParams, action.OperationConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	secondHash, err := computeDuplicateFingerprint(other, req.DynamicParams, action.OperationConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reflect.DeepEqual(firstHash, secondHash) {
		t.Fatal("different targets must not share a fingerprint")
	}
}

func TestActionParametersFailClosed(t *testing.T) {
	svc := &actionSchedulerService{}
	for _, parameter := range []interfaces.Parameter{{Name: "id", ValueFrom: "property", Value: "missing"}, {Name: "id", ValueFrom: "input"}, {Name: "id", ValueFrom: "unknown"}, {Name: "id", ValueFrom: "source_identity"}} {
		_, err := svc.buildExecutionParams(&interfaces.ActionType{Parameters: []interfaces.Parameter{parameter}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"}}, nil, nil)
		if err == nil {
			t.Fatal("expected an error")
		}
	}
}

func TestRelationAuthorizationIncludesBothEndpoints(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	models.EXPECT().GetRelationType(gomock.Any(), "kn", "main", "supplier").Return(interfaces.RelationType{RTID: "supplier", SourceObjectTypeID: "orders", TargetObjectTypeID: "suppliers"}, true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "orders").Return(actionTargetObject("orders", "order_id"), true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "suppliers").Return(actionTargetObject("suppliers", "supplier_id"), true, nil)
	permissions := &actionPermissionStub{}
	svc := &actionSchedulerService{omAccess: models, permissions: permissions}
	action := &interfaces.ActionType{ATID: "link", ObjectTypeID: "orders", OperationConfig: &interfaces.ActionOperationConfig{Type: "relation", RelationTypeID: "supplier", RelationOperation: "create", AnchorRole: "source"}}
	requirements, err := svc.authorizeActionType(context.Background(), "kn", action)
	if err != nil {
		t.Fatal(err)
	}
	want := []interfaces.PermissionRequirement{
		{ResourceType: "action_type", ResourceID: "kn/link", Operation: "execute"},
		{ResourceType: "object_type", ResourceID: "kn/orders", Operation: "query_data"},
		{ResourceType: "object_type", ResourceID: "kn/suppliers", Operation: "query_data"},
		{ResourceType: "relation_type", ResourceID: "kn/supplier", Operation: "view_detail"},
	}
	if !reflect.DeepEqual(want, requirements) {
		t.Fatalf("requirements = %#v", requirements)
	}
}

func TestMCPReceivesOnlyConfiguredBindings(t *testing.T) {
	ctrl := gomock.NewController(t)
	tools := omock.NewMockAgentOperatorAccess(ctrl)
	action := &interfaces.ActionType{OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"}, ActionSource: interfaces.ActionSource{Type: "mcp", McpID: "mcp", ToolName: "notify"}, Parameters: []interfaces.Parameter{{Name: "message", Type: "string", ValueFrom: "input"}}}
	params, err := (&actionSchedulerService{}).buildExecutionParams(action, nil, map[string]any{"message": "hello", "unconfigured": "must not be forwarded"})
	if err != nil {
		t.Fatal(err)
	}
	tools.EXPECT().ExecuteMCPAsProxy(gomock.Any(), "mcp", "notify", gomock.Any()).DoAndReturn(func(_ context.Context, _, _ string, request interfaces.MCPExecutionRequest) (any, error) {
		if !reflect.DeepEqual(request.Parameters, map[string]any{"message": "hello"}) {
			t.Fatalf("unconfigured MCP input: %#v", request.Parameters)
		}
		return map[string]any{"ok": true}, nil
	})
	if _, err := ExecuteMCP(context.Background(), tools, action, params); err != nil {
		t.Fatal(err)
	}
}
