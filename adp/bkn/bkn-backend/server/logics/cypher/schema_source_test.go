// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestSchemaSourceReadsWholeNetwork(t *testing.T) {
	ctrl := gomock.NewController(t)
	objectTypes := bmock.NewMockObjectTypeService(ctrl)
	relationTypes := bmock.NewMockRelationTypeService(ctrl)

	objectTypes.EXPECT().
		GetAllObjectTypesByKnID(gomock.Any(), "kn_1", "main").
		Return(map[string]*interfaces.ObjectType{
			"ot_order": objectType("ot_order", "Order", resource("res_order", "orders")),
		}, nil)

	// Limit -1 asks for every relation type. Paging here would hide some of
	// them, and a pattern may name any one, so the query would fail with an
	// unknown relationship type that the model does contain.
	relationTypes.EXPECT().
		ListRelationTypes(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, query interfaces.RelationTypesQueryParams) ([]*interfaces.RelationType, int, error) {
			if query.Limit != -1 {
				t.Fatalf("limit = %d, want -1 for the whole set", query.Limit)
			}
			if query.KNID != "kn_1" || query.Branch != "main" {
				t.Fatalf("query = %+v, want the requested network and branch", query)
			}
			return []*interfaces.RelationType{relationType("rt_placed", "PLACED")}, 1, nil
		})

	source := NewSchemaSource(objectTypes, relationTypes)

	gotObjectTypes, err := source.AllObjectTypes(context.Background(), "kn_1", "main")
	if err != nil || len(gotObjectTypes) != 1 || gotObjectTypes[0].OTID != "ot_order" {
		t.Fatalf("AllObjectTypes = %v, %v", gotObjectTypes, err)
	}

	gotRelationTypes, err := source.AllRelationTypes(context.Background(), "kn_1", "main")
	if err != nil || len(gotRelationTypes) != 1 || gotRelationTypes[0].RTID != "rt_placed" {
		t.Fatalf("AllRelationTypes = %v, %v", gotRelationTypes, err)
	}
}
