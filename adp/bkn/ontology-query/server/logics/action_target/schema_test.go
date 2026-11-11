// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_target

import (
	"context"
	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"reflect"
	"testing"
)

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
