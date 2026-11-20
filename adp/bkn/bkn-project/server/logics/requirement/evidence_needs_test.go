// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A knowledge base keeps source files and blocks to locate text, and relates every record read to
// them. They are not business: no path runs through them and no question needs them.
func Test_EvidenceIsNoBusinessNeed(t *testing.T) {
	Convey("Test evidence objects in needs and paths\\n", t, func() {
		ot := func(id, name, role string) *interfaces.DraftObjectType {
			o := &interfaces.DraftObjectType{ID: id, Name: name, Adopted: true}
			if role != "" {
				o.Document = &interfaces.DraftDocumentModel{Role: role}
			}
			return o
		}
		rel := func(id, source, target, mode string) *interfaces.DraftRelationType {
			r := &interfaces.DraftRelationType{ID: id, Source: source, Target: target, Adopted: true}
			if mode != "" {
				r.DocumentMapping = &interfaces.DraftDocumentMapping{Mode: mode}
			}
			return r
		}
		draft := &interfaces.Draft{Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{ot("contract", "Contract", "entity"), ot("line", "Contract line", "in_document"),
				ot("file", "Source document", "evidence_document")},
			RelationTypes: []*interfaces.DraftRelationType{rel("contract_file", "contract", "file", "evidence"),
				rel("line_file", "line", "file", "evidence")},
		}}
		objectRef := func(id string) interfaces.RequirementItemRef {
			return interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: id}
		}

		Convey("no path between two records runs through the file they were read from\\n", func() {
			q := &interfaces.RequirementQuestion{Text: "Lines of a contract?", NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS,
				Needs: []interfaces.RequirementItemRef{objectRef("contract"), objectRef("line")}}
			path, unconnected := questionPath(q, draft)
			So(path, ShouldBeEmpty)
			So(unconnected, ShouldHaveLength, 1)
		})
		Convey("a need of evidence is dropped, and none is named from the text\\n", func() {
			q := &interfaces.RequirementQuestion{Text: "Which Source document has the Contract?", NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS,
				Needs: []interfaces.RequirementItemRef{objectRef("contract"), objectRef("file"),
					{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "contract_file"}}}
			So(businessNeeds(q, draft), ShouldResemble, []interfaces.RequirementItemRef{objectRef("contract")})
			So(objectTypesNamed(draft, q.Text), ShouldHaveLength, 1)
		})
		Convey("a question drafted with no needs needs the object types its text names\\n", func() {
			q := &interfaces.RequirementQuestion{Text: "Which Contract line costs most?", NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS}
			So(businessNeeds(q, draft), ShouldResemble, []interfaces.RequirementItemRef{objectRef("line")})
			// One a person said needs nothing keeps needing nothing.
			q.NeedsSource = interfaces.REQUIREMENT_NEEDS_FROM_PERSON
			So(businessNeeds(q, draft), ShouldBeEmpty)
		})
	})
}
