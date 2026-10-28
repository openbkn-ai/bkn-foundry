// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// confirmableDraft is the compile fixture as a ready draft: nothing waits for a person, since the
// relation adopted while its end is not is left out.
func confirmableDraft() *interfaces.Draft {
	items := compileFixture()
	items.RelationTypes = items.RelationTypes[:2]
	return &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, Items: *items, UpdateTime: readTime}
}

func compiledPaths() []string {
	compiled, err := compileDraft(&confirmableDraft().Items)
	So(err, ShouldBeNil)
	paths := []string{}
	for p := range compiled.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func Test_ConfirmDraft(t *testing.T) {
	Convey("Test draftService ConfirmDraft\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		ls := pmock.NewMockLineageService(ctrl)
		svc := &draftService{da: da, ps: ps, ls: ls}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{
			ID: "p1", KNID: "supply", Branch: "project/p1", Owner: caller,
		}}
		confirm := interfaces.DraftConfirm{UpdateTime: readTime}

		Convey("The owner's confirmation commits the adopted items and records the commit\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			paths := compiledPaths()
			var claimed interfaces.DraftConfirmation
			first := da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					claimed = *d.Items.Confirmation
					return true, nil
				})
			// Nothing on the branch is this draft's yet: an existing definition of the same path is
			// refused, not replaced.
			ls.EXPECT().AddDefinitions(gomock.Any(), "supply", "project/p1", gomock.Any(), []string(nil), gomock.Any(),
				interfaces.GitAuthor{Name: caller.ID}).
				DoAndReturn(func(_ context.Context, _ string, _ string, files map[string][]byte, _ []string, _ string,
					_ interfaces.GitAuthor) (*interfaces.CommitDefinitionsResult, error) {
					So(len(files), ShouldEqual, len(paths))
					return &interfaces.CommitDefinitionsResult{Commit: "c1"}, nil
				}).After(first)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)

			draft, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			So(err, ShouldBeNil)
			// Claimed before the branch: not confirmed, what it is about to write recorded apart.
			So(claimed.UpdateTime, ShouldEqual, 0)
			So(claimed.Commit, ShouldEqual, "")
			So(claimed.Files, ShouldBeEmpty)
			So(claimed.Pending, ShouldResemble, paths)
			c := draft.Items.Confirmation
			So(c.Commit, ShouldEqual, "c1")
			So(c.By, ShouldResemble, caller)
			So(c.Files, ShouldResemble, paths)
			So(c.Pending, ShouldBeEmpty)
			So(c.SkippedValueTypes, ShouldBeEmpty)
			// Confirmed is the confirmation of the draft as it now is.
			So(c.UpdateTime, ShouldEqual, draft.UpdateTime)
			So(draft.UpdateTime, ShouldBeGreaterThan, readTime)
		})

		Convey("Confirming again may replace what the previous confirmation wrote\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			previous := confirmableDraft()
			previous.Items.Confirmation = &interfaces.DraftConfirmation{
				UpdateTime: readTime - 10, Commit: "c0", Files: []string{"object_types/audit_log.bkn"},
			}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(previous, true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
			// Only what the last confirmation wrote is the draft's own.
			ls.EXPECT().AddDefinitions(gomock.Any(), "supply", "project/p1", gomock.Any(),
				[]string{"object_types/audit_log.bkn"}, gomock.Any(), gomock.Any()).
				Return(&interfaces.CommitDefinitionsResult{Commit: "c2"}, nil)

			draft, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			So(err, ShouldBeNil)
			So(draft.Items.Confirmation.Commit, ShouldEqual, "c2")
			So(draft.Items.Confirmation.Files, ShouldResemble, compiledPaths())
		})

		Convey("A refused commit drops what it was about to write, and the draft is not confirmed\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			before := confirmableDraft()
			earlier := interfaces.AccountInfo{ID: "u0", Type: "user"}
			before.Items.Confirmation = &interfaces.DraftConfirmation{
				UpdateTime: readTime, Commit: "c0", By: earlier, Time: 50, Files: []string{"object_types/customers.bkn"},
			}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(before, true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)
			refused := httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_DefinitionsExist)
			ls.EXPECT().AddDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any()).Return(nil, refused)
			var cleared interfaces.DraftConfirmation
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					cleared = *d.Items.Confirmation
					return true, nil
				})

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			So(errors.Is(err, refused), ShouldBeTrue)
			// The paths it was refused for are not its own next time.
			So(cleared.Pending, ShouldBeEmpty)
			So(cleared.UpdateTime, ShouldEqual, 0)
			// The record is still the last confirmation's, whole: not mixed with the refused one.
			So(cleared.Commit, ShouldEqual, "c0")
			So(cleared.By, ShouldResemble, earlier)
			So(cleared.Time, ShouldEqual, 50)
			So(cleared.Files, ShouldResemble, []string{"object_types/customers.bkn"})
		})

		Convey("What a confirmation stopped after committing left pending is the draft's own\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			stopped := confirmableDraft()
			stopped.Items.Confirmation = &interfaces.DraftConfirmation{
				Files: []string{"object_types/audit_log.bkn"}, Pending: []string{"object_types/orders.bkn"},
			}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(stopped, true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
			ls.EXPECT().AddDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
				[]string{"object_types/audit_log.bkn", "object_types/orders.bkn"}, gomock.Any(), gomock.Any()).
				Return(&interfaces.CommitDefinitionsResult{Commit: "c3"}, nil)

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			So(err, ShouldBeNil)
		})

		Convey("Someone other than the owner cannot confirm\n", func() {
			other := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: interfaces.AccountInfo{ID: "u2"}}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(other, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Draft_NotOwner)
		})

		Convey("What waits for a person is named, and nothing is committed\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			waiting := confirmableDraft()
			waiting.Items.ObjectTypes[0].NeedsIdentity = true
			waiting.Items.RelationTypes = compileFixture().RelationTypes
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(waiting, true, nil)

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_HasPending)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.ErrorDetails, ShouldResemble, map[string][]string{
				"needs_identity": {"orders"},
				"unadopted_ends": {"audit_log_to_orders"},
			})
		})

		Convey("An object type without an identity that is not adopted waits for no one\n", func() {
			items := compileFixture()
			items.ObjectTypes[3].NeedsIdentity = true
			So(pendingItems(&interfaces.DraftItems{ObjectTypes: items.ObjectTypes[3:]}), ShouldBeEmpty)
		})

		Convey("A draft with no object type adopted is refused\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			empty := confirmableDraft()
			for _, ot := range empty.Items.ObjectTypes {
				ot.Adopted = false
			}
			for _, rt := range empty.Items.RelationTypes {
				rt.Adopted = false
			}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(empty, true, nil)

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_NothingAdopted)
		})

		Convey("A confirmation of a draft changed since is stale\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)

			_, err := svc.ConfirmDraft(ctx, token, "p1", interfaces.DraftConfirm{UpdateTime: readTime - 1}, caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_Stale)
		})

		Convey("Someone else's change between reading and claiming is stale, and nothing is committed\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(false, nil)

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_Stale)
		})
	})
}
