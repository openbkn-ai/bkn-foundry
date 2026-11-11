// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_type

import (
	"context"
	"reflect"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

func Test_NewActionTypeService(t *testing.T) {
	Convey("Test NewActionTypeService", t, func() {
		appSetting := &common.AppSetting{}

		Convey("成功 - 创建服务实例", func() {
			service := NewActionTypeService(appSetting)
			So(service, ShouldNotBeNil)
		})

		Convey("成功 - 单例模式", func() {
			service1 := NewActionTypeService(appSetting)
			service2 := NewActionTypeService(appSetting)
			So(service1, ShouldEqual, service2)
		})
	})
}

// Discovery is definition-first. Applicability conditions do not select an
// implicit target set, and dynamic values are only mandatory at execution.
func TestActionDiscoveryDoesNotScanOrRequireDynamicValues(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	object := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "orders", PrimaryKeys: []string{"order_id"}, DataProperties: []cond.DataProperty{{Name: "order_id", Type: "string"}, {Name: "status", Type: "string"}},
	}}
	action := interfaces.ActionType{ATID: "modify_order", ActionType: "modify", ObjectTypeID: "orders", OperationConfig: &interfaces.ActionOperationConfig{Type: "object"},
		Condition:  &cond.CondCfg{Name: "status", Operation: "==", ValueFrom: "input", Value: "status"},
		Parameters: []interfaces.Parameter{{Name: "status", Type: "string", ValueFrom: "input"}},
	}
	models.EXPECT().GetActionType(gomock.Any(), "kn", "main", "modify_order").Return(action, nil, true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "orders").Return(object, true, nil)
	service := &actionTypeService{omAccess: models}
	result, err := service.GetActionsByActionTypeID(context.Background(), &interfaces.ActionQuery{KNID: "kn", Branch: "main", ActionTypeID: "modify_order", CommonQueryParameters: interfaces.CommonQueryParameters{IncludeTypeInfo: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Actions) != 1 || result.ActionType == nil || result.ExecutionSchema == nil {
		t.Fatalf("incomplete definition response: %#v", result)
	}
	if value, exists := result.Actions[0].DynamicParams["status"]; !exists || value != nil {
		t.Fatalf("dynamic placeholder = %#v", result.Actions[0].DynamicParams)
	}
	properties := result.ExecutionSchema["properties"].(map[string]any)
	if properties["_instance_identities"] == nil || properties["dynamic_params"] == nil {
		t.Fatalf("missing execution inputs: %#v", properties)
	}
}

func TestActionDiscoveryLoadsOnlyExplicitTargetsWithFullPropertyAccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	objects := omock.NewMockObjectTypeService(ctrl)
	object := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "orders", PrimaryKeys: []string{"order_id"}, DataProperties: []cond.DataProperty{{Name: "order_id", Type: "string"}, {Name: "status", Type: "string"}},
	}}
	action := interfaces.ActionType{ATID: "modify_order", ActionType: "modify", ObjectTypeID: "orders", OperationConfig: &interfaces.ActionOperationConfig{Type: "object"},
		Condition:  &cond.CondCfg{Name: "status", Operation: "==", ValueFrom: "input", Value: "new_status"},
		Parameters: []interfaces.Parameter{{Name: "status", Type: "string", ValueFrom: "property", Value: "status"}, {Name: "new_status", Type: "string", ValueFrom: "input"}},
	}
	models.EXPECT().GetActionType(gomock.Any(), "kn", "main", "modify_order").Return(action, nil, true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "orders").Return(object, true, nil).Times(2)
	objects.EXPECT().GetObjectsByObjectTypeID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
		if !reflect.DeepEqual(query.RequiredFullProperties, []string{"order_id", "status"}) {
			t.Fatalf("full property requirements = %#v", query.RequiredFullProperties)
		}
		return interfaces.Objects{Datas: []map[string]any{{"order_id": "O1", "status": "pending"}}}, nil
	})
	service := &actionTypeService{omAccess: models, ots: objects}
	response, err := service.GetActionsByActionTypeID(context.Background(), &interfaces.ActionQuery{KNID: "kn", Branch: "main", ActionTypeID: "modify_order", InstanceIdentities: []map[string]any{{"order_id": "O1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Actions[0].Parameters["status"] != "pending" || response.Actions[0].DynamicParams["new_status"] != nil {
		t.Fatalf("unexpected preview: %#v", response)
	}
}

func TestActionDiscoveryRejectsUnmigratedDefinition(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	models.EXPECT().GetActionType(gomock.Any(), "kn", "main", "old").Return(interfaces.ActionType{ATID: "old"}, nil, true, nil)
	service := &actionTypeService{omAccess: models}
	if _, err := service.GetActionsByActionTypeID(context.Background(), &interfaces.ActionQuery{KNID: "kn", Branch: "main", ActionTypeID: "old"}); err == nil {
		t.Fatal("unmigrated definitions must not be inferred at runtime")
	}
}
