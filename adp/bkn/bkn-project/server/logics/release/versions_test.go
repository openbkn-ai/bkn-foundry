// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_ProjectVersions(t *testing.T) {
	Convey("Test listing and copying the versions of a project's network\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockReleaseAccess(ctrl)
		kva := pmock.NewMockKNVersionAccess(ctrl)
		ls := pmock.NewMockLineageService(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		svc := &projectReleaseService{ps: ps, ra: ra, kva: kva, ls: ls, gs: gs}
		ctx := context.Background()
		author := interfaces.GitAuthor{Name: reviewOwner.ID}
		other := interfaces.AccountInfo{ID: "u2", Type: "user"}
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "supply", Branch: "project/p1", Owner: reviewOwner}}
		ps.EXPECT().GetProject(gomock.Any(), reviewToken, "p1").Return(project, nil).AnyTimes()

		published := interfaces.RELEASE_STATE_PUBLISHED
		prepared := interfaces.RELEASE_STATE_PREPARED
		r5 := &interfaces.Release{ID: "r5", LineageID: "supply", Version: "1.4.0", ProjectID: "p1", State: prepared, CreateTime: 5}
		r4 := &interfaces.Release{ID: "r4", LineageID: "supply", Version: "1.3.0", ProjectID: "p1", State: prepared, CreateTime: 4}
		r3 := &interfaces.Release{ID: "r3", LineageID: "supply", Version: "1.2.0", ProjectID: "p2", State: published, KNVersion: 3, CreateTime: 3}
		r2 := &interfaces.Release{ID: "r2", LineageID: "supply", Version: "1.1.0", ProjectID: "p1", State: published, KNVersion: 2, CreateTime: 2}
		r6 := &interfaces.Release{ID: "r6", LineageID: "supply", Version: "1.5.0", ProjectID: "p2", State: prepared, CreateTime: 6}
		releases := []*interfaces.Release{r6, r5, r4, r3, r2}
		ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "supply", Limit: interfaces.MAX_RELEASE_LIMIT}).
			DoAndReturn(func(context.Context, interfaces.ReleasesQuery) ([]*interfaces.Release, error) {
				return releases, nil
			}).AnyTimes()
		versioning := &interfaces.KNVersioning{Enabled: true, RunningVersion: 4, PreviousVersion: 3,
			Draft: interfaces.KNDraftState{BaseVersion: 4, Revision: 17}}
		v4 := &interfaces.KNVersion{Version: 4, Label: "hotfix", State: interfaces.KN_VERSION_STATE_RUNNING}
		v3 := &interfaces.KNVersion{Version: 3, Label: "v1.2.0", State: interfaces.KN_VERSION_STATE_HISTORY,
			Source: interfaces.KNVersionSource{Type: interfaces.KN_VERSION_SOURCE_TYPE, ProjectID: "p2", ReleaseID: "r3"}}
		v2 := &interfaces.KNVersion{Version: 2, Label: "v1.1.0", State: interfaces.KN_VERSION_STATE_HISTORY,
			Source: interfaces.KNVersionSource{Type: interfaces.KN_VERSION_SOURCE_TYPE, ProjectID: "p1", ReleaseID: "r2"}}
		v1 := &interfaces.KNVersion{Version: 1, State: interfaces.KN_VERSION_STATE_PURGED}

		Convey("Listing\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), reviewToken, "supply").Return(versioning, nil)

			Convey("each version is named by the release that published it and the project's newest prepared release waits\n", func() {
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{v4, v3, v2, v1}, nil)

				got, err := svc.ListVersions(ctx, reviewToken, "p1")

				So(err, ShouldBeNil)
				So(got.Versioning, ShouldResemble, *versioning)
				So(got.Prepared, ShouldEqual, r5)
				So(got.Entries, ShouldResemble, []interfaces.ProjectVersion{
					{KNVersion: *v4, Origin: interfaces.VERSION_ORIGIN_WORKSPACE},
					{KNVersion: *v3, Origin: interfaces.VERSION_ORIGIN_OTHER_PROJECT, Release: "1.2.0"},
					{KNVersion: *v2, Origin: interfaces.VERSION_ORIGIN_PROJECT, Release: "1.1.0"},
					{KNVersion: *v1, Origin: interfaces.VERSION_ORIGIN_WORKSPACE},
				})
			})

			Convey("without a prepared release of the project nothing waits, and no version is an empty list\n", func() {
				releases = []*interfaces.Release{r6, r3}
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{}, nil)

				got, err := svc.ListVersions(ctx, reviewToken, "p1")

				So(err, ShouldBeNil)
				So(got.Prepared, ShouldBeNil)
				So(got.Entries, ShouldNotBeNil)
				So(got.Entries, ShouldBeEmpty)
			})

			Convey("a failure to list the versions is an internal error\n", func() {
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return(nil, errors.New("timeout"))

				_, err := svc.ListVersions(ctx, reviewToken, "p1")

				status, _, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusInternalServerError)
			})
		})

		Convey("Listing a network that is not versioned is bkn-project's refusal\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), reviewToken, "supply").Return(nil, &interfaces.BknBackendError{
				HTTPCode: http.StatusConflict, BaseError: httperrors.BaseError{ErrorCode: interfaces.KN_VERSION_ERROR_NOT_VERSIONED}})

			_, err := svc.ListVersions(ctx, reviewToken, "p1")

			_, code, _ := errorOf(err)
			So(code, ShouldEqual, perrors.BknProject_Release_NotVersioned)
		})

		Convey("Copying\n", func() {
			copied := &interfaces.CommitDefinitionsResult{Commit: "b9"}
			files := map[string][]byte{"object_types/lot.bkn": objectTypeFile("lot", "Lot")}
			kva.EXPECT().GetVersioning(gomock.Any(), reviewToken, "supply").
				DoAndReturn(func(context.Context, string, string) (*interfaces.KNVersioning, error) {
					return versioning, nil
				}).AnyTimes()

			Convey("a version the network keeps is copied into the draft, then the draft into the branch\n", func() {
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{v3}, nil)
				gomock.InOrder(
					kva.EXPECT().ResetDraft(gomock.Any(), reviewToken, "supply", 3, int64(17), true).Return(nil),
					kva.EXPECT().ExportVersion(gomock.Any(), reviewToken, "supply", interfaces.KN_VERSION_DRAFT).Return(files, nil),
					ls.EXPECT().CommitDefinitions(gomock.Any(), "supply", "project/p1", files, "copy v1.2.0 as draft", author).
						Return(copied, nil),
				)

				got, err := svc.CopyVersion(ctx, reviewToken, "p1", 3, interfaces.ProjectCopyVersion{OverwriteDraftChanges: true},
					reviewOwner, author)

				So(err, ShouldBeNil)
				So(got, ShouldEqual, copied)
			})

			Convey("the full history keeps every version not purged\n", func() {
				versioning.FullHistory = true
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{v2}, nil)
				kva.EXPECT().ResetDraft(gomock.Any(), reviewToken, "supply", 2, int64(17), false).Return(nil)
				kva.EXPECT().ExportVersion(gomock.Any(), reviewToken, "supply", interfaces.KN_VERSION_DRAFT).Return(files, nil)
				ls.EXPECT().CommitDefinitions(gomock.Any(), "supply", "project/p1", files, "copy v1.1.0 as draft", author).Return(copied, nil)

				_, err := svc.CopyVersion(ctx, reviewToken, "p1", 2, interfaces.ProjectCopyVersion{}, reviewOwner, author)

				So(err, ShouldBeNil)
			})

			Convey("a version without a label is named by its number\n", func() {
				unlabelled := &interfaces.KNVersion{Version: 4, State: interfaces.KN_VERSION_STATE_RUNNING}
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{unlabelled}, nil)
				kva.EXPECT().ResetDraft(gomock.Any(), reviewToken, "supply", 4, int64(17), false).Return(nil)
				kva.EXPECT().ExportVersion(gomock.Any(), reviewToken, "supply", interfaces.KN_VERSION_DRAFT).Return(files, nil)
				ls.EXPECT().CommitDefinitions(gomock.Any(), "supply", "project/p1", files, "copy version 4 as draft", author).Return(copied, nil)

				_, err := svc.CopyVersion(ctx, reviewToken, "p1", 4, interfaces.ProjectCopyVersion{}, reviewOwner, author)

				So(err, ShouldBeNil)
			})

			Convey("a version the community edition no longer keeps is read from its release's tag, leaving the draft\n", func() {
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{v2}, nil)
				gs.EXPECT().ReadTree(gomock.Any(), "supply", "refs/tags/v1.1.0").Return(files, nil)
				ls.EXPECT().CommitDefinitions(gomock.Any(), "supply", "project/p1", files, "copy v1.1.0 as draft", author).Return(copied, nil)

				got, err := svc.CopyVersion(ctx, reviewToken, "p1", 2, interfaces.ProjectCopyVersion{}, reviewOwner, author)

				So(err, ShouldBeNil)
				So(got, ShouldEqual, copied)
			})

			Convey("a purged version is read from its release's tag\n", func() {
				versioning.FullHistory = true
				purged := &interfaces.KNVersion{Version: 2, Label: "v1.1.0", State: interfaces.KN_VERSION_STATE_PURGED,
					Source: interfaces.KNVersionSource{ReleaseID: "r2"}}
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{purged}, nil)
				gs.EXPECT().ReadTree(gomock.Any(), "supply", "refs/tags/v1.1.0").Return(files, nil)
				ls.EXPECT().CommitDefinitions(gomock.Any(), "supply", "project/p1", files, "copy v1.1.0 as draft", author).Return(copied, nil)

				_, err := svc.CopyVersion(ctx, reviewToken, "p1", 2, interfaces.ProjectCopyVersion{}, reviewOwner, author)

				So(err, ShouldBeNil)
			})

			Convey("a version the network has no record of is not found\n", func() {
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{v1}, nil)

				_, err := svc.CopyVersion(ctx, reviewToken, "p1", 9, interfaces.ProjectCopyVersion{}, reviewOwner, author)

				status, code, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusNotFound)
				So(code, ShouldEqual, perrors.BknProject_Release_NotFound)
			})

			Convey("a purged version no release published is gone\n", func() {
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{v1}, nil)

				_, err := svc.CopyVersion(ctx, reviewToken, "p1", 1, interfaces.ProjectCopyVersion{}, reviewOwner, author)

				status, code, details := errorOf(err)
				So(status, ShouldEqual, http.StatusGone)
				So(code, ShouldEqual, perrors.BknProject_Release_VersionPurged)
				So(details, ShouldResemble, map[string]any{"kn_version": 1})
			})

			Convey("edits in the draft not published are not dropped without confirming\n", func() {
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{v3}, nil)
				kva.EXPECT().ResetDraft(gomock.Any(), reviewToken, "supply", 3, int64(17), false).Return(&interfaces.BknBackendError{
					HTTPCode: http.StatusConflict, BaseError: httperrors.BaseError{ErrorCode: interfaces.KN_VERSION_ERROR_DRAFT_HAS_CHANGES}})

				_, err := svc.CopyVersion(ctx, reviewToken, "p1", 3, interfaces.ProjectCopyVersion{}, reviewOwner, author)

				status, code, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusConflict)
				So(code, ShouldEqual, perrors.BknProject_Release_DraftHasChanges)
			})

			Convey("a refused commit to the branch is passed on\n", func() {
				conflict := httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_MergeConflict)
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{v2}, nil)
				gs.EXPECT().ReadTree(gomock.Any(), "supply", "refs/tags/v1.1.0").Return(files, nil)
				ls.EXPECT().CommitDefinitions(gomock.Any(), "supply", "project/p1", files, gomock.Any(), author).Return(nil, conflict)

				_, err := svc.CopyVersion(ctx, reviewToken, "p1", 2, interfaces.ProjectCopyVersion{}, reviewOwner, author)

				So(err, ShouldEqual, conflict)
			})

			Convey("a tag that cannot be read is an internal error\n", func() {
				kva.EXPECT().ListVersions(gomock.Any(), reviewToken, "supply").Return([]*interfaces.KNVersion{v2}, nil)
				gs.EXPECT().ReadTree(gomock.Any(), "supply", "refs/tags/v1.1.0").Return(nil, errors.New("tag not found"))

				_, err := svc.CopyVersion(ctx, reviewToken, "p1", 2, interfaces.ProjectCopyVersion{}, reviewOwner, author)

				status, _, _ := errorOf(err)
				So(status, ShouldEqual, http.StatusInternalServerError)
			})
		})

		Convey("Only the owner of an unarchived project copies a version\n", func() {
			_, err := svc.CopyVersion(ctx, reviewToken, "p1", 3, interfaces.ProjectCopyVersion{}, other, author)
			status, code, _ := errorOf(err)
			So(status, ShouldEqual, http.StatusForbidden)
			So(code, ShouldEqual, perrors.BknProject_Release_NotOwner)

			project.Archived = true
			_, err = svc.CopyVersion(ctx, reviewToken, "p1", 3, interfaces.ProjectCopyVersion{}, reviewOwner, author)
			_, code, _ = errorOf(err)
			So(code, ShouldEqual, perrors.BknProject_Release_ProjectArchived)
			project.Archived = false
		})
	})
}
