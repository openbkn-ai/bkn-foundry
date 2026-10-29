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
	// Leaving audit_log out was a person's decision, so nothing is undecided.
	items.Decisions = []*interfaces.DraftDecision{
		{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "audit_log", Action: interfaces.DRAFT_DECISION_UNADOPT},
	}
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
		ca := pmock.NewMockCheckAccess(ctrl)
		ira := &fakeIDRegistry{}
		svc := &draftService{da: da, ps: ps, ls: ls, ca: ca, ira: ira, run: func(func()) {}}
		// A confirmation that commits starts a check of its commit; the check itself is tested apart.
		var checked []string
		ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		ca.EXPECT().StartCheck(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, c *interfaces.Check, _ int64) (bool, error) {
				checked = append(checked, c.Commit)
				return true, nil
			}).AnyTimes()
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
					// The ids of what is written are locked before it is on the branch.
					compiled, _ := compileDraft(&confirmableDraft().Items)
					So(ira.locked, ShouldResemble, compiled.IDKeys)
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
			So(checked, ShouldResemble, []string{"c1"})
			So(ira.locked, ShouldNotBeEmpty)
			So(ira.lockTime, ShouldEqual, draft.UpdateTime)
			So(ira.unlocked, ShouldBeEmpty)
		})

		Convey("A failure to read the registered ids writes nothing and locks nothing\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
			ira.listErr = errors.New("database is down")

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
			So(ira.locked, ShouldBeEmpty)
			So(checked, ShouldBeEmpty)
		})

		Convey("A failure to lock the ids writes nothing and leaves the draft as it was read\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			var claimTime int64
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					claimTime = d.UpdateTime
					return true, nil
				})
			ira.lockErr = errors.New("database is down")
			var restored interfaces.Draft
			var restoredOver int64
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, read int64) (bool, error) {
					restored, restoredOver = *d, read
					return true, nil
				})

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
			So(restoredOver, ShouldEqual, claimTime)
			So(restored.UpdateTime, ShouldEqual, readTime)
			So(restored.Items.Confirmation, ShouldBeNil)
			So(checked, ShouldBeEmpty)
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

		Convey("A refused commit leaves the draft as it was read\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			earlier := interfaces.AccountInfo{ID: "u0", Type: "user"}
			last := func() *interfaces.DraftConfirmation {
				return &interfaces.DraftConfirmation{
					UpdateTime: readTime, Commit: "c0", By: earlier, Time: 50, Files: []string{"object_types/customers.bkn"},
					// Left by a confirmation that stopped after committing: on the branch, the draft's own.
					Pending: []string{"object_types/stopped.bkn"},
				}
			}
			before := confirmableDraft()
			before.Items.Confirmation = last()
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(before, true, nil)
			// orders' id was registered unlocked, customers' locked by an earlier confirmation.
			ira.ids = []*interfaces.RegisteredID{
				registered(interfaces.ID_KIND_OBJECT_TYPES, "res_orders", "ot_000000", interfaces.ID_ORIGIN_FALLBACK, false),
				registered(interfaces.ID_KIND_OBJECT_TYPES, "res_customers", "customers", interfaces.ID_ORIGIN_SEMANTIC, true),
			}
			var claimed []string
			var claimTime int64
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					claimed = append([]string{}, d.Items.Confirmation.Pending...)
					claimTime = d.UpdateTime
					return true, nil
				})
			refused := httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_DefinitionsExist)
			ls.EXPECT().AddDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any()).Return(nil, refused)
			var restored interfaces.Draft
			var restoredOver int64
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, read int64) (bool, error) {
					restored, restoredOver = *d, read
					return true, nil
				})

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			So(errors.Is(err, refused), ShouldBeTrue)
			// The claim kept what was left pending beside what this attempt was about to write.
			So(claimed, ShouldResemble, unionSorted([]string{"object_types/stopped.bkn"}, compiledPaths()))
			// Written back over the claim, as read: still confirmed by the last confirmation, and
			// confirmable again with the update time the person read.
			So(restoredOver, ShouldEqual, claimTime)
			So(restored.UpdateTime, ShouldEqual, readTime)
			So(restored.Items.Confirmation, ShouldResemble, last())
			So(checked, ShouldBeEmpty)
			// Nothing was written: the ids this confirmation locked are given back, the others stay.
			So(ira.unlocked, ShouldResemble, []interfaces.IDKey{{Kind: interfaces.ID_KIND_OBJECT_TYPES, SourceKey: "res_orders"}})
		})

		Convey("A refused commit is written back after the caller went away\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)
			gone, cancel := context.WithCancel(ctx)
			refused := httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_DefinitionsExist)
			// The caller disconnects while the branch is being written.
			ls.EXPECT().AddDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, string, string, map[string][]byte, []string, string,
					interfaces.GitAuthor) (*interfaces.CommitDefinitionsResult, error) {
					cancel()
					return nil, refused
				})
			var restoreErr error
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(c context.Context, _ *interfaces.Draft, _ int64) (bool, error) {
					restoreErr = c.Err()
					return true, nil
				})

			_, err := svc.ConfirmDraft(gone, token, "p1", confirm, caller)

			So(errors.Is(err, refused), ShouldBeTrue)
			So(restoreErr, ShouldBeNil)
		})

		Convey("A committed confirmation is recorded after the caller went away\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)
			gone, cancel := context.WithCancel(ctx)
			// The caller disconnects while the branch is being written.
			ls.EXPECT().AddDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, string, string, map[string][]byte, []string, string,
					interfaces.GitAuthor) (*interfaces.CommitDefinitionsResult, error) {
					cancel()
					return &interfaces.CommitDefinitionsResult{Commit: "c1"}, nil
				})
			var recordErr error
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(c context.Context, _ *interfaces.Draft, _ int64) (bool, error) {
					recordErr = c.Err()
					return true, nil
				})

			draft, err := svc.ConfirmDraft(gone, token, "p1", confirm, caller)

			So(err, ShouldBeNil)
			So(recordErr, ShouldBeNil)
			So(draft.Items.Confirmation.Commit, ShouldEqual, "c1")
		})

		Convey("A partial draft is not confirmed unless its owner accepts what drafting could not read\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			partial := confirmableDraft()
			partial.Items.Unreadable = []*interfaces.DraftTable{{ResourceID: "r_hidden", Name: "hidden", Reason: "not_visible"}}
			partial.Items.Inputs = &interfaces.DraftInputs{Partial: true}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(partial, true, nil)

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_PartialNotAccepted)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.ErrorDetails, ShouldResemble, []string{"r_hidden"})
		})

		Convey("Accepting a partial draft is recorded as a decision once the confirmation commits\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			partial := confirmableDraft()
			partial.Items.Unreadable = []*interfaces.DraftTable{{ResourceID: "r_hidden", Reason: "not_visible"}}
			partial.Items.Inputs = &interfaces.DraftInputs{Partial: true}
			// Leaving audit_log out is accepted in the same confirmation.
			partial.Items.Decisions = nil
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(partial, true, nil)
			var claimed int
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					claimed = len(d.Items.Decisions)
					return true, nil
				})
			ls.EXPECT().AddDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any()).Return(&interfaces.CommitDefinitionsResult{Commit: "c1"}, nil)
			var recorded []*interfaces.DraftDecision
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					recorded = d.Items.Decisions
					return true, nil
				})

			_, err := svc.ConfirmDraft(ctx, token, "p1",
				interfaces.DraftConfirm{UpdateTime: readTime, AcceptPartial: true, AcceptUndecided: true}, caller)

			So(err, ShouldBeNil)
			So(claimed, ShouldEqual, 0)
			So(len(recorded), ShouldEqual, 2)
			So(recorded[1].Kind, ShouldEqual, interfaces.DRAFT_KIND_DRAFT)
			So(recorded[1].Action, ShouldEqual, interfaces.DRAFT_DECISION_ACCEPT_PARTIAL)
			So(recorded[1].After, ShouldResemble, []string{"r_hidden"})
			So(recorded[1].By, ShouldResemble, caller)
		})

		Convey("A draft with undecided items is confirmed only when its owner accepts leaving them out\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			undecided := confirmableDraft()
			undecided.Items.ObjectTypes[3].NeedsIdentity = true
			undecided.Items.Decisions = nil
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(undecided, true, nil)

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_UndecidedNotAccepted)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.ErrorDetails, ShouldResemble, []string{"object_types/audit_log"})
		})

		Convey("Accepting undecided items is recorded once the confirmation commits, and decides them\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			undecided := confirmableDraft()
			undecided.Items.Decisions = nil
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(undecided, true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)
			ls.EXPECT().AddDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any()).Return(&interfaces.CommitDefinitionsResult{Commit: "c1"}, nil)
			var recorded *interfaces.Draft
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					recorded = d
					return true, nil
				})

			_, err := svc.ConfirmDraft(ctx, token, "p1",
				interfaces.DraftConfirm{UpdateTime: readTime, AcceptUndecided: true}, caller)

			So(err, ShouldBeNil)
			last := recorded.Items.Decisions[len(recorded.Items.Decisions)-1]
			So(last.Action, ShouldEqual, interfaces.DRAFT_DECISION_ACCEPT_UNDECIDED)
			So(last.After, ShouldResemble, []string{"object_types/audit_log"})
			So(undecidedItems(&recorded.Items), ShouldBeEmpty)
		})

		Convey("A draft drafted before inputs were recorded is confirmed as before\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			older := confirmableDraft()
			older.Items.Unreadable = []*interfaces.DraftTable{{ResourceID: "r_hidden", Reason: "not_visible"}}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(older, true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
			ls.EXPECT().AddDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any()).Return(&interfaces.CommitDefinitionsResult{Commit: "c1"}, nil)

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			So(err, ShouldBeNil)
		})

		Convey("A refused first commit leaves the draft without a confirmation\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)
			refused := httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_LineageNotV3)
			ls.EXPECT().AddDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any()).Return(nil, refused)
			var restored interfaces.Draft
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					restored = *d
					return true, nil
				})

			_, err := svc.ConfirmDraft(ctx, token, "p1", confirm, caller)

			So(errors.Is(err, refused), ShouldBeTrue)
			So(restored.UpdateTime, ShouldEqual, readTime)
			So(restored.Items.Confirmation, ShouldBeNil)
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
