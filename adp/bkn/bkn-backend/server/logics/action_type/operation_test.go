// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/stretchr/testify/require"
)

func TestResolveActionRelationDirection(t *testing.T) {
	relation := &interfaces.RelationType{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "supplier", SourceObjectTypeID: "orders", TargetObjectTypeID: "suppliers"}}
	for _, tc := range []struct {
		current, role, other string
		invalid              bool
	}{
		{"orders", "", "suppliers", false}, {"suppliers", "", "orders", false},
		{"orders", "target", "", true}, {"unrelated", "", "", true},
	} {
		t.Run(tc.current+tc.role, func(t *testing.T) {
			cfg := &interfaces.ActionOperationConfig{AnchorRole: tc.role}
			other, err := resolveActionRelation(cfg, tc.current, relation)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.other, other)
				require.NotEmpty(t, cfg.AnchorRole)
			}
		})
	}
	relation.TargetObjectTypeID = "orders"
	_, err := resolveActionRelation(&interfaces.ActionOperationConfig{}, "orders", relation)
	require.Error(t, err, "self relations must not guess which instance is current")
	_, err = resolveActionRelation(&interfaces.ActionOperationConfig{AnchorRole: "target"}, "orders", relation)
	require.NoError(t, err)
}

func TestActionOperationUsesEndpointSchema(t *testing.T) {
	object := func(id, key string) *interfaces.ObjectType {
		return &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id, PrimaryKeys: []string{key}, DataProperties: []*interfaces.DataProperty{{Name: key, Type: "string"}, {Name: "status", Type: "string"}}}}
	}
	orders, suppliers := object("orders", "order_id"), object("suppliers", "supplier_id")
	relation := &interfaces.RelationType{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "supplier", SourceObjectTypeID: "orders", TargetObjectTypeID: "suppliers"}}
	batch := &interfaces.BatchIDIndex{ObjectTypes: map[string]*interfaces.ObjectType{"orders": orders, "suppliers": suppliers}, RelationTypes: map[string]*interfaces.RelationType{"supplier": relation}}
	at := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
		ObjectTypeID:    "orders",
		OperationConfig: &interfaces.ActionOperationConfig{Type: "relation", RelationTypeID: "supplier", RelationOperation: "create", OtherObjectCondition: &interfaces.ActionCondCfg{Field: "status", Operation: "==", Value: "active"}},
		Parameters:      []interfaces.Parameter{{Name: "order_id", Type: "string", ValueFrom: "source_identity"}, {Name: "supplier_id", Type: "string", ValueFrom: "target_identity"}},
	}}
	svc := &actionTypeService{}
	require.NoError(t, svc.checkOperationDependencies(context.Background(), nil, at, batch))
	require.Equal(t, "source", at.OperationConfig.AnchorRole)
	require.Equal(t, "suppliers", at.OperationConfig.OtherObjectCondition.ObjectTypeID)
	suppliers.PrimaryKeys = []string{"supplier_id", "status"}
	require.ErrorContains(t, svc.checkOperationDependencies(context.Background(), nil, at, batch), "exactly one primary key")
	suppliers.PrimaryKeys = []string{"supplier_id"}
	at.Parameters[1].Type = "integer"
	require.ErrorContains(t, svc.checkOperationDependencies(context.Background(), nil, at, batch), "incompatible")
}
