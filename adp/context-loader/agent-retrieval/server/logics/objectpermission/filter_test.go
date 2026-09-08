// Copyright openbkn.ai

package objectpermission

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type schemaAccessStub struct {
	response *interfaces.ObjectTypeSchemaResp
	err      error
}

func (s schemaAccessStub) GetObjectTypeSchema(context.Context, string, string) (*interfaces.ObjectTypeSchemaResp, error) {
	return s.response, s.err
}

func TestFilterObjectTypesAppliesEffectivePermissionPlan(t *testing.T) {
	objects := []*interfaces.ObjectType{{
		ID:          "customer",
		DataSource:  &interfaces.ResourceInfo{Type: "resource", ID: "customers"},
		PrimaryKeys: []string{"id", "secret"},
		DataProperties: []*interfaces.DataProperty{
			{Name: "id", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeEqual}},
			{Name: "phone", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeLike}},
			{Name: "notes", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeMatch}},
			{Name: "secret", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeEqual}},
		},
		LogicProperties: []*interfaces.LogicPropertyDef{
			{Name: "visible_logic", Parameters: []interfaces.PropertyParameter{{ValueFrom: "property", Value: "id"}}},
			{Name: "hidden_logic", Parameters: []interfaces.PropertyParameter{{ValueFrom: "property", Value: "phone"}}},
		},
	}}
	permissions := map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "phone": interfaces.PropertyAccessMasked,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}

	filtered, err := FilterObjectTypes(context.Background(), schemaAccessStub{response: &interfaces.ObjectTypeSchemaResp{
		EffectivePermissions: permissions,
	}}, "kn-1", objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || len(filtered[0].DataProperties) != 3 {
		t.Fatalf("filtered object types = %#v", filtered)
	}
	if got := filtered[0].DataProperties[0].ConditionOperations; len(got) != 1 {
		t.Fatalf("full property operations = %#v", got)
	}
	if got := filtered[0].DataProperties[1].ConditionOperations; len(got) != 0 {
		t.Fatalf("masked property advertised operations = %#v", got)
	}
	if got := filtered[0].DataProperties[2].ConditionOperations; len(got) != 0 {
		t.Fatalf("schema-only property advertised operations = %#v", got)
	}
	if !reflect.DeepEqual(filtered[0].PrimaryKeys, []string{"id"}) {
		t.Fatalf("primary keys = %#v", filtered[0].PrimaryKeys)
	}
	if len(filtered[0].LogicProperties) != 1 || filtered[0].LogicProperties[0].Name != "visible_logic" {
		t.Fatalf("logic properties = %#v", filtered[0].LogicProperties)
	}
	if _, exists := filtered[0].EffectivePermissions["secret"]; exists {
		t.Fatal("none permission must not be exposed")
	}
	if len(objects[0].DataProperties) != 4 || len(objects[0].LogicProperties) != 2 {
		t.Fatal("source object type was mutated")
	}
}

func TestFilterObjectTypesDeniesUnknownPermission(t *testing.T) {
	objects := []*interfaces.ObjectType{{ID: "customer", DataSource: &interfaces.ResourceInfo{Type: "resource", ID: "customers"}, DataProperties: []*interfaces.DataProperty{{Name: "email"}}}}
	filtered, err := FilterObjectTypes(context.Background(), schemaAccessStub{response: &interfaces.ObjectTypeSchemaResp{
		EffectivePermissions: map[string]interfaces.PropertyAccessLevel{"email": "future-level"},
	}}, "kn-1", objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered[0].DataProperties) != 0 || len(filtered[0].EffectivePermissions) != 0 {
		t.Fatalf("unknown permission was exposed: %#v", filtered[0])
	}
}

func TestFilterObjectTypesKeepsUnboundObjectWithoutProperties(t *testing.T) {
	objects := []*interfaces.ObjectType{{ID: "unbound", DataProperties: []*interfaces.DataProperty{{Name: "secret"}}}}
	filtered, err := FilterObjectTypes(context.Background(), schemaAccessStub{err: fmt.Errorf("must not be called")}, "kn-1", objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || len(filtered[0].DataProperties) != 0 {
		t.Fatalf("unbound object result = %#v", filtered)
	}
}

func TestFilterObjectTypesSupportsLegacyBindingWithoutType(t *testing.T) {
	objects := []*interfaces.ObjectType{{
		ID:         "legacy",
		DataSource: &interfaces.ResourceInfo{ID: "legacy-view"},
		DataProperties: []*interfaces.DataProperty{
			{Name: "visible"}, {Name: "hidden"},
		},
	}}
	filtered, err := FilterObjectTypes(context.Background(), schemaAccessStub{response: &interfaces.ObjectTypeSchemaResp{
		EffectivePermissions: map[string]interfaces.PropertyAccessLevel{"visible": interfaces.PropertyAccessFull},
	}}, "kn-1", objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || len(filtered[0].DataProperties) != 1 || filtered[0].DataProperties[0].Name != "visible" {
		t.Fatalf("legacy resource binding was not authorized: %#v", filtered)
	}
}
