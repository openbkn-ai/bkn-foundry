// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// understoodDraft has an object type drafted from erp_material before semantic understanding
// named the table, and one from erp_vendor that understanding never reached.
func understoodDraft() *interfaces.Draft {
	draft := candidateDraft()
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes,
		&interfaces.DraftObjectType{ID: "erp_material", Name: "erp_material", Adopted: true,
			Table: interfaces.DraftTable{ResourceID: "r_material", Name: "erp_material", CatalogID: "c1"}},
		&interfaces.DraftObjectType{ID: "erp_vendor", Name: "erp_vendor", Adopted: true,
			Table: interfaces.DraftTable{ResourceID: "r_vendor", Name: "erp_vendor", CatalogID: "c1"}})
	return draft
}

func understoodTables() map[string]*interfaces.VegaTable {
	return map[string]*interfaces.VegaTable{
		"r_material": {ID: "r_material", Name: "物料", SourceName: "erp_material", SourceSchema: "erp",
			Description: "记录物料主数据，每个物料一行"},
		"r_vendor": {ID: "r_vendor", Name: "erp.erp_vendor", SourceName: "erp_vendor", SourceSchema: "erp"},
	}
}

func Test_UnderstoodConcepts(t *testing.T) {
	Convey("Test naming table concepts by semantic understanding\n", t, func() {
		ctx := context.Background()
		draft := understoodDraft()
		concepts := conceptsWith(ctx, "p1", draft, nil, false, understoodTables())

		Convey("A table understood names its concept, the table's own name an alias and the source\n", func() {
			c := conceptByKey(concepts, "object_types/erp_material")
			So(c.Name, ShouldEqual, "物料")
			So(c.Description, ShouldEqual, "记录物料主数据，每个物料一行")
			So(c.Aliases, ShouldResemble, []string{"erp_material"})
			So(c.Sources[0].Name, ShouldEqual, "erp_material")
			So(c.SuggestedBecause, ShouldEqual, interfaces.CONCEPT_BECAUSE_UNDERSTOOD)
			So(c.AI, ShouldBeTrue)
		})

		Convey("A table not understood keeps its name and is counted\n", func() {
			c := conceptByKey(concepts, "object_types/erp_vendor")
			So(c.Name, ShouldEqual, "erp_vendor")
			So(c.SuggestedBecause, ShouldEqual, interfaces.CONCEPT_BECAUSE_TABLE)
			So(c.AI, ShouldBeFalse)
			So(concepts.NotUnderstood, ShouldEqual, 1)
		})

		Convey("A table discovered by vega 0.2.1 or later keeps its own name until understood\n", func() {
			tables := understoodTables()
			tables["r_vendor"].Name = "erp_vendor"
			got := conceptsWith(ctx, "p1", draft, nil, false, tables)
			So(conceptByKey(got, "object_types/erp_vendor").AI, ShouldBeFalse)
			So(got.NotUnderstood, ShouldEqual, 1)
		})

		Convey("A name a person gave the object type stands\n", func() {
			draft.Items.ObjectTypes[4].Name = "Material"
			c := conceptByKey(conceptsWith(ctx, "p1", draft, nil, false, understoodTables()), "object_types/erp_material")
			So(c.Name, ShouldEqual, "Material")
			So(c.Aliases, ShouldResemble, []string{"erp_material"})
		})

		Convey("Unread tables leave the concepts as drafting named them\n", func() {
			c := conceptByKey(conceptsOf(ctx, "p1", draft, nil, false), "object_types/erp_material")
			So(c.Name, ShouldEqual, "erp_material")
			So(conceptsOf(ctx, "p1", draft, nil, false).NotUnderstood, ShouldEqual, 0)
		})
	})
}

func Test_CheckConceptRevision(t *testing.T) {
	Convey("Test checking Ask AI's revisions of the concepts\n", t, func() {
		ctx := context.Background()
		draft := understoodDraft()
		draft.Items.DocumentConcepts = []*interfaces.DraftDocumentConcept{{ID: "c1", Name: "Ward", Verdict: interfaces.CONCEPT_VERDICT_PROPERTY}}
		concepts := conceptsWith(ctx, "p1", draft, conceptRequirement(), true, understoodTables())
		check := func(out conceptRevisionOutput) (*interfaces.DraftConceptRevision, string) {
			return checkConceptRevision(ctx, &draft.Items, concepts, out)
		}

		Convey("Renaming and defining keeps what is not given\n", func() {
			got, reason := check(conceptRevisionOutput{Field: "define", Key: "object_types/erp_vendor", Name: " 供应商 "})
			So(reason, ShouldBeEmpty)
			So(got.Name, ShouldEqual, "供应商")
			_, reason = check(conceptRevisionOutput{Field: "define", Key: "object_types/erp_material", Name: "物料"})
			So(reason, ShouldEqual, askDroppedUnchanged)
			_, reason = check(conceptRevisionOutput{Field: "define", Key: "object_types/nothing", Name: "x"})
			So(reason, ShouldEqual, askDroppedUnknownConcept)
		})

		Convey("A verdict fits the item\n", func() {
			_, reason := check(conceptRevisionOutput{Field: "verdict", Key: "object_types/erp_vendor", Verdict: "relation"})
			So(reason, ShouldEqual, askDroppedVerdictNotFit)
			got, _ := check(conceptRevisionOutput{Field: "verdict", Key: "object_types/erp_vendor", Verdict: "event"})
			So(got.Verdict, ShouldEqual, "event")
		})

		Convey("A requirement concept no item is only merges\n", func() {
			_, reason := check(conceptRevisionOutput{Field: "define", Key: "requirement_concepts/k3", Name: "OTD"})
			So(reason, ShouldEqual, askDroppedInSpecification)
			got, _ := check(conceptRevisionOutput{Field: "merge", Key: "requirement_concepts/k3", Into: "object_types/items"})
			So(got.Into, ShouldEqual, "object_types/items")
		})

		Convey("Merging into itself or nothing is left out\n", func() {
			_, reason := check(conceptRevisionOutput{Field: "merge", Key: "object_types/items", Into: "object_types/items"})
			So(reason, ShouldEqual, askDroppedInvalid)
		})

		Convey("A split has two parts at least, the first fitting the concept\n", func() {
			_, reason := check(conceptRevisionOutput{Field: "split", Key: "object_types/items",
				Parts: []interfaces.DraftConceptPart{{Name: "Item", Verdict: "object"}}})
			So(reason, ShouldEqual, askDroppedInvalid)
			_, reason = check(conceptRevisionOutput{Field: "split", Key: "object_types/items",
				Parts: []interfaces.DraftConceptPart{{Name: "Item", Verdict: "values"}, {Name: "Item line", Verdict: "object"}}})
			So(reason, ShouldEqual, askDroppedVerdictNotFit)
			got, _ := check(conceptRevisionOutput{Field: "split", Key: "object_types/items",
				Parts: []interfaces.DraftConceptPart{{Name: "Item", Verdict: "object"}, {Name: "Item line", Verdict: "object"}}})
			So(got.Parts, ShouldHaveLength, 2)
		})

		Convey("An added concept is new\n", func() {
			_, reason := check(conceptRevisionOutput{Field: "add", Name: "items"})
			So(reason, ShouldEqual, askDroppedRepeated)
			got, _ := check(conceptRevisionOutput{Field: "add", Name: "Purchase order line"})
			So(got.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_OBJECT)
		})

		Convey("Aliases are an object type's of a table\n", func() {
			got, reason := check(conceptRevisionOutput{Field: "aliases", Key: "object_types/erp_material", Aliases: []string{"原材料"}})
			So(reason, ShouldBeEmpty)
			So(got.Aliases, ShouldResemble, []string{"原材料"})
			_, reason = check(conceptRevisionOutput{Field: "aliases", Key: "document_concepts/c1", Aliases: []string{"x"}})
			So(reason, ShouldEqual, askDroppedNotEditable)
		})
	})
}

func Test_ConceptAskAndRevise(t *testing.T) {
	defer projecthooks.SetForTest(projecthooks.Hooks{Drafts: conceptHooks{}})()
	Convey("Test asking AI about the concepts and adopting its revisions\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		rqa := pmock.NewMockRequirementAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, rqa: rqa, va: va, ba: ba}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil).AnyTimes()
		rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(conceptRequirement(), true, nil).AnyTimes()
		draft := understoodDraft()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
		va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ interfaces.AccountInfo, ids []string) ([]*interfaces.VegaTable, error) {
				tables := []*interfaces.VegaTable{}
				for _, id := range ids {
					if table := understoodTables()[id]; table != nil {
						tables = append(tables, table)
					}
				}
				return tables, nil
			}).AnyTimes()
		write := func() {
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
		}
		revise := func(field string, value interfaces.DraftConceptRevision) (*interfaces.DraftConcepts, error) {
			return svc.ReviseConcept(ctx, token, "p1", interfaces.DraftConceptRevise{UpdateTime: draft.UpdateTime,
				Field: field, Value: value, Reason: "From Ask AI"}, caller)
		}

		Convey("Concepts are read with the tables as the caller sees them\n", func() {
			got, err := svc.GetConcepts(ctx, token, "p1", caller)
			So(err, ShouldBeNil)
			So(conceptByKey(got, "object_types/erp_material").Name, ShouldEqual, "物料")
		})

		Convey("Ask AI reads the concepts and gives the revisions that check, dropping the others\n", func() {
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, account interfaces.AccountInfo, req *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					var message map[string]any
					So(json.Unmarshal([]byte(req.Message), &message), ShouldBeNil)
					So(message["kind"], ShouldEqual, askKindConcepts)
					So(req.Message, ShouldContainSubstring, `"key":"object_types/erp_material"`)
					So(req.Message, ShouldContainSubstring, "table:erp_material")
					schema, _ := json.Marshal(req.ResponseFormat)
					So(string(schema), ShouldContainSubstring, `"split"`)
					return revised(
						`{"field":"define","key":"object_types/erp_vendor","name":"供应商","reason":"业务名称"}`,
						`{"field":"verdict","key":"object_types/erp_vendor","verdict":"relation","reason":"x"}`,
						`{"field":"add","name":"采购订单行","verdict":"object","reason":"订单行"}`,
					)(ctx, account, req, on)
				})
			answer, err := svc.AskConcepts(ctx, token, "p1", interfaces.DraftConceptAskRequest{Question: "按业务命名"}, caller, nil)
			So(err, ShouldBeNil)
			So(answer.Answer, ShouldEqual, "答复")
			So(answer.Revisions, ShouldHaveLength, 2)
			So(answer.Revisions[0].Value.(*interfaces.DraftConceptRevision).Name, ShouldEqual, "供应商")
			So(answer.Dropped, ShouldResemble, []interfaces.DraftAskDropped{{Field: "verdict", Reason: askDroppedVerdictNotFit}})
		})

		Convey("Adopting a definition renames the item, recorded as editing it\n", func() {
			write()
			got, err := revise("define", interfaces.DraftConceptRevision{Key: "object_types/erp_vendor", Name: "供应商"})
			So(err, ShouldBeNil)
			So(conceptByKey(got, "object_types/erp_vendor").Name, ShouldEqual, "供应商")
			last := draft.Items.Decisions[len(draft.Items.Decisions)-1]
			So(last.Action, ShouldEqual, interfaces.DRAFT_DECISION_RENAME)
		})

		Convey("An added concept, no item, is defined as a concept\n", func() {
			write()
			got, err := revise("add", interfaces.DraftConceptRevision{Name: "Ward", Verdict: "object"})
			So(err, ShouldBeNil)
			key := ""
			for _, c := range got.Concepts {
				if strings.HasPrefix(c.Key, addedConceptPrefix) {
					key = c.Key
				}
			}
			So(key, ShouldNotBeEmpty)
			write()
			got, err = revise("define", interfaces.DraftConceptRevision{Key: key, Name: "Hospital ward"})
			So(err, ShouldBeNil)
			c := conceptByKey(got, key)
			So(c.Name, ShouldEqual, "Hospital ward")
			So(c.AI, ShouldBeTrue)
			So(draft.Items.Decisions[len(draft.Items.Decisions)-1].Action, ShouldEqual, interfaces.DRAFT_DECISION_DEFINE)
		})

		Convey("A split keeps the concept as its first part and adds the others with its sources\n", func() {
			write()
			got, err := revise("split", interfaces.DraftConceptRevision{Key: "object_types/erp_material", Parts: []interfaces.DraftConceptPart{
				{Name: "物料", Verdict: "object"}, {Name: "物料批次", Description: "同一物料的一批", Verdict: "object"}}})
			So(err, ShouldBeNil)
			var added *interfaces.DraftConcept
			for i := range got.Concepts {
				if strings.HasPrefix(got.Concepts[i].Key, addedConceptPrefix) {
					added = &got.Concepts[i]
				}
			}
			So(added, ShouldNotBeNil)
			So(added.Name, ShouldEqual, "物料批次")
			So(added.SuggestedBecause, ShouldEqual, interfaces.CONCEPT_BECAUSE_AI)
			So(added.Reason, ShouldEqual, "From Ask AI")
			So(added.Sources[0].Name, ShouldEqual, "erp_material")
			So(added.State, ShouldEqual, interfaces.CONCEPT_STATE_PENDING)

			Convey("and adopting it again makes no second set\n", func() {
				_, err := revise("split", interfaces.DraftConceptRevision{Key: "object_types/erp_material", Parts: []interfaces.DraftConceptPart{
					{Name: "物料", Verdict: "object"}, {Name: "物料批次", Verdict: "object"}}})
				So(err, ShouldNotBeNil)
			})

			Convey("and the added concept is judged as one of the documents is\n", func() {
				write()
				judged, err := svc.JudgeConcept(ctx, token, "p1", interfaces.DraftConceptJudge{UpdateTime: draft.UpdateTime,
					Key: added.Key, Verdict: interfaces.CONCEPT_VERDICT_NONE}, caller)
				So(err, ShouldBeNil)
				So(conceptByKey(judged, added.Key).State, ShouldEqual, interfaces.CONCEPT_STATE_JUDGED)
			})
		})

		Convey("A split judges what the concept stays in the same write\n", func() {
			write()
			got, err := revise("split", interfaces.DraftConceptRevision{Key: "object_types/erp_vendor", Parts: []interfaces.DraftConceptPart{
				{Name: "供应商事件", Verdict: "event"}, {Name: "供应商", Verdict: "object"}}})
			So(err, ShouldBeNil)
			c := conceptByKey(got, "object_types/erp_vendor")
			So(c.Name, ShouldEqual, "供应商事件")
			So(c.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_EVENT)
		})

		Convey("A split renaming and judging a concept semantic understanding named keeps the new name\n", func() {
			write()
			got, err := revise("split", interfaces.DraftConceptRevision{Key: "object_types/erp_material", Parts: []interfaces.DraftConceptPart{
				{Name: "物料事件", Verdict: "event"}, {Name: "物料批次", Verdict: "object"}}})
			So(err, ShouldBeNil)
			c := conceptByKey(got, "object_types/erp_material")
			So(c.Name, ShouldEqual, "物料事件")
			So(c.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_EVENT)
			So(findObjectType(&draft.Items, "erp_material").Name, ShouldEqual, "物料事件")
		})

		Convey("Adopting a verdict judges the concept with the reason\n", func() {
			write()
			got, err := revise("verdict", interfaces.DraftConceptRevision{Key: "object_types/erp_vendor", Verdict: "none"})
			So(err, ShouldBeNil)
			c := conceptByKey(got, "object_types/erp_vendor")
			So(c.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_NONE)
			So(c.Reason, ShouldEqual, "From Ask AI")
		})

		Convey("Judging a concept understanding named gives its object type the name\n", func() {
			write()
			_, err := svc.JudgeConcept(ctx, token, "p1", interfaces.DraftConceptJudge{UpdateTime: draft.UpdateTime,
				Key: "object_types/erp_material", Verdict: interfaces.CONCEPT_VERDICT_OBJECT}, caller)
			So(err, ShouldBeNil)
			ot := findObjectType(&draft.Items, "erp_material")
			So(ot.Name, ShouldEqual, "物料")
			So(ot.Aliases, ShouldResemble, []string{"erp_material"})
			So(ot.Description, ShouldEqual, "记录物料主数据，每个物料一行")
		})

		Convey("A revision that no longer checks is refused\n", func() {
			_, err := revise("define", interfaces.DraftConceptRevision{Key: "object_types/gone", Name: "x"})
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusNotFound)
		})
	})
}
