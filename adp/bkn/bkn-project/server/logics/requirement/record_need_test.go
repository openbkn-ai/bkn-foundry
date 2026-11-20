// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A question that names one record by values of several object types, as "the line of material
// {code} in contract {number}", needs the object type of that record: the one that holds them all.
func Test_RecordNeed(t *testing.T) {
	Convey("Test the record a question names by values of several object types\n", t, func() {
		prop := func(name string) *interfaces.DraftProperty {
			return &interfaces.DraftProperty{Name: name, MappedField: name}
		}
		ref := func(id string) interfaces.RequirementItemRef {
			return interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: id}
		}
		linking := func(id, source, target, mention, identity string) *interfaces.DraftRelationType {
			return &interfaces.DraftRelationType{ID: id, Source: source, Target: target, Adopted: true,
				DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: mention, IdentityProperty: identity}}
		}
		draft := &interfaces.Draft{Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				{ID: "material", Adopted: true, Properties: []*interfaces.DraftProperty{prop("code")}},
				{ID: "contract", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "entity"}, Properties: []*interfaces.DraftProperty{prop("number")}},
				{ID: "line", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "in_document"},
					Properties: []*interfaces.DraftProperty{prop("material_code"), prop("contract_number")}},
			},
			RelationTypes: []*interfaces.DraftRelationType{
				linking("line_material", "line", "material", "material_code", "code"),
				linking("line_contract", "line", "contract", "contract_number", "number"),
			},
		}}
		spec := &interfaces.RequirementSpec{Concepts: []*interfaces.RequirementConcept{
			{ID: "c1", MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "material"}},
			{ID: "c2", MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "contract"}},
		}}
		inputs := []interfaces.RequirementInput{
			{Name: "code", ConceptID: "c1", Property: "code", Role: interfaces.REQUIREMENT_INPUT_RECORD},
			{Name: "number", ConceptID: "c2", Property: "number", Role: interfaces.REQUIREMENT_INPUT_RECORD},
		}
		q := &interfaces.RequirementQuestion{Text: "Line of material {code} in contract {number}?", NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS,
			Needs: []interfaces.RequirementItemRef{ref("material"), ref("contract")}, Inputs: &inputs}

		Convey("the object type that holds every value is needed, and the path reaches it\n", func() {
			needs := businessNeeds(spec, q, draft)
			So(needs, ShouldResemble, []interfaces.RequirementItemRef{ref("material"), ref("contract"), ref("line")})
			asked := *q
			asked.Needs = needs
			path, unconnected := questionPath(&asked, draft)
			So(unconnected, ShouldBeEmpty)
			So(path, ShouldNotBeEmpty)
		})
		Convey("values of one object type name a record of it: nothing is added\n", func() {
			one := []interfaces.RequirementInput{inputs[0]}
			single := *q
			single.Inputs = &one
			So(businessNeeds(spec, &single, draft), ShouldResemble, []interfaces.RequirementItemRef{ref("material"), ref("contract")})
		})
		Convey("two object types that could hold them: which is meant is not the model's to say\n", func() {
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, &interfaces.DraftObjectType{ID: "return", Adopted: true,
				Properties: []*interfaces.DraftProperty{prop("material_code"), prop("contract_number")}})
			draft.Items.RelationTypes = append(draft.Items.RelationTypes,
				linking("return_material", "return", "material", "material_code", "code"),
				linking("return_contract", "return", "contract", "contract_number", "number"))
			So(businessNeeds(spec, q, draft), ShouldResemble, []interfaces.RequirementItemRef{ref("material"), ref("contract")})
		})
		Convey("what a person said a question needs is kept\n", func() {
			person := *q
			person.NeedsSource = interfaces.REQUIREMENT_NEEDS_FROM_PERSON
			So(businessNeeds(spec, &person, draft), ShouldResemble, []interfaces.RequirementItemRef{ref("material"), ref("contract")})
		})
	})
}
