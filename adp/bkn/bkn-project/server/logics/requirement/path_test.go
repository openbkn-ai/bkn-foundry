// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"slices"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_QuestionPath(t *testing.T) {
	Convey("Test the path a question rests on in the draft\n", t, func() {
		ot := func(id string) interfaces.RequirementItemRef {
			return interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: id}
		}
		rel := func(id string) interfaces.RequirementItemRef {
			return interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: id}
		}
		relation := func(id, source, target string) *interfaces.DraftRelationType {
			return &interfaces.DraftRelationType{ID: id, Source: source, Target: target, Adopted: true}
		}
		// Materials and suppliers, joined only through purchase order lines; inventory reaches materials.
		draft := func() *interfaces.Draft {
			return &interfaces.Draft{Items: interfaces.DraftItems{
				ObjectTypes: []*interfaces.DraftObjectType{{ID: "material"}, {ID: "supplier"}, {ID: "po_line"}, {ID: "inventory"},
					{ID: "customer"}, {ID: "plant"}},
				RelationTypes: []*interfaces.DraftRelationType{
					relation("inv_mat", "inventory", "material"),
					relation("po_sup", "po_line", "supplier"),
					relation("po_mat", "po_line", "material"),
				},
			}}
		}
		question := func(needs ...interfaces.RequirementItemRef) *interfaces.RequirementQuestion {
			return &interfaces.RequirementQuestion{ID: "q3", Needs: needs, NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS}
		}

		Convey("Two object types joined only through a third rest on it and both relations\n", func() {
			path, unconnected := questionPath(question(ot("material"), ot("supplier")), draft())

			So(path, ShouldResemble, []interfaces.RequirementItemRef{rel("po_mat"), ot("po_line"), rel("po_sup")})
			So(unconnected, ShouldBeEmpty)
		})

		Convey("A relation between them is the shortest path; one the question names joins without one\n", func() {
			d := draft()
			d.Items.RelationTypes = append(d.Items.RelationTypes, relation("mat_sup", "material", "supplier"))
			path, _ := questionPath(question(ot("material"), ot("supplier")), d)
			So(path, ShouldResemble, []interfaces.RequirementItemRef{rel("mat_sup")})

			path, unconnected := questionPath(question(ot("material"), ot("supplier"), rel("po_sup")), d)
			So(path, ShouldResemble, []interfaces.RequirementItemRef{rel("mat_sup")})
			So(unconnected, ShouldBeEmpty)
			// The named relation joins its ends: nothing more is derived.
			named := draft()
			named.Items.RelationTypes = append(named.Items.RelationTypes, relation("mat_sup", "material", "supplier"))
			path, _ = questionPath(question(ot("material"), ot("supplier"), rel("mat_sup")), named)
			So(path, ShouldBeEmpty)
		})

		Convey("Types joined through one another are one group: a star reports no gap\n", func() {
			d := draft()
			d.Items.RelationTypes = append(d.Items.RelationTypes,
				relation("mat_plant", "material", "plant"))
			// supplier and plant meet only through material.
			path, unconnected := questionPath(question(ot("supplier"), ot("plant"), ot("material")), d)

			So(unconnected, ShouldBeEmpty)
			So(path, ShouldContain, rel("mat_plant"))
		})

		Convey("Types no adopted path of two relations joins are named, once per group\n", func() {
			d := draft()
			path, unconnected := questionPath(question(ot("material"), ot("customer"), ot("supplier")), d)

			So(unconnected, ShouldResemble, []interfaces.ScenarioUnconnected{{From: "material", To: "customer"}})
			So(path, ShouldResemble, []interfaces.RequirementItemRef{rel("po_mat"), ot("po_line"), rel("po_sup")})

			d.Items.RelationTypes[1].Adopted = false
			_, unconnected = questionPath(question(ot("material"), ot("supplier")), d)
			So(unconnected, ShouldResemble, []interfaces.ScenarioUnconnected{{From: "material", To: "supplier"}})
		})

		Convey("Types already joined add no other path; a named relation joins its ends first\n", func() {
			d := draft()
			// material and supplier, each joined to plant; also joined to each other directly.
			d.Items.RelationTypes = []*interfaces.DraftRelationType{relation("mat_plant", "material", "plant"),
				relation("sup_plant", "supplier", "plant"), relation("mat_sup", "material", "supplier")}
			path, _ := questionPath(question(ot("material"), ot("plant"), ot("supplier")), d)
			So(path, ShouldResemble, []interfaces.RequirementItemRef{rel("mat_plant"), rel("mat_sup")})

			// Two relations join material and supplier; the question names the second.
			d.Items.RelationTypes = []*interfaces.DraftRelationType{relation("mat_sup_a", "material", "supplier"),
				relation("mat_sup_b", "material", "supplier")}
			path, _ = questionPath(question(ot("material"), ot("supplier"), rel("mat_sup_b")), d)
			So(path, ShouldBeEmpty)
		})

		Convey("A group apart is named once\n", func() {
			d := draft()
			d.Items.RelationTypes = append(d.Items.RelationTypes, relation("cust_plant", "customer", "plant"))
			_, unconnected := questionPath(question(ot("material"), ot("customer"), ot("plant")), d)
			So(unconnected, ShouldResemble, []interfaces.ScenarioUnconnected{{From: "material", To: "customer"}})
		})

		Convey("Three relations apart is beyond the bound\n", func() {
			d := draft()
			d.Items.RelationTypes = append(d.Items.RelationTypes, relation("cust_inv", "customer", "inventory"))
			// customer -> inventory -> material -> po_line -> supplier
			_, unconnected := questionPath(question(ot("customer"), ot("supplier")), d)
			So(unconnected, ShouldResemble, []interfaces.ScenarioUnconnected{{From: "customer", To: "supplier"}})
		})

		Convey("Needs a person set are theirs; one object type, or none drafted, needs no path\n", func() {
			person := question(ot("material"), ot("supplier"))
			person.NeedsSource = interfaces.REQUIREMENT_NEEDS_FROM_PERSON
			path, unconnected := questionPath(person, draft())
			So(path, ShouldBeEmpty)
			So(unconnected, ShouldBeEmpty)

			for _, q := range []*interfaces.RequirementQuestion{question(ot("material")), question(ot("material"), ot("gone")), question()} {
				path, unconnected = questionPath(q, draft())
				So(path, ShouldBeEmpty)
				So(unconnected, ShouldBeEmpty)
			}
			path, unconnected = questionPath(question(ot("material"), ot("supplier")), nil)
			So(path, ShouldBeEmpty)
			So(unconnected, ShouldBeEmpty)
		})

		Convey("The same draft gives the same path; reordering it changes only ties\n", func() {
			q := question(ot("material"), ot("supplier"))
			first, _ := questionPath(q, draft())
			for range 20 {
				again, _ := questionPath(q, draft())
				So(again, ShouldResemble, first)
			}
			d := draft()
			slices.Reverse(d.Items.RelationTypes)
			reordered, _ := questionPath(q, d)
			// One path only: the same items, found from the other relation first.
			So(reordered, ShouldResemble, []interfaces.RequirementItemRef{rel("po_mat"), ot("po_line"), rel("po_sup")})

			// Two lines between them tie: the draft's order picks.
			tie := draft()
			tie.Items.ObjectTypes = append(tie.Items.ObjectTypes, &interfaces.DraftObjectType{ID: "contract"})
			tie.Items.RelationTypes = append(tie.Items.RelationTypes,
				relation("con_mat", "contract", "material"), relation("con_sup", "contract", "supplier"))
			path, _ := questionPath(q, tie)
			So(path, ShouldResemble, []interfaces.RequirementItemRef{rel("po_mat"), ot("po_line"), rel("po_sup")})
			slices.Reverse(tie.Items.RelationTypes)
			path, _ = questionPath(q, tie)
			So(path, ShouldResemble, []interfaces.RequirementItemRef{rel("con_mat"), ot("contract"), rel("con_sup")})
		})
	})
}
