// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_PendingOf(t *testing.T) {
	Convey("Test pendingOf\n", t, func() {
		project := &interfaces.Project{ID: "p1"}
		draft := func() *interfaces.Draft {
			d := confirmableDraft()
			d.UpdateTime = 100
			// orders waits for its identity; the relation from audit_log waits for its end.
			orders := d.Items.ObjectTypes[0]
			orders.NeedsIdentity = true
			orders.Key = interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_INFERRED, Field: "customer_id",
				Computed: true, Rows: 10, Distinct: 9}
			d.Items.RelationTypes = append(d.Items.RelationTypes, compileFixture().RelationTypes[2])
			return d
		}

		Convey("Each pending item comes with the requests that settle it, in one order\n", func() {
			p := pendingOf(project, draft(), nil)

			So(p.DraftUpdateTime, ShouldEqual, 100)
			So(len(p.Items), ShouldEqual, 2)
			orders := p.Items[0]
			So([]string{orders.Kind, orders.ItemID, orders.Reason}, ShouldResemble,
				[]string{"object_types", "orders", interfaces.BLOCKER_NEEDS_IDENTITY})
			So(orders.Recommendation, ShouldBeNil)
			So(orders.Options, ShouldResemble, []interfaces.PendingOption{
				{
					Action: interfaces.ACTION_CHOOSE_IDENTITY, Method: "PUT",
					Path: "/projects/p1/ontology/draft/object_types/orders/identity",
					Body: map[string]any{"update_time": "$draft_update_time", "properties": "$candidate"},
					// What drafting counted first, then the other key-typed properties by name.
					Allowed: true, Candidates: [][]string{{"customer_id"}, {"channel"}, {"order_id"}, {"status"}},
				},
				{
					Action: interfaces.ACTION_UNADOPT, Method: "PATCH", Path: "/projects/p1/ontology/draft/items/object_types/orders",
					Body: map[string]any{"update_time": "$draft_update_time", "adopted": false}, Allowed: true,
				},
			})
			relation := p.Items[1]
			So([]string{relation.Kind, relation.ItemID, relation.Reason}, ShouldResemble,
				[]string{"relation_types", "audit_log_to_orders", interfaces.BLOCKER_UNADOPTED_ENDS})
			So(relation.Options, ShouldResemble, []interfaces.PendingOption{
				{
					Action: interfaces.ACTION_ADOPT_END, Method: "PATCH", Path: "/projects/p1/ontology/draft/items/object_types/audit_log",
					Body: map[string]any{"update_time": "$draft_update_time", "adopted": true}, Allowed: true,
				},
				{
					Action: interfaces.ACTION_UNADOPT, Method: "PATCH",
					Path: "/projects/p1/ontology/draft/items/relation_types/audit_log_to_orders",
					Body: map[string]any{"update_time": "$draft_update_time", "adopted": false}, Allowed: true,
				},
			})
		})

		Convey("An end that waits for its own identity cannot be adopted yet\n", func() {
			d := draft()
			d.Items.ObjectTypes[3].NeedsIdentity = true

			adopt := pendingOf(project, d, nil).Items[1].Options[0]

			So(adopt.Allowed, ShouldBeFalse)
			So(adopt.Reason, ShouldEqual, interfaces.ACTION_REASON_END_NEEDS_IDENTITY)
		})

		Convey("An object type with no property that can identify records cannot have one chosen\n", func() {
			d := draft()
			d.Items.Decisions = nil
			for _, p := range d.Items.ObjectTypes[0].Properties {
				p.Type = "datetime"
			}
			d.Items.ObjectTypes[3].NeedsIdentity = true
			d.Items.ObjectTypes[3].Properties[0].Type = "decimal"

			p := pendingOf(project, d, nil)

			for _, item := range []interfaces.PendingItem{p.Items[0], p.Items[2]} {
				choose := item.Options[0]
				So(choose.Action, ShouldEqual, interfaces.ACTION_CHOOSE_IDENTITY)
				So(choose.Allowed, ShouldBeFalse)
				So(choose.Reason, ShouldEqual, interfaces.ACTION_REASON_NO_KEY_PROPERTY)
				So(choose.Candidates, ShouldBeEmpty)
			}
		})

		Convey("Nothing is settled in an archived project\n", func() {
			archived := &interfaces.Project{ID: "p1", Archived: true}

			for _, item := range pendingOf(archived, draft(), nil).Items {
				for _, option := range item.Options {
					So(option.Allowed, ShouldBeFalse)
					So(option.Reason, ShouldEqual, interfaces.ACTION_REASON_ARCHIVED)
				}
			}
		})

		Convey("It lists what confirming checks, no more and no less\n", func() {
			d := draft()
			p := pendingOf(project, d, nil)
			pending := pendingItems(&d.Items)

			So(len(p.Items), ShouldEqual, len(pending["needs_identity"])+len(pending["unadopted_ends"]))
		})

		Convey("Items no one decided on follow the blocking ones, with how to take them in\n", func() {
			d := draft()
			d.Items.Decisions = nil
			d.Items.ObjectTypes[3].NeedsIdentity = true
			d.Items.RelationTypes = append(d.Items.RelationTypes, compileFixture().RelationTypes[3])
			d.Items.RelationTypes[3].ProposedBecause = "below_adopt_hit_rate"

			p := pendingOf(project, d, nil)

			So(len(p.Items), ShouldEqual, 4)
			for _, item := range p.Items[:2] {
				So(item.Blocking, ShouldBeTrue)
			}
			auditLog, proposed := p.Items[2], p.Items[3]
			So([]any{auditLog.ItemID, auditLog.Blocking, auditLog.Reason, auditLog.Because}, ShouldResemble,
				[]any{"audit_log", false, interfaces.PENDING_REASON_UNDECIDED, interfaces.BLOCKER_NEEDS_IDENTITY})
			So(auditLog.Options[0].Action, ShouldEqual, interfaces.ACTION_CHOOSE_IDENTITY)
			adopt := auditLog.Options[1]
			So([]any{adopt.Action, adopt.Allowed, adopt.Reason}, ShouldResemble,
				[]any{interfaces.ACTION_ADOPT, false, interfaces.BLOCKER_NEEDS_IDENTITY})
			So([]any{proposed.ItemID, proposed.Because}, ShouldResemble, []any{"customers_to_products", "below_adopt_hit_rate"})
			So(proposed.Options[0].Action, ShouldEqual, interfaces.ACTION_ADOPT)
			So(proposed.Options[0].Allowed, ShouldBeTrue)
		})

		Convey("A draft with nothing pending lists nothing, as an empty list\n", func() {
			out, err := json.Marshal(pendingOf(project, confirmableDraft(), nil))

			So(err, ShouldBeNil)
			So(string(out), ShouldContainSubstring, `"items":[]`)
		})
	})
}

func Test_GetPending(t *testing.T) {
	Convey("Test draftService GetPending\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		rqa := pmock.NewMockRequirementAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, rqa: rqa}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}

		Convey("A ready draft lists its pending items, each with the knowledge questions that need it\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			d := confirmableDraft()
			d.Items.ObjectTypes[0].NeedsIdentity = true
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
			ot := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: d.Items.ObjectTypes[0].ID}
			rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{Status: interfaces.REQUIREMENT_STATUS_READY,
				Spec: interfaces.RequirementSpec{Questions: []*interfaces.RequirementQuestion{
					// Needing it twice names the question once.
					{ID: "q1", Text: "Which ones?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
						Needs: []interfaces.RequirementItemRef{ot, ot}},
					// A question a metric answers needs more than the item; it is not named.
					{ID: "q2", Text: "How many?", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY, Needs: []interfaces.RequirementItemRef{ot}},
					{ID: "q3", Text: "Of whom?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE, Needs: []interfaces.RequirementItemRef{
						{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: d.Items.ObjectTypes[0].ID}, ot}},
				}}}, true, nil)

			p, err := svc.GetPending(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(len(p.Items), ShouldEqual, 1)
			So(p.DraftUpdateTime, ShouldEqual, readTime)
			So(p.Items[0].NeededBy, ShouldResemble, []interfaces.PendingQuestion{{ID: "q1", Text: "Which ones?"}, {ID: "q3", Text: "Of whom?"}})
		})

		Convey("A specification not ready names no question\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			d := confirmableDraft()
			d.Items.ObjectTypes[0].NeedsIdentity = true
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
			rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{Status: interfaces.REQUIREMENT_STATUS_DRAFTING,
				Spec: interfaces.RequirementSpec{Questions: []*interfaces.RequirementQuestion{{ID: "q1", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
					Needs: []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: d.Items.ObjectTypes[0].ID}}}}}}, true, nil)

			p, err := svc.GetPending(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(p.Items[0].NeededBy, ShouldBeNil)
		})

		Convey("A specification that cannot be read fails the list\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, errors.New("db down"))

			_, err := svc.GetPending(ctx, token, "p1")

			So(err, ShouldNotBeNil)
		})

		Convey("A draft not ready lists nothing yet\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").
				Return(&interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_DRAFTING}, true, nil)

			p, err := svc.GetPending(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(p.Items, ShouldBeEmpty)
		})

		Convey("No draft is not found\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)

			_, err := svc.GetPending(ctx, token, "p1")

			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
		})
	})
}
