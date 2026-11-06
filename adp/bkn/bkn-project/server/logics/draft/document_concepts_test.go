// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// readableDocuments lets every draft be read, as an edition does for a caller who may read its
// documents.
type readableDocuments struct{ noDocumentHooks }

func (readableDocuments) CheckRead(context.Context, string, *interfaces.Draft) error { return nil }

// documentConceptDraft is a draft of two domains whose document drafting left concepts it modeled
// no item for: a retail discount rate read as a property and a hospital's ward states as a value list.
func documentConceptDraft() *interfaces.Draft {
	draft := candidateDraft()
	draft.Items.DocumentConcepts = []*interfaces.DraftDocumentConcept{
		{ID: "c1", Name: "Discount rate", Description: "Share taken off the list price",
			Verdict: interfaces.CONCEPT_VERDICT_PROPERTY, Reason: "A value of an order, no identity of its own",
			Citations: []interfaces.DraftSourceCitation{{FileID: "promo"}, {FileID: "promo"}, {FileID: "pricing"}}},
		{ID: "c2", Name: "Ward status", Description: "Open, closed or quarantined",
			Verdict: interfaces.CONCEPT_VERDICT_VALUES, Reason: "A fixed list of states",
			Citations: []interfaces.DraftSourceCitation{{FileID: "ward_rules"}}},
		nil,
	}
	return draft
}

func Test_DocumentConcepts(t *testing.T) {
	Convey("Test the concepts document drafting modeled no item for\n", t, func() {
		ctx := context.Background()
		draft := documentConceptDraft()

		Convey("Each is listed with its verdict, the reason drafting gave and its files\n", func() {
			got := conceptsOf(ctx, "p1", draft, conceptRequirement(), true)
			rate := conceptByKey(got, "document_concepts/c1")
			So(rate, ShouldNotBeNil)
			So(rate.Item, ShouldBeNil)
			So(rate.Suggested, ShouldEqual, interfaces.CONCEPT_VERDICT_PROPERTY)
			So(rate.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_PROPERTY)
			So(rate.SuggestedBecause, ShouldEqual, "document_concept")
			So(rate.Reason, ShouldEqual, "A value of an order, no identity of its own")
			So(rate.State, ShouldEqual, interfaces.CONCEPT_STATE_PENDING)
			So(rate.Sources, ShouldResemble, []interfaces.DraftConceptSource{
				{Kind: interfaces.CONCEPT_SOURCE_DOCUMENT, ID: "promo"}, {Kind: interfaces.CONCEPT_SOURCE_DOCUMENT, ID: "pricing"},
			})
			ward := conceptByKey(got, "document_concepts/c2")
			So(ward.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_VALUES)
			So(got.Counts["verdict:"+interfaces.CONCEPT_VERDICT_VALUES], ShouldEqual, 1)
			// Requirement concepts are still read beside them.
			So(conceptByKey(got, "requirement_concepts/k3"), ShouldNotBeNil)
		})

		Convey("A person's judgment is the verdict, with its reason\n", func() {
			draft.Items.Decisions = append(draft.Items.Decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_CONCEPTS,
				ItemID: "document_concepts/c2", Action: interfaces.DRAFT_DECISION_JUDGE, After: interfaces.CONCEPT_VERDICT_NONE,
				Reason: "Kept in the bed system", By: reviewer, Time: 7})
			ward := conceptByKey(conceptsOf(ctx, "p1", draft, nil, false), "document_concepts/c2")
			So(ward.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_NONE)
			So(ward.Reason, ShouldEqual, "Kept in the bed system")
			So(ward.State, ShouldEqual, interfaces.CONCEPT_STATE_JUDGED)
			So(ward.Judged, ShouldResemble, &interfaces.DraftReview{By: reviewer, Time: 7})
		})

		Convey("Without an edition a draft holding them is refused as a source out of reach\n", func() {
			err := noDocumentHooks{}.CheckRead(ctx, token, draft)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusServiceUnavailable)
		})
	})

	Convey("Test judging a concept document drafting modeled no item for\n", t, func() {
		defer projecthooks.SetForTest(projecthooks.Hooks{Drafts: readableDocuments{}})()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		rqa := pmock.NewMockRequirementAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, rqa: rqa}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil).AnyTimes()
		rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		draft := documentConceptDraft()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()

		Convey("The judgment is recorded and no item changes\n", func() {
			var written *interfaces.Draft
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).DoAndReturn(
				func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					written = d
					return true, nil
				})
			adopted := map[string]bool{}
			for _, ot := range draft.Items.ObjectTypes {
				adopted[ot.ID] = ot.Adopted
			}
			got, err := svc.JudgeConcept(ctx, token, "p1", interfaces.DraftConceptJudge{UpdateTime: readTime,
				Key: "document_concepts/c1", Verdict: interfaces.CONCEPT_VERDICT_KINETIC, Reason: " Computed per order "}, caller)
			So(err, ShouldBeNil)
			last := written.Items.Decisions[len(written.Items.Decisions)-1]
			So(last.Kind, ShouldEqual, interfaces.DRAFT_KIND_CONCEPTS)
			So(last.ItemID, ShouldEqual, "document_concepts/c1")
			So(last.Before, ShouldEqual, interfaces.CONCEPT_VERDICT_PROPERTY)
			So(last.After, ShouldEqual, interfaces.CONCEPT_VERDICT_KINETIC)
			for _, ot := range written.Items.ObjectTypes {
				So(ot.Adopted, ShouldEqual, adopted[ot.ID])
			}
			rate := conceptByKey(got, "document_concepts/c1")
			So(rate.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_KINETIC)
			So(rate.Reason, ShouldEqual, "Computed per order")
			So(rate.State, ShouldEqual, interfaces.CONCEPT_STATE_JUDGED)
		})

		Convey("An unknown one is not found\n", func() {
			_, err := svc.JudgeConcept(ctx, token, "p1", interfaces.DraftConceptJudge{UpdateTime: readTime,
				Key: "document_concepts/c9", Verdict: interfaces.CONCEPT_VERDICT_NONE}, caller)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusNotFound)
		})
	})
}
