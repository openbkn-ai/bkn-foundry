// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// knowledgeHooks stands for an edition that reads knowledge: its own are the concepts bound to a
// dataset, a kinetic one is a rule, one left out is none. The real rules are the edition's.
type knowledgeHooks struct{ conceptHooks }

func (knowledgeHooks) Knowledge(concepts []interfaces.DraftConcept) interfaces.RequirementKnowledge {
	knowledge := interfaces.RequirementKnowledge{}
	for _, c := range concepts {
		if c.Binding == nil || c.Binding.Kind != interfaces.CONCEPT_BINDING_DATASET || c.Verdict == interfaces.CONCEPT_VERDICT_NONE {
			continue
		}
		item := &interfaces.RequirementKnowledgeItem{Name: c.Name, Kind: c.Verdict}
		if c.Verdict == interfaces.CONCEPT_VERDICT_KINETIC {
			knowledge.Rules = append(knowledge.Rules, item)
		} else {
			knowledge.Concepts = append(knowledge.Concepts, item)
		}
	}
	return knowledge
}

func Test_Knowledge(t *testing.T) {
	Convey("Test the knowledge the requirement passes read\n", t, func() {
		ctx := context.Background()
		draft := documentConceptDraft()
		draft.Items.DocumentConcepts = append(draft.Items.DocumentConcepts, &interfaces.DraftDocumentConcept{
			ID: "c3", Name: "Invoicing terms", Description: "Invoice each delivered batch within five days",
			Verdict: interfaces.CONCEPT_VERDICT_KINETIC, Citations: []interfaces.DraftSourceCitation{{FileID: "contract"}}})

		Convey("Without an edition that reads it there is none\n", func() {
			defer projecthooks.SetForTest(projecthooks.Hooks{Drafts: conceptHooks{}})()
			got := Knowledge(ctx, draft)
			So(got.Concepts, ShouldBeEmpty)
			So(got.Rules, ShouldBeEmpty)
			So(got.Concepts, ShouldNotBeNil)
		})

		Convey("The edition reads the concepts as judged: a kinetic one is a rule, one left out is none\n", func() {
			defer projecthooks.SetForTest(projecthooks.Hooks{Drafts: knowledgeHooks{}})()
			draft.Items.Decisions = append(draft.Items.Decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_CONCEPTS,
				ItemID: "document_concepts/c2", Action: interfaces.DRAFT_DECISION_JUDGE, After: interfaces.CONCEPT_VERDICT_NONE,
				By: reviewer, Time: 7})
			got := Knowledge(ctx, draft)
			So(got.Concepts, ShouldResemble, []*interfaces.RequirementKnowledgeItem{
				{Name: "Discount rate", Kind: interfaces.CONCEPT_VERDICT_PROPERTY}})
			So(got.Rules, ShouldResemble, []*interfaces.RequirementKnowledgeItem{
				{Name: "Invoicing terms", Kind: interfaces.CONCEPT_VERDICT_KINETIC}})
		})

		Convey("A draft not ready holds none\n", func() {
			defer projecthooks.SetForTest(projecthooks.Hooks{Drafts: knowledgeHooks{}})()
			draft.Status = interfaces.DRAFT_STATUS_DRAFTING
			So(Knowledge(ctx, draft).Rules, ShouldBeEmpty)
			So(Knowledge(ctx, nil).Concepts, ShouldBeEmpty)
		})
	})
}
