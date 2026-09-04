// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"strings"
	"testing"

	"bkn-backend/interfaces"
)

type fakeSchemaSource struct {
	objectTypes   []*interfaces.ObjectType
	relationTypes []*interfaces.RelationType
	err           error
}

func (f *fakeSchemaSource) AllObjectTypes(context.Context, string, string) ([]*interfaces.ObjectType, error) {
	return f.objectTypes, f.err
}

func (f *fakeSchemaSource) AllRelationTypes(context.Context, string, string) ([]*interfaces.RelationType, error) {
	return f.relationTypes, f.err
}

func objectType(id, name string, dataSource *interfaces.ResourceInfo, props ...*interfaces.DataProperty) *interfaces.ObjectType {
	return &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID:           id,
			OTName:         name,
			DataSource:     dataSource,
			DataProperties: props,
		},
	}
}

func dataProperty(name, column string) *interfaces.DataProperty {
	return &interfaces.DataProperty{
		Name:        name,
		DisplayName: name + " display",
		Type:        "string",
		MappedField: &interfaces.Field{Name: column},
	}
}

func relationType(id, name string) *interfaces.RelationType {
	return &interfaces.RelationType{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID:   id,
			RTName: name,
			Type:   interfaces.RELATION_TYPE_DIRECT,
		},
	}
}

func resource(id, name string) *interfaces.ResourceInfo {
	return &interfaces.ResourceInfo{ID: id, Name: name}
}

func testSchema(t *testing.T, src *fakeSchemaSource) *Schema {
	t.Helper()
	s, err := LoadSchema(context.Background(), src, "kn_test", "main")
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	return s
}

func TestResolveLabel(t *testing.T) {
	src := &fakeSchemaSource{objectTypes: []*interfaces.ObjectType{
		objectType("ot_order", "Order", resource("res_order", "orders")),
		objectType("ot_customer", "Customer", resource("res_customer", "customers")),
		// Its name is another object type's id, which is the collision the
		// id-first rule has to report rather than pick a side on.
		objectType("ot_shadow", "ot_order", resource("res_shadow", "shadow")),
	}}
	schema := testSchema(t, src)

	for _, tc := range []struct {
		name    string
		label   string
		wantOT  string
		wantErr string
	}{
		{name: "by id", label: "ot_customer", wantOT: "ot_customer"},
		{name: "by name", label: "Customer", wantOT: "ot_customer"},
		{name: "id wins but collision is reported", label: "ot_order", wantErr: "ambiguous"},
		{name: "unknown", label: "Invoice", wantErr: `unknown label "Invoice"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ot, err := schema.ResolveLabel(tc.label)
			switch {
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ResolveLabel(%q) = %v, want error containing %q", tc.label, err, tc.wantErr)
				}
			case err != nil:
				t.Fatalf("ResolveLabel(%q): %v", tc.label, err)
			case ot.OTID != tc.wantOT:
				t.Fatalf("ResolveLabel(%q) = %q, want %q", tc.label, ot.OTID, tc.wantOT)
			}
		})
	}
}

// An object type whose id and name are the same string resolves to itself, and
// must not be mistaken for a collision between two object types.
func TestResolveLabelSelfMatchIsNotAmbiguous(t *testing.T) {
	src := &fakeSchemaSource{objectTypes: []*interfaces.ObjectType{
		objectType("Order", "Order", resource("res_order", "orders")),
	}}
	ot, err := testSchema(t, src).ResolveLabel("Order")
	if err != nil {
		t.Fatalf("ResolveLabel: %v", err)
	}
	if ot.OTID != "Order" {
		t.Fatalf("got %q, want %q", ot.OTID, "Order")
	}
}

func TestResolveRelationType(t *testing.T) {
	src := &fakeSchemaSource{relationTypes: []*interfaces.RelationType{
		relationType("rt_placed", "PLACED"),
		relationType("rt_shadow", "rt_placed"),
	}}
	schema := testSchema(t, src)

	if rt, err := schema.ResolveRelationType("PLACED"); err != nil || rt.RTID != "rt_placed" {
		t.Fatalf("ResolveRelationType by name = %v, %v", rt, err)
	}
	if _, err := schema.ResolveRelationType("rt_placed"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("want ambiguity error, got %v", err)
	}
	if _, err := schema.ResolveRelationType("SHIPPED"); err == nil ||
		!strings.Contains(err.Error(), "unknown relationship type") {
		t.Fatalf("want unknown relationship type error, got %v", err)
	}
}

func TestColumn(t *testing.T) {
	// amount is renamed on the way down: the property is amount, the column is
	// f_total. Emitting the property name here would query a column that does
	// not exist.
	ot := objectType("ot_order", "Order", resource("res_order", "orders"),
		dataProperty("amount", "f_total"),
		&interfaces.DataProperty{Name: "unmapped", Type: "string"},
	)
	ot.LogicProperties = []*interfaces.LogicProperty{{Name: "lifetime_value", Type: "float"}}

	schema := testSchema(t, &fakeSchemaSource{objectTypes: []*interfaces.ObjectType{ot}})
	bound, err := schema.ResolveLabel("Order")
	if err != nil {
		t.Fatalf("ResolveLabel: %v", err)
	}

	if column, err := schema.Column(bound, "amount"); err != nil || column != "f_total" {
		t.Fatalf("Column(amount) = %q, %v; want f_total", column, err)
	}
	if _, err := schema.Column(bound, "amount display"); err == nil ||
		!strings.Contains(err.Error(), "has no property") {
		t.Fatalf("display name must not resolve, got %v", err)
	}
	if _, err := schema.Column(bound, "lifetime_value"); err == nil ||
		!strings.Contains(err.Error(), "logic property") {
		t.Fatalf("want logic property rejection, got %v", err)
	}
	if _, err := schema.Column(bound, "unmapped"); err == nil ||
		!strings.Contains(err.Error(), "no mapped column") {
		t.Fatalf("want unmapped column error, got %v", err)
	}
	if _, err := schema.Column(bound, "missing"); err == nil ||
		!strings.Contains(err.Error(), "has no property") {
		t.Fatalf("want unknown property error, got %v", err)
	}
}

func TestResourceID(t *testing.T) {
	unbound := objectType("ot_unbound", "Unbound", nil)
	// A binding left behind by a rebuilt catalog: the name survives, the id
	// does not.
	stale := objectType("ot_stale", "Stale", &interfaces.ResourceInfo{Name: "orders"})
	bound := objectType("ot_order", "Order", resource("res_order", "orders"))

	schema := testSchema(t, &fakeSchemaSource{
		objectTypes: []*interfaces.ObjectType{unbound, stale, bound},
	})

	if id, err := schema.ResourceID(bound); err != nil || id != "res_order" {
		t.Fatalf("ResourceID = %q, %v; want res_order", id, err)
	}
	if _, err := schema.ResourceID(unbound); err == nil ||
		!strings.Contains(err.Error(), "no data source bound") {
		t.Fatalf("want unbound error, got %v", err)
	}
	if _, err := schema.ResourceID(stale); err == nil ||
		!strings.Contains(err.Error(), "stale data source binding") {
		t.Fatalf("want stale binding error, got %v", err)
	}
}
