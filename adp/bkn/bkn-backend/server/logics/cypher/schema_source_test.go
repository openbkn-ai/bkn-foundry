// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"slices"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
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

	// The whole set, unfiltered: the public list keeps only what the caller may
	// view_detail on, so reading it here would hide relation types the caller
	// may query (#3410). The mock fails the test if ListRelationTypes is called.
	relationTypes.EXPECT().
		GetAllRelationTypesByKnID(gomock.Any(), "kn_1", "main").
		Return([]*interfaces.RelationType{relationType("rt_placed", "PLACED")}, nil)

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

// grantQueryData stands in for bkn-safe holding query_data, and nothing else,
// on the listed children. Any other operation is refused, so a schema load
// that still asked for view_detail would lose the relation type.
func grantQueryData(t *testing.T, ps *bmock.MockPermissionService, granted ...string) {
	t.Helper()
	ps.EXPECT().FilterVisibleResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, ids, ops []string) (map[string]interfaces.PermissionResourceOps, error) {
			matched := map[string]interfaces.PermissionResourceOps{}
			if !slices.Equal(ops, []string{interfaces.OPERATION_TYPE_QUERY_DATA}) {
				return matched, nil
			}
			for _, id := range ids {
				if slices.Contains(granted, id) {
					matched[id] = interfaces.PermissionResourceOps{ResourceID: id,
						Operations: []string{interfaces.OPERATION_TYPE_QUERY_DATA}}
				}
			}
			return matched, nil
		}).AnyTimes()
}

// A caller holding query_data on a relation type and both its ends, without
// view_detail on any of them, may query that relation; a caller missing
// query_data on the relation or on either end still finds it unknown (#3410).
func TestLoadSchemaResolvesRelationTypeWithQueryDataOnly(t *testing.T) {
	cases := []struct {
		name     string
		granted  []string
		resolves bool
	}{
		{"relation and both ends", []string{"kn_1/ot_order", "kn_1/ot_customer", "kn_1/rt_placed"}, true},
		{"no query_data on the relation", []string{"kn_1/ot_order", "kn_1/ot_customer"}, false},
		{"no query_data on the source", []string{"kn_1/ot_customer", "kn_1/rt_placed"}, false},
		{"no query_data on the target", []string{"kn_1/ot_order", "kn_1/rt_placed"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			objectTypes := bmock.NewMockObjectTypeService(ctrl)
			relationTypes := bmock.NewMockRelationTypeService(ctrl)
			ps := bmock.NewMockPermissionService(ctrl)

			objectTypes.EXPECT().GetAllObjectTypesByKnID(gomock.Any(), "kn_1", "main").
				Return(map[string]*interfaces.ObjectType{
					"ot_order":    objectType("ot_order", "Order", resource("res_order", "orders")),
					"ot_customer": objectType("ot_customer", "Customer", resource("res_customer", "customers")),
				}, nil)
			relationTypes.EXPECT().GetAllRelationTypesByKnID(gomock.Any(), "kn_1", "main").
				Return([]*interfaces.RelationType{relationType("rt_placed", "PLACED")}, nil)
			grantQueryData(t, ps, tc.granted...)

			schema, err := LoadSchema(context.Background(), NewSchemaSource(objectTypes, relationTypes),
				&permissionVisibility{ps: ps}, "kn_1", "main")
			if err != nil {
				t.Fatalf("LoadSchema: %v", err)
			}
			rt, err := schema.ResolveRelationType("rt_placed", func(*interfaces.RelationType) bool { return true })
			if tc.resolves {
				if err != nil || rt == nil || rt.RTID != "rt_placed" {
					t.Fatalf("ResolveRelationType = %v, %v; want rt_placed", rt, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "unknown relationship type") {
				t.Fatalf("ResolveRelationType = %v, %v; want unknown relationship type", rt, err)
			}
		})
	}
}
