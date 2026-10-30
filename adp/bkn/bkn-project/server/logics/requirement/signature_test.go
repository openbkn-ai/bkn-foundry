// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_OntologySignature(t *testing.T) {
	Convey("Test the ontology signature of a question's drafted items\n", t, func() {
		orderType := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}
		relation := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "sales_order_customer"}
		q := &interfaces.RequirementQuestion{ID: "q1", Needs: []interfaces.RequirementItemRef{orderType, customerRef, relation}}
		draft := func() *interfaces.Draft {
			d := guidanceDraft()
			d.Items.ObjectTypes[0].Adopted, d.Items.ObjectTypes[1].Adopted = true, true
			d.Items.ObjectTypes[0].PrimaryKeys = []string{"order_id"}
			d.Items.ObjectTypes[0].Properties = []*interfaces.DraftProperty{
				{Name: "order_id", DisplayName: "Order", Type: "string", MappedField: "id", Description: "The order"},
				{Name: "customer_ref", Type: "string", MappedField: "cust"},
			}
			d.Items.RelationTypes[0].Adopted = true
			d.Items.RelationTypes[0].Mapping = []interfaces.DraftFieldMapping{{SourceProperty: "customer_ref", TargetProperty: "customer_id"}}
			return d
		}
		base := ontologySignature(draft(), q)

		Convey("The same items sign the same, in any order, whatever people call them\n", func() {
			So(base, ShouldHaveLength, 64)
			So(ontologySignature(draft(), q), ShouldEqual, base)

			d := draft()
			d.Items.ObjectTypes[0].Properties[0], d.Items.ObjectTypes[0].Properties[1] = d.Items.ObjectTypes[0].Properties[1], d.Items.ObjectTypes[0].Properties[0]
			d.Items.ObjectTypes[0], d.Items.ObjectTypes[1] = d.Items.ObjectTypes[1], d.Items.ObjectTypes[0]
			So(ontologySignature(d, q), ShouldEqual, base)

			d = draft()
			d.Items.ObjectTypes[0].Name, d.Items.RelationTypes[0].Name = "Purchase order", "Ordered by"
			d.Items.ObjectTypes[0].Description = "Renamed"
			d.Items.ObjectTypes[0].Properties[0].DisplayName, d.Items.ObjectTypes[0].Properties[0].Description = "Order number", "x"
			So(ontologySignature(d, q), ShouldEqual, base)
		})

		Convey("What the tests rely on changes it\n", func() {
			for _, change := range []func(d *interfaces.Draft){
				func(d *interfaces.Draft) { d.Items.ObjectTypes[0].Adopted = false },
				func(d *interfaces.Draft) { d.Items.ObjectTypes[0].PrimaryKeys = []string{"customer_ref"} },
				// The same table in another catalog is other data.
				func(d *interfaces.Draft) { d.Items.ObjectTypes[0].Table.ResourceID = "r-other" },
				func(d *interfaces.Draft) { d.Items.ObjectTypes[0].Properties[0].Name = "order_no" },
				func(d *interfaces.Draft) { d.Items.ObjectTypes[0].Properties[0].Type = "integer" },
				func(d *interfaces.Draft) { d.Items.ObjectTypes[0].Properties[0].MappedField = "order_no" },
				func(d *interfaces.Draft) { d.Items.ObjectTypes[0].Properties = d.Items.ObjectTypes[0].Properties[:1] },
				func(d *interfaces.Draft) { d.Items.RelationTypes[0].Adopted = false },
				func(d *interfaces.Draft) { d.Items.RelationTypes[0].Target = "sales_order" },
				func(d *interfaces.Draft) { d.Items.RelationTypes[0].Mapping[0].SourceProperty = "order_id" },
				func(d *interfaces.Draft) {
					d.Items.RelationTypes[0].LinkTable = &interfaces.DraftTable{ResourceID: "r-link"}
				},
				// A need the draft no longer has.
				func(d *interfaces.Draft) { d.Items.RelationTypes = nil },
			} {
				d := draft()
				change(d)
				So(ontologySignature(d, q), ShouldNotEqual, base)
			}
		})

		Convey("Only the items the question needs are signed; without needs, the adopted ones\n", func() {
			d := draft()
			d.Items.ObjectTypes = append(d.Items.ObjectTypes, &interfaces.DraftObjectType{ID: "supplier", Adopted: true})
			So(ontologySignature(d, q), ShouldEqual, base)

			none := &interfaces.RequirementQuestion{ID: "q2"}
			all := ontologySignature(d, none)
			d.Items.ObjectTypes[2].Adopted = false
			unadopted := ontologySignature(d, none)
			So(unadopted, ShouldNotEqual, all)
			// Another item not adopted is not signed.
			d.Items.ObjectTypes = append(d.Items.ObjectTypes, &interfaces.DraftObjectType{ID: "other"})
			So(ontologySignature(d, none), ShouldEqual, unadopted)
		})

		Convey("A need the draft does not have is signed as missing\n", func() {
			ghost := *q
			ghost.Needs = append(append([]interfaces.RequirementItemRef{}, q.Needs...),
				interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "ghost"})
			So(ontologySignature(draft(), &ghost), ShouldNotEqual, base)
		})

		Convey("The path that joins what the question names is signed too\n", func() {
			pathDraft := func() *interfaces.Draft {
				return &interfaces.Draft{Items: interfaces.DraftItems{
					ObjectTypes: []*interfaces.DraftObjectType{{ID: "material", Adopted: true}, {ID: "supplier", Adopted: true},
						{ID: "po_line", Adopted: true}},
					RelationTypes: []*interfaces.DraftRelationType{
						{ID: "po_mat", Source: "po_line", Target: "material", Adopted: true,
							Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "m", TargetProperty: "code"}}},
						{ID: "po_sup", Source: "po_line", Target: "supplier", Adopted: true,
							Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "s", TargetProperty: "code"}}},
					},
				}}
			}
			two := &interfaces.RequirementQuestion{ID: "q3", NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS,
				Needs: []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "material"},
					{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "supplier"}}}
			joined := ontologySignature(pathDraft(), two)
			for _, change := range []func(d *interfaces.Draft){
				func(d *interfaces.Draft) { d.Items.RelationTypes[1].Adopted = false },
				func(d *interfaces.Draft) { d.Items.RelationTypes[0].Mapping[0].SourceProperty = "other" },
				func(d *interfaces.Draft) { d.Items.ObjectTypes[2].PrimaryKeys = []string{"id"} },
			} {
				d := pathDraft()
				change(d)
				So(ontologySignature(d, two), ShouldNotEqual, joined)
			}
		})

		Convey("Without a draft there is nothing to sign\n", func() {
			So(ontologySignature(nil, q), ShouldEqual, "")
		})
	})
}
