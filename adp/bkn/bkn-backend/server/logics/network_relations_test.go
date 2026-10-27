// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// The fakes hold the stored definitions of one network and answer the reads NetworkRelations makes.

type fakeObjectTypes struct {
	interfaces.ObjectTypeAccess
	stored []*interfaces.ObjectType
}

func (f *fakeObjectTypes) ListDeprecatedObjectTypes(context.Context, string, string) ([]*interfaces.ObjectType, error) {
	var out []*interfaces.ObjectType
	for _, ot := range f.stored {
		if ot.Lifecycle != nil && ot.Lifecycle.Status == interfaces.LIFECYCLE_DEPRECATED {
			out = append(out, ot)
		}
	}
	return out, nil
}

func (f *fakeObjectTypes) GetObjectTypeIdentitiesByIDsOrNames(_ context.Context, _, _ string, ids, _ []string) ([]*interfaces.ObjectType, error) {
	var out []*interfaces.ObjectType
	for _, ot := range f.stored {
		if slices.Contains(ids, ot.OTID) {
			out = append(out, &interfaces.ObjectType{ObjectTypeWithKeyField: ot.ObjectTypeWithKeyField})
		}
	}
	return out, nil
}

type fakeActionTypes struct {
	interfaces.ActionTypeAccess
	stored []*interfaces.ActionType
}

func (f *fakeActionTypes) ListDeprecatedActionTypes(context.Context, string, string) ([]*interfaces.ActionType, error) {
	var out []*interfaces.ActionType
	for _, at := range f.stored {
		if at.Lifecycle != nil && at.Lifecycle.Status == interfaces.LIFECYCLE_DEPRECATED {
			out = append(out, at)
		}
	}
	return out, nil
}

func (f *fakeActionTypes) GetActionTypeIdentitiesByIDsOrNames(_ context.Context, _, _ string, ids, _ []string) ([]*interfaces.ActionType, error) {
	var out []*interfaces.ActionType
	for _, at := range f.stored {
		if slices.Contains(ids, at.ATID) {
			out = append(out, &interfaces.ActionType{ActionTypeWithKeyField: at.ActionTypeWithKeyField})
		}
	}
	return out, nil
}

type fakeRelationTypes struct {
	interfaces.RelationTypeAccess
	stored []*interfaces.RelationType
	// reads counts the reads beyond the list of deprecated and fallback relation types.
	reads int
}

func (f *fakeRelationTypes) ListDeprecatedOrFallbackRelationTypeIDs(context.Context, string, string) ([]string, error) {
	var ids []string
	for _, rt := range f.stored {
		if (rt.Lifecycle != nil && rt.Lifecycle.Status == interfaces.LIFECYCLE_DEPRECATED) || rt.FallbackFor != "" {
			ids = append(ids, rt.RTID)
		}
	}
	return ids, nil
}

func (f *fakeRelationTypes) GetRelationTypesByIDs(_ context.Context, _, _ string, ids []string) ([]*interfaces.RelationType, error) {
	f.reads++
	var out []*interfaces.RelationType
	for _, rt := range f.stored {
		if slices.Contains(ids, rt.RTID) {
			out = append(out, rt)
		}
	}
	return out, nil
}

func (f *fakeRelationTypes) ListRelationTypes(_ context.Context, query interfaces.RelationTypesQueryParams) ([]*interfaces.RelationType, error) {
	f.reads++
	var out []*interfaces.RelationType
	for _, rt := range f.stored {
		if slices.Contains(query.BoundObjectTypeIDs, rt.SourceObjectTypeID) || slices.Contains(query.BoundObjectTypeIDs, rt.TargetObjectTypeID) {
			out = append(out, rt)
		}
	}
	return out, nil
}

func objectType(id string, lc *interfaces.Lifecycle) *interfaces.ObjectType {
	ot := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id, OTName: id}}
	ot.Lifecycle = lc
	return ot
}

func actionType(id string, lc *interfaces.Lifecycle) *interfaces.ActionType {
	at := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: id, ATName: id}}
	at.Lifecycle = lc
	return at
}

func relationType(id, source, target string, decl interfaces.RelationDeclarations) *interfaces.RelationType {
	return &interfaces.RelationType{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: id, RTName: id, SourceObjectTypeID: source, TargetObjectTypeID: target, Type: "direct",
		},
		RelationDeclarations: decl,
	}
}

func deprecated(replacedBy string) *interfaces.Lifecycle {
	return &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED, Reason: "retired", ReplacedBy: replacedBy}
}

type storedNetwork struct {
	objects   []*interfaces.ObjectType
	relations []*interfaces.RelationType
	actions   []*interfaces.ActionType
}

func (n storedNetwork) checker() (*NetworkRelations, *fakeRelationTypes) {
	rta := &fakeRelationTypes{stored: n.relations}
	return &NetworkRelations{
		OTA: &fakeObjectTypes{stored: n.objects},
		RTA: rta,
		ATA: &fakeActionTypes{stored: n.actions},
	}, rta
}

// A supply network: an order ships from a warehouse (primary), or through its site (fallback).
func supplyNetwork() storedNetwork {
	return storedNetwork{
		objects: []*interfaces.ObjectType{objectType("order", nil), objectType("warehouse", nil), objectType("legacy_order", deprecated("order"))},
		relations: []*interfaces.RelationType{
			relationType("ships_from", "order", "warehouse", interfaces.RelationDeclarations{Cardinality: "many_to_one"}),
			relationType("ships_via_site", "order", "warehouse", interfaces.RelationDeclarations{Importance: "secondary", FallbackFor: "ships_from"}),
		},
		actions: []*interfaces.ActionType{actionType("ship", nil), actionType("ship_v1", deprecated("ship"))},
	}
}

func TestNetworkRelationsCheck(t *testing.T) {
	cases := []struct {
		name  string
		saved SavedDefinitions
		want  []string // a fragment of each issue, in order
	}{
		{"an unrelated save", SavedDefinitions{ObjectTypes: []*interfaces.ObjectType{objectType("customer", nil)}}, nil},
		{"deprecating an object type a relation in use is bound to",
			SavedDefinitions{ObjectTypes: []*interfaces.ObjectType{objectType("warehouse", deprecated(""))}},
			[]string{`relation type "ships_from": endpoint object type "warehouse" is deprecated`,
				`relation type "ships_via_site": endpoint object type "warehouse" is deprecated`}},
		{"deprecating the object type another one is replaced by",
			SavedDefinitions{ObjectTypes: []*interfaces.ObjectType{objectType("order", deprecated(""))}},
			[]string{`object type "legacy_order" lifecycle replaced_by "order": that object type is deprecated too`,
				`relation type "ships_from": endpoint object type "order" is deprecated`,
				`relation type "ships_via_site": endpoint object type "order" is deprecated`}},
		{"a replaced_by naming no object type",
			SavedDefinitions{ObjectTypes: []*interfaces.ObjectType{objectType("customer", deprecated("client"))}},
			[]string{`object type "customer" lifecycle replaced_by "client": no object type has this id`}},
		{"a replaced_by naming a stored object type in use",
			SavedDefinitions{ObjectTypes: []*interfaces.ObjectType{objectType("customer", deprecated("order"))}}, nil},
		{"deprecating the action type another one is replaced by",
			SavedDefinitions{ActionTypes: []*interfaces.ActionType{actionType("ship", deprecated(""))}},
			[]string{`action type "ship_v1" lifecycle replaced_by "ship": that action type is deprecated too`}},
		{"making the primary relation of a fallback path secondary",
			SavedDefinitions{RelationTypes: []*interfaces.RelationType{
				relationType("ships_from", "order", "warehouse", interfaces.RelationDeclarations{Cardinality: "many_to_one", Importance: "secondary"})}},
			[]string{`relation type "ships_via_site" fallback_for "ships_from": the relation it stands in for is secondary`}},
		{"a second fallback path for the same primary relation",
			SavedDefinitions{RelationTypes: []*interfaces.RelationType{
				relationType("ships_by_carrier", "order", "warehouse", interfaces.RelationDeclarations{Importance: "secondary", FallbackFor: "ships_from"})}},
			[]string{`is already its fallback path`}},
		{"a fallback path naming no relation type",
			SavedDefinitions{RelationTypes: []*interfaces.RelationType{
				relationType("ships_by_carrier", "order", "warehouse", interfaces.RelationDeclarations{Importance: "secondary", FallbackFor: "ships_by_air"})}},
			[]string{`relation type "ships_by_carrier" fallback_for "ships_by_air": no relation type has this id`}},
		{"a relation in use bound to a deprecated object type",
			SavedDefinitions{RelationTypes: []*interfaces.RelationType{relationType("placed_as", "legacy_order", "order", interfaces.RelationDeclarations{})}},
			[]string{`relation type "placed_as": endpoint object type "legacy_order" is deprecated`}},
		{"one save deprecating an object type and the relations bound to it",
			SavedDefinitions{
				ObjectTypes: []*interfaces.ObjectType{objectType("warehouse", deprecated(""))},
				RelationTypes: []*interfaces.RelationType{
					relationType("ships_from", "order", "warehouse", interfaces.RelationDeclarations{Cardinality: "many_to_one", Lifecycle: deprecated("")}),
					relationType("ships_via_site", "order", "warehouse", interfaces.RelationDeclarations{Lifecycle: deprecated("")}),
				},
			}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nr, _ := supplyNetwork().checker()
			got, err := nr.Check(context.Background(), "kn1", "main", tc.saved)
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Check() = %q, want %d issues %q", got, len(tc.want), tc.want)
			}
			for i, fragment := range tc.want {
				if !strings.Contains(got[i], fragment) {
					t.Errorf("issue %d = %q, want it to contain %q", i, got[i], fragment)
				}
			}
		})
	}
}

func TestNetworkRelationsCheckIgnoresIssuesBetweenStoredDefinitions(t *testing.T) {
	network := supplyNetwork()
	// Stored before the check existed: a relation in use bound to a deprecated object type.
	network.relations = append(network.relations, relationType("placed_as", "legacy_order", "order", interfaces.RelationDeclarations{}))
	nr, _ := network.checker()
	got, err := nr.Check(context.Background(), "kn1", "main",
		SavedDefinitions{ObjectTypes: []*interfaces.ObjectType{objectType("customer", nil)}})
	if err != nil || len(got) != 0 {
		t.Fatalf("Check() = %q, %v; want no issue, the save writes none of them", got, err)
	}
}

func TestNetworkRelationsCheckReadsLittleWithoutDeclarations(t *testing.T) {
	network := storedNetwork{
		objects:   []*interfaces.ObjectType{objectType("order", nil), objectType("warehouse", nil)},
		relations: []*interfaces.RelationType{relationType("ships_from", "order", "warehouse", interfaces.RelationDeclarations{})},
	}
	nr, rta := network.checker()
	got, err := nr.Check(context.Background(), "kn1", "main", SavedDefinitions{
		ObjectTypes:   []*interfaces.ObjectType{objectType("order", nil)},
		RelationTypes: []*interfaces.RelationType{relationType("ships_from", "order", "warehouse", interfaces.RelationDeclarations{})},
	})
	if err != nil || len(got) != 0 {
		t.Fatalf("Check() = %q, %v; want no issue", got, err)
	}
	if rta.reads != 0 {
		t.Errorf("Check() read relation types %d more times; a network without declarations needs only the lists", rta.reads)
	}
}

func TestNetworkRelationsDeferredSave(t *testing.T) {
	nr, _ := supplyNetwork().checker()
	ctx, written := WithDeferredNetworkRelations(context.Background())
	// The object types of an import come first: checked alone, deprecating warehouse would be refused
	// for the relations the same import deprecates next.
	objects := SavedDefinitions{ObjectTypes: []*interfaces.ObjectType{objectType("warehouse", deprecated(""))}}
	relations := SavedDefinitions{RelationTypes: []*interfaces.RelationType{
		relationType("ships_from", "order", "warehouse", interfaces.RelationDeclarations{Cardinality: "many_to_one", Lifecycle: deprecated("")}),
		relationType("ships_via_site", "order", "warehouse", interfaces.RelationDeclarations{Lifecycle: deprecated("")}),
	}}
	for _, saved := range []SavedDefinitions{objects, relations} {
		if got, err := nr.CheckSave(ctx, "kn1", "main", saved); err != nil || len(got) != 0 {
			t.Fatalf("CheckSave() = %q, %v; want it recorded, not checked", got, err)
		}
	}
	got, err := nr.Check(ctx, "kn1", "main", *written)
	if err != nil || len(got) != 0 {
		t.Fatalf("Check() of the whole import = %q, %v; want no issue", got, err)
	}

	// Without the relations, the import is refused.
	ctx, written = WithDeferredNetworkRelations(context.Background())
	_, _ = nr.CheckSave(ctx, "kn1", "main", objects)
	if got, _ := nr.Check(ctx, "kn1", "main", *written); len(got) != 2 {
		t.Fatalf("Check() = %q, want the two relations bound to warehouse", got)
	}
}

func TestNetworkRelationsDeleteReferrers(t *testing.T) {
	cases := []struct {
		name string
		kind string
		ids  []string
		want []string
	}{
		{"an object type nothing refers to", interfaces.RESOURCE_TYPE_OBJECT_TYPE, []string{"warehouse"}, nil},
		{"the object type another one is replaced by", interfaces.RESOURCE_TYPE_OBJECT_TYPE, []string{"order"},
			[]string{`object type "order" is the lifecycle replaced_by of object type "legacy_order"`}},
		{"an object type with the one replaced by it", interfaces.RESOURCE_TYPE_OBJECT_TYPE, []string{"order", "legacy_order"}, nil},
		{"the action type another one is replaced by", interfaces.RESOURCE_TYPE_ACTION_TYPE, []string{"ship"},
			[]string{`action type "ship" is the lifecycle replaced_by of action type "ship_v1"`}},
		{"the primary relation of a fallback path", interfaces.RESOURCE_TYPE_RELATION_TYPE, []string{"ships_from"},
			[]string{`relation type "ships_from" is the fallback_for of relation type "ships_via_site"`}},
		{"a primary relation with its fallback path", interfaces.RESOURCE_TYPE_RELATION_TYPE, []string{"ships_from", "ships_via_site"}, nil},
		{"a fallback path", interfaces.RESOURCE_TYPE_RELATION_TYPE, []string{"ships_via_site"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nr, _ := supplyNetwork().checker()
			got, err := nr.DeleteReferrers(context.Background(), "kn1", "main", tc.kind, tc.ids)
			if err != nil {
				t.Fatalf("DeleteReferrers() error = %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("DeleteReferrers() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNilNetworkRelationsChecksNothing(t *testing.T) {
	var nr *NetworkRelations
	saved := SavedDefinitions{ObjectTypes: []*interfaces.ObjectType{objectType("order", deprecated("missing"))}}
	if got, err := nr.Check(context.Background(), "kn1", "main", saved); err != nil || got != nil {
		t.Fatalf("Check() = %q, %v", got, err)
	}
	if got, err := nr.DeleteReferrers(context.Background(), "kn1", "main", interfaces.RESOURCE_TYPE_OBJECT_TYPE, []string{"order"}); err != nil || got != nil {
		t.Fatalf("DeleteReferrers() = %q, %v", got, err)
	}
}
