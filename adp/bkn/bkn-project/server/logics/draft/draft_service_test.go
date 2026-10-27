// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const token = "Bearer t"

var caller = interfaces.AccountInfo{ID: "u1", Type: "user"}

func shouldBeHTTPError(err error, status int, code string) {
	var httpErr *httperrors.HTTPError
	So(errors.As(err, &httpErr), ShouldBeTrue)
	So(httpErr.HTTPCode, ShouldEqual, status)
	So(httpErr.ErrorCode, ShouldEqual, code)
}

func Test_DraftService(t *testing.T) {
	Convey("Test draftService\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		setting := testSetting
		setting.Timeout = time.Minute
		setting.QueryTimeout = 60
		svc := &draftService{setting: setting, da: da, va: va, ps: ps, run: func(fn func()) { fn() }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", CatalogIDs: []string{"c1"}}}

		Convey("Generate starts drafting as the caller and records the draft when it is done\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), false).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, staleBefore int64, _ bool) (bool, error) {
					So(d.Status, ShouldEqual, interfaces.DRAFT_STATUS_DRAFTING)
					So(d.Drafter, ShouldResemble, caller)
					// Stale after the drafting timeout and a minute to record it.
					So(d.CreateTime-staleBefore, ShouldEqual, (2 * time.Minute).Milliseconds())
					return true, nil
				})
			levels := table("r_levels", "c1", "levels", 3, []string{"code"}, col("code", "string", ""), col("name", "string", ""))
			va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return([]*interfaces.VegaTable{
				{ID: "r_customers", Name: "customers", CatalogID: "c1"},
				{ID: "r_hidden", Name: "hidden", CatalogID: "c1"},
				{ID: "r_empty", Name: "empty", CatalogID: "c1"},
				{ID: "r_levels", Name: "levels", CatalogID: "c1"},
			}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_customers", "r_hidden", "r_empty", "r_levels"}).
				Return([]*interfaces.VegaTable{
					table("r_customers", "c1", "customers", 2, []string{"id"}, col("id", "string", "")),
					{ID: "r_empty", Name: "empty", CatalogID: "c1"},
					levels,
				}, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 60).
				Return([]map[string]any{{"total": float64(2), "distinct_values": float64(2), "nulls": float64(0)}}, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 60).
				Return([]map[string]any{{"code_value": "T1", "display_value": "Tier 1"}}, nil)
			var finished *interfaces.Draft
			da.EXPECT().FinishDraft(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft) error { finished = d; return nil })

			draft, err := svc.GenerateDraft(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(draft.Status, ShouldEqual, interfaces.DRAFT_STATUS_DRAFTING)
			So(finished.Status, ShouldEqual, interfaces.DRAFT_STATUS_READY)
			So(finished.Items.ObjectTypes[0].ID, ShouldEqual, "customers")
			So(finished.Items.ValueTypes[0].ID, ShouldEqual, "levels")
			So(finished.Items.Unreadable, ShouldResemble, []*interfaces.DraftTable{
				{ResourceID: "r_hidden", Name: "hidden", CatalogID: "c1", Reason: unreadableNotVisible},
				{ResourceID: "r_empty", Name: "empty", CatalogID: "c1", Reason: unreadableNoStructure},
			})
		})

		Convey("A failure reading vega-backend fails the draft with its reason\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(true, nil)
			va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return(nil, errors.New("connection refused"))
			var finished *interfaces.Draft
			da.EXPECT().FinishDraft(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft) error { finished = d; return nil })

			_, err := svc.GenerateDraft(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.DRAFT_STATUS_FAILED)
			So(finished.Error, ShouldContainSubstring, "connection refused")
		})

		Convey("Drafting that runs out of time fails as timed out\n", func() {
			svc.setting.Timeout = time.Nanosecond
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(true, nil)
			va.EXPECT().ListTables(gomock.Any(), caller, "c1").
				DoAndReturn(func(ctx context.Context, _ interfaces.VegaAccount, _ string) ([]*interfaces.VegaTable, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				})
			var finished *interfaces.Draft
			da.EXPECT().FinishDraft(gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, d *interfaces.Draft) error {
					So(ctx.Err(), ShouldBeNil)
					finished = d
					return nil
				})

			_, err := svc.GenerateDraft(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Error, ShouldEqual, FailureTimeout)
		})

		Convey("A project without catalogs cannot be drafted\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").
				Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil)

			_, err := svc.GenerateDraft(ctx, token, "p1", caller, false)

			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_NoCatalogs)
		})

		Convey("A project that is drafting is not drafted twice\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(false, nil)

			_, err := svc.GenerateDraft(ctx, token, "p1", caller, false)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_Drafting)
		})

		Convey("A project the caller cannot see answers as the project service does\n", func() {
			notFound := httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Project_NotFound)
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(nil, notFound).Times(2)

			_, err := svc.GenerateDraft(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Project_NotFound)
			_, err = svc.GetDraft(ctx, token, "p1")
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Project_NotFound)
		})

		Convey("Get returns the draft, or not found before the first one\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).Times(2)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&interfaces.Draft{ProjectID: "p1", Status: "ready"}, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)

			draft, err := svc.GetDraft(ctx, token, "p1")
			So(err, ShouldBeNil)
			So(draft.Status, ShouldEqual, "ready")

			_, err = svc.GetDraft(ctx, token, "p1")
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
		})

		Convey("Drafts a previous run left drafting fail as interrupted\n", func() {
			da.EXPECT().FailDrafting(gomock.Any(), FailureInterrupted, gomock.Any()).Return(int64(1), nil)

			So(svc.FailInterrupted(ctx), ShouldBeNil)
		})
	})
}
