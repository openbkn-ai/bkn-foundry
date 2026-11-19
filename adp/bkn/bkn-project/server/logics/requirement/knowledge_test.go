// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// withKnowledge has every draft read as holding knowledge, as an edition reads it; it returns what
// restores the reader.
func withKnowledge(knowledge interfaces.RequirementKnowledge) (restore func()) {
	previous := readKnowledge
	readKnowledge = func(context.Context, *interfaces.Draft) interfaces.RequirementKnowledge { return knowledge }
	return func() { readKnowledge = previous }
}

// contractKnowledge is what a procurement knowledge base holds: two concepts, one in the model, and
// a rule its contracts state.
func contractKnowledge() interfaces.RequirementKnowledge {
	return interfaces.RequirementKnowledge{Files: 2,
		Concepts: []*interfaces.RequirementKnowledgeItem{
			{Name: "Purchase invoice", Description: "An invoice a supplier issues", Kind: interfaces.CONCEPT_VERDICT_OBJECT, Adopted: true},
			{Name: "Payment terms", Kind: interfaces.CONCEPT_VERDICT_PROPERTY},
		},
		Rules: []*interfaces.RequirementKnowledgeItem{
			{Name: "Price change check", Description: "A price change needs a signed amendment", Kind: interfaces.CONCEPT_VERDICT_KINETIC},
		}}
}

// messageBody is the JSON of a pass message, after its header lines.
func messageBody(message string) []byte {
	return []byte(message[strings.Index(message, "{"):])
}

func Test_Knowledge(t *testing.T) {
	Convey("Test the knowledge base in the requirement passes\n", t, func() {
		Convey("None is left out of the messages\n", func() {
			So(messageKnowledgeOf(interfaces.RequirementKnowledge{}), ShouldBeNil)
			message, err := buildKineticMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, kineticDraft(), emptySpec(), nil, "",
				interfaces.RequirementKnowledge{})
			So(err, ShouldBeNil)
			So(message, ShouldNotContainSubstring, `"knowledge"`)
		})

		want := &messageKnowledge{
			Concepts: []messageKnowledgeItem{
				{Name: "Purchase invoice", Kind: interfaces.CONCEPT_VERDICT_OBJECT, Definition: "An invoice a supplier issues", InModel: true},
				{Name: "Payment terms", Kind: interfaces.CONCEPT_VERDICT_PROPERTY},
			},
			Rules: []messageKnowledgeItem{{Name: "Price change check", Definition: "A price change needs a signed amendment"}},
		}

		Convey("Kinetic analysis reads its concepts and rules by name\n", func() {
			message, err := buildKineticMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, kineticDraft(), emptySpec(), nil, "",
				contractKnowledge())
			So(err, ShouldBeNil)
			var msg kineticMessage
			So(json.Unmarshal(messageBody(message), &msg), ShouldBeNil)
			So(msg.Knowledge, ShouldResemble, want)
		})

		Convey("Scenario drafting reads them too\n", func() {
			message, _, _, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, staffOntology(), emptySpec(),
				emptySpec(), nil, "", contractKnowledge())
			So(err, ShouldBeNil)
			var msg draftMessage
			So(json.Unmarshal(messageBody(message), &msg), ShouldBeNil)
			So(msg.Knowledge, ShouldResemble, want)
		})

		Convey("And Ask AI on either page\n", func() {
			for _, page := range []string{interfaces.REQUIREMENT_PAGE_KINETIC, interfaces.REQUIREMENT_PAGE_SCENARIOS} {
				message, err := buildAskMessage(page, "What else?", nil, nil,
					&askInputs{spec: emptySpec(), knowledge: contractKnowledge()})
				So(err, ShouldBeNil)
				var msg askMessage
				So(json.Unmarshal(messageBody(message), &msg), ShouldBeNil)
				So(msg.Knowledge, ShouldResemble, want)
			}
		})
	})

	Convey("Test the knowledge as a document runs cite\n", t, func() {
		maintenance := interfaces.RequirementKnowledge{Files: 1,
			Concepts: []*interfaces.RequirementKnowledgeItem{{Name: "设备", Description: "场所里的维护对象", Kind: interfaces.CONCEPT_VERDICT_OBJECT, Adopted: true}},
			Rules: []*interfaces.RequirementKnowledgeItem{
				{Name: "维护建议查询", Description: "动力需求 DR03：提供只读查询设备维护建议的接口", Kind: interfaces.CONCEPT_VERDICT_KINETIC},
			}}

		Convey("None makes no document\n", func() {
			So(knowledgeDocument("p1", interfaces.NAMING_LANGUAGE_ZH_CN, interfaces.RequirementKnowledge{}), ShouldBeNil)
		})

		Convey("A need resting on a rule cites the rule, numbers and all; an unrelated quote is still refused\n", func() {
			doc := knowledgeDocument("p1", interfaces.NAMING_LANGUAGE_ZH_CN, maintenance)
			So(doc.ID, ShouldEqual, interfaces.REQUIREMENT_KNOWLEDGE_DOCUMENT_ID)
			So(doc.Content, ShouldContainSubstring, "动力需求 DR03：提供只读查询设备维护建议的接口")
			So(doc.Content, ShouldContainSubstring, "场所里的维护对象")
			network := &interfaces.RequirementDocument{ID: interfaces.REQUIREMENT_NETWORK_DOCUMENT_ID, Content: "2. **查找关系定义**",
				Blocks: segment("2. **查找关系定义**", interfaces.REQUIREMENT_FORMAT_MARKDOWN)}
			spec := emptySpec()
			var rejected []string
			addLogicNeeds(spec, []agentLogicNeed{
				{Kind: interfaces.LOGIC_NEED_API, Name: "查询设备维护建议接口", Definition: "动力需求 DR03 的只读查询接口",
					Citations: []agentCitation{cite(interfaces.REQUIREMENT_KNOWLEDGE_DOCUMENT_ID, "动力需求 DR03：提供只读查询设备维护建议的接口")}},
				{Kind: interfaces.LOGIC_NEED_API, Name: "生成设备维护建议", Definition: "动力需求 DR04 的生成接口",
					Citations: []agentCitation{cite(interfaces.REQUIREMENT_NETWORK_DOCUMENT_ID, "2. **查找关系定义**")}},
			}, interfaces.LOGIC_NEED_FROM_ANALYSIS, documentsByID([]*interfaces.RequirementDocument{network, doc}), kineticDraft(),
				map[string]string{}, func(kind, text, reason string) { rejected = append(rejected, text+" "+reason) })
			So(len(spec.LogicNeeds), ShouldEqual, 1)
			So(spec.LogicNeeds[0].Name, ShouldEqual, "查询设备维护建议接口")
			So(rejected, ShouldResemble, []string{"生成设备维护建议 " + rejectedBackgroundNotBasis})
		})
	})

	Convey("Test reading the knowledge of a project\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, da: da}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil)

		Convey("It is what the edition reads from the draft\n", func() {
			defer withKnowledge(contractKnowledge())()
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(kineticDraft(), true, nil)
			got, err := svc.GetKnowledge(ctx, token, "p1")
			So(err, ShouldBeNil)
			So(*got, ShouldResemble, contractKnowledge())
		})

		Convey("Without a draft there is none\n", func() {
			var read *interfaces.Draft
			defer func(previous func(context.Context, *interfaces.Draft) interfaces.RequirementKnowledge) {
				readKnowledge = previous
			}(readKnowledge)
			readKnowledge = func(_ context.Context, d *interfaces.Draft) interfaces.RequirementKnowledge {
				read = d
				return interfaces.RequirementKnowledge{}
			}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&interfaces.Draft{}, false, nil)
			_, err := svc.GetKnowledge(ctx, token, "p1")
			So(err, ShouldBeNil)
			So(read, ShouldBeNil)
		})

		Convey("The knowledge is read as a document a citation opens\n", func() {
			defer withKnowledge(contractKnowledge())()
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(kineticDraft(), true, nil)
			doc, err := svc.GetDocument(ctx, token, "p1", interfaces.REQUIREMENT_KNOWLEDGE_DOCUMENT_ID)
			So(err, ShouldBeNil)
			So(doc.Content, ShouldContainSubstring, "Price change check: A price change needs a signed amendment")
			So(doc.Name, ShouldEqual, "Rules and concepts of the knowledge base")
		})

		Convey("A draft that cannot be read is an error\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, errors.New("db down"))
			_, err := svc.GetKnowledge(ctx, token, "p1")
			So(err, ShouldNotBeNil)
		})
	})
}
