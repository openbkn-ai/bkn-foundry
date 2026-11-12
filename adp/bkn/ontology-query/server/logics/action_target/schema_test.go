// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_target

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

func TestCreateSchemaOnlyExposesMappedObjectProperties(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	object := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "brands", OTName: "Brands", DataProperties: []cond.DataProperty{
			{Name: "brand_name", DisplayName: "Brand name", Type: "string"},
			{Name: "country", DisplayName: "Country", Type: "string"},
		},
	}}
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "brands").Return(object, true, nil).Times(2)
	action := &interfaces.ActionType{ActionType: "add", ObjectTypeID: "brands", OperationConfig: &interfaces.ActionOperationConfig{Type: "object"}, Parameters: []interfaces.Parameter{
		{Name: "name", ValueFrom: "property", Value: "brand_name"},
		{Name: "label", ValueFrom: "property", Value: "brand_name"},
	}}
	schema, err := (&Resolver{Models: models}).ExecutionSchema(context.Background(), action, "kn", "main")
	if err != nil {
		t.Fatal(err)
	}
	input := schema["properties"].(map[string]any)["object_input"].(map[string]any)
	fields := input["properties"].(map[string]any)
	if len(fields) != 1 || fields["brand_name"] == nil || fields["country"] != nil {
		t.Fatalf("object_input exposes fields without tool mappings: %#v", fields)
	}
	if !reflect.DeepEqual(input["required"], []string{"brand_name"}) {
		t.Fatalf("required=%#v", input["required"])
	}
	action.Parameters = nil
	schema, err = (&Resolver{Models: models}).ExecutionSchema(context.Background(), action, "kn", "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := schema["properties"].(map[string]any)["object_input"]; exists {
		t.Fatal("object_input must be absent when no object property is mapped")
	}
}

func TestCreateRejectsUnmappedObjectInput(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	object := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "brands", DataProperties: []cond.DataProperty{{Name: "brand_name", Type: "string"}, {Name: "country", Type: "string"}},
	}}
	models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "brands").Return(object, true, nil).Times(2)
	resolver := &Resolver{Models: models}
	action := &interfaces.ActionType{ActionType: "add", ObjectTypeID: "brands", OperationConfig: &interfaces.ActionOperationConfig{Type: "object"}, Parameters: []interfaces.Parameter{
		{Name: "name", ValueFrom: "property", Value: "brand_name"},
	}}
	req := &interfaces.ActionExecutionRequest{KNID: "kn", Branch: "main", ObjectInput: map[string]any{"brand_name": "Acme", "country": "CN"}}
	_, _, err := resolver.Resolve(context.Background(), action, req)
	if err == nil || !strings.Contains(err.Error(), "not mapped to an execution parameter") {
		t.Fatalf("expected unmapped input error, got %v", err)
	}
	req.ObjectInput = map[string]any{"brand_name": "Acme"}
	_, data, err := resolver.Resolve(context.Background(), action, req)
	if err != nil || !reflect.DeepEqual(data, []map[string]any{{"brand_name": "Acme"}}) {
		t.Fatalf("mapped input should be accepted; data=%#v err=%v", data, err)
	}
}

func TestExecutionSchemaUsesNestedRuntimePathsWithoutQueryingObjects(t *testing.T) {
	action := &interfaces.ActionType{OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"}, Parameters: []interfaces.Parameter{
		{Name: "body.notify", Type: "boolean", ValueFrom: "input"}, {Name: "body.message", Type: "string", ValueFrom: "input"},
	}}
	schema, err := (&Resolver{}).ExecutionSchema(context.Background(), action, "kn", "main")
	if err != nil {
		t.Fatal(err)
	}
	inputs := schema["properties"].(map[string]any)["dynamic_params"].(map[string]any)
	if !reflect.DeepEqual(inputs["required"], []string{"body"}) {
		t.Fatalf("required=%#v", inputs["required"])
	}
	body := inputs["properties"].(map[string]any)["body"].(map[string]any)
	if !reflect.DeepEqual(body["required"], []string{"notify", "message"}) {
		t.Fatalf("body=%#v", body)
	}
	action.Parameters = append(action.Parameters, interfaces.Parameter{Name: "body", ValueFrom: "input", Type: "object"})
	if _, err := (&Resolver{}).ExecutionSchema(context.Background(), action, "kn", "main"); err == nil {
		t.Fatal("overlapping paths must be rejected")
	}
}

func TestIdentitySchemaPreservesPrimaryKeyTypeAndDisplayNames(t *testing.T) {
	object := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "orders", OTName: "Orders", PrimaryKeys: []string{"id"}, DataProperties: []cond.DataProperty{{Name: "id", DisplayName: "Order number", Type: "int64"}}}}
	schema, err := identitySchema(object)
	if err != nil {
		t.Fatal(err)
	}
	field := schema["properties"].(map[string]any)["id"].(map[string]any)
	if schema["title"] != "Orders" || field["title"] != "Order number" || field["type"] != "integer" {
		t.Fatalf("schema=%#v", schema)
	}
}

func TestExistingActionLikeUsesBackingResourceCollation(t *testing.T) {
	for _, matches := range []bool{true, false} {
		t.Run(map[bool]string{true: "source matches", false: "source rejects"}[matches], func(t *testing.T) {
			ctrl := gomock.NewController(t)
			objects := omock.NewMockObjectTypeService(ctrl)
			data := map[string]any{"id": "1", "name": "Apple", interfaces.SYSTEM_PROPERTY_INSTANCE_ID: "obj-1"}
			object := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID: "products", PrimaryKeys: []string{"id"}, DataProperties: []cond.DataProperty{
					{Name: "id", Type: "string"}, {Name: "name", Type: "string"},
				},
			}}
			objects.EXPECT().GetObjectsByObjectTypeID(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
					if query.ActualCondition.Operation != cond.OperationAnd {
						t.Fatalf("identity query changed: %#v", query.ActualCondition)
					}
					return interfaces.Objects{Datas: []map[string]any{data}}, nil
				})
			objects.EXPECT().GetObjectsByObjectTypeID(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
					if query.ActualCondition.Operation != cond.OperationAnd || len(query.ActualCondition.SubConds) != 2 ||
						query.ActualCondition.SubConds[1].Value != "apple" {
						t.Fatalf("LIKE condition was not sent to Vega: %#v", query.ActualCondition)
					}
					if matches {
						return interfaces.Objects{Datas: []map[string]any{data}}, nil
					}
					return interfaces.Objects{}, nil
				})
			resolver := &Resolver{Objects: objects}
			_, _, err := resolver.loadActionTarget(context.Background(),
				&interfaces.ActionExecutionRequest{KNID: "kn", Branch: "main"}, object,
				map[string]any{"id": "1"},
				&cond.CondCfg{Name: "name", Operation: cond.OperationLike, Value: "apple"}, nil)
			if (err == nil) != matches {
				t.Fatalf("source matches=%v, err=%v", matches, err)
			}
		})
	}
}
