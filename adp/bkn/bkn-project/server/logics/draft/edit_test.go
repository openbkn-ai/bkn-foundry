// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const readTime = int64(100)

// editableDraft is a ready draft: customers and orders adopted and related, regions not
// adopted, and a relation to regions that is not adopted either.
func editableDraft() *interfaces.Draft {
	return &interfaces.Draft{
		ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				{ID: "customers", Name: "Customers", Adopted: true},
				{ID: "orders", Name: "Orders", Description: "Sales orders", Adopted: true},
				{ID: "regions", Name: "Regions"},
			},
			RelationTypes: []*interfaces.DraftRelationType{
				{ID: "orders_customers", Name: "Orders customers", Source: "orders", Target: "customers", Adopted: true},
				{ID: "customers_regions", Name: "Customers regions", Source: "customers", Target: "regions"},
			},
			ValueTypes: []*interfaces.DraftValueType{{ID: "levels", Name: "Levels", Adopted: true}},
		},
	}
}

func boolPtr(b bool) *bool           { return &b }
func strPtr(s string) *string        { return &s }
func edit() interfaces.DraftItemEdit { return interfaces.DraftItemEdit{UpdateTime: readTime} }

func Test_EditDraftItem(t *testing.T) {
	Convey("Test editing a draft item\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, run: func(fn func()) { fn() }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		draft := editableDraft()
		var written *interfaces.Draft
		expectWrite := func() {
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					written = d
					return true, nil
				})
		}

		Convey("Not adopting an object type stops adopting the relation types that connect it\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Adopted = boolPtr(false)

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "customers", e, caller)

			So(err, ShouldBeNil)
			So(got, ShouldEqual, written)
			So(got.Items.ObjectTypes[0].Adopted, ShouldBeFalse)
			So(got.Items.RelationTypes[0].Adopted, ShouldBeFalse)
			// The update time moves on, so the next edit must be made on this draft.
			So(got.UpdateTime, ShouldBeGreaterThan, readTime)
			decisions := got.Items.Decisions
			So(len(decisions), ShouldEqual, 2)
			So(*decisions[0], ShouldResemble, interfaces.DraftDecision{
				Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "customers", Action: interfaces.DRAFT_DECISION_UNADOPT,
				Before: true, After: false, By: caller, Time: got.UpdateTime,
			})
			So(decisions[1].Kind, ShouldEqual, interfaces.DRAFT_KIND_RELATION_TYPES)
			So(decisions[1].ItemID, ShouldEqual, "orders_customers")
			So(decisions[1].Action, ShouldEqual, interfaces.DRAFT_DECISION_UNADOPT)
		})

		Convey("A relation type is adopted only when both its object types are\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			e := edit()
			e.Adopted = boolPtr(true)

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_RELATION_TYPES, "customers_regions", e, caller)

			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_EndpointNotAdopted)
		})

		Convey("Adopting a relation type of adopted object types records the decision\n", func() {
			draft.Items.ObjectTypes[2].Adopted = true
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Adopted = boolPtr(true)

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_RELATION_TYPES, "customers_regions", e, caller)

			So(err, ShouldBeNil)
			So(got.Items.RelationTypes[1].Adopted, ShouldBeTrue)
			So(got.Items.Decisions[0].Action, ShouldEqual, interfaces.DRAFT_DECISION_ADOPT)
		})

		Convey("Renaming and describing record what they replaced\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Name = strPtr("  Sales orders  ")
			e.Description = strPtr("Orders customers place")

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			So(err, ShouldBeNil)
			So(got.Items.ObjectTypes[1].Name, ShouldEqual, "Sales orders")
			So(got.Items.ObjectTypes[1].Description, ShouldEqual, "Orders customers place")
			decisions := got.Items.Decisions
			So(len(decisions), ShouldEqual, 2)
			So(decisions[0].Action, ShouldEqual, interfaces.DRAFT_DECISION_RENAME)
			So(decisions[0].Before, ShouldEqual, "Orders")
			So(decisions[0].After, ShouldEqual, "Sales orders")
			So(decisions[1].Action, ShouldEqual, interfaces.DRAFT_DECISION_DESCRIBE)
			So(decisions[1].Before, ShouldEqual, "Sales orders")
		})

		Convey("An object type without an identity cannot be adopted\n", func() {
			draft.Items.ObjectTypes[2].NeedsIdentity = true
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			e := edit()
			e.Adopted = boolPtr(true)

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "regions", e, caller)

			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_NeedsIdentity)
		})

		Convey("Value types can be renamed and not adopted\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Adopted = boolPtr(false)
			e.Name = strPtr("Customer levels")

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_VALUE_TYPES, "levels", e, caller)

			So(err, ShouldBeNil)
			So(got.Items.ValueTypes[0].Adopted, ShouldBeFalse)
			So(got.Items.ValueTypes[0].Name, ShouldEqual, "Customer levels")
			So(len(got.Items.Decisions), ShouldEqual, 2)
		})

		Convey("An edit that changes nothing writes nothing\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			e := edit()
			e.Adopted = boolPtr(true)
			e.Name = strPtr("Orders")

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			So(err, ShouldBeNil)
			So(got.UpdateTime, ShouldEqual, readTime)
			So(got.Items.Decisions, ShouldBeEmpty)
		})

		Convey("Invalid edits are refused before the draft is read\n", func() {
			cases := []struct {
				edit interfaces.DraftItemEdit
				code string
			}{
				{edit(), perrors.BknProject_Draft_EmptyEdit},
				// Without the draft's update time, reloading would never make the edit pass.
				{interfaces.DraftItemEdit{Adopted: boolPtr(true)}, perrors.BknProject_Draft_MissingUpdateTime},
				{interfaces.DraftItemEdit{UpdateTime: readTime, Name: strPtr("  ")}, perrors.BknProject_Draft_InvalidName},
				{interfaces.DraftItemEdit{UpdateTime: readTime, Name: strPtr(strings.Repeat("\u540d", 129))}, perrors.BknProject_Draft_InvalidName},
				{interfaces.DraftItemEdit{UpdateTime: readTime, Description: strPtr(strings.Repeat("\u8ff0", 1001))}, perrors.BknProject_Draft_InvalidDescription},
			}
			for _, c := range cases {
				_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", c.edit, caller)
				shouldBeHTTPError(err, http.StatusBadRequest, c.code)
			}
		})

		Convey("A name of exactly the limit is accepted\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Name = strPtr(strings.Repeat("\u540d", interfaces.DRAFT_NAME_MAX_LENGTH))

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			So(err, ShouldBeNil)
		})

		Convey("An edit made on an older draft is refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			e := interfaces.DraftItemEdit{UpdateTime: readTime - 1, Adopted: boolPtr(false)}

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_Stale)
		})

		Convey("An edit racing another one is refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(false, nil)
			e := edit()
			e.Adopted = boolPtr(false)

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_Stale)
		})

		Convey("Only a ready draft of an unarchived project can be edited\n", func() {
			e := edit()
			e.Adopted = boolPtr(false)

			Convey("archived project\n", func() {
				archived := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p2", Archived: true}}
				ps.EXPECT().GetProject(gomock.Any(), token, "p2").Return(archived, nil)
				_, err := svc.EditDraftItem(ctx, token, "p2", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)
				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_ProjectArchived)
			})
			Convey("drafting\n", func() {
				draft.Status = interfaces.DRAFT_STATUS_DRAFTING
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
				_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)
				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_NotReady)
			})
			Convey("no draft\n", func() {
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
				_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)
				shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
			})
			Convey("unknown item or kind\n", func() {
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).Times(2)
				_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "missing", e, caller)
				shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
				_, err = svc.EditDraftItem(ctx, token, "p1", "excluded", "orders", e, caller)
				shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
			})
		})
	})
}

func Test_GenerateDraftGuards(t *testing.T) {
	Convey("Test what keeps drafting from starting\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, va: va, ps: ps, ira: &fakeIDRegistry{}, run: func(fn func()) {}}
		ctx := context.Background()

		Convey("An archived project is not drafted\n", func() {
			archived := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", CatalogIDs: []string{"c1"}, Archived: true}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(archived, nil)

			_, err := svc.GenerateDraft(ctx, token, "p1", caller, false)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_ProjectArchived)
		})

		Convey("Drafting again keeps people's decisions unless told to discard them\n", func() {
			project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", CatalogIDs: []string{"c1"}}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).Times(2)
			va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c1"}).Return(nil, nil).Times(2)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(false, interfaces.ErrDraftHasDecisions)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), true).Return(true, nil)

			_, err := svc.GenerateDraft(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_HasDecisions)

			_, err = svc.GenerateDraft(ctx, token, "p1", caller, true)
			So(err, ShouldBeNil)
		})
	})
}
