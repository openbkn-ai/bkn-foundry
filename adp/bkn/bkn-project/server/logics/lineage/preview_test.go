// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func refusalCodes(preview *interfaces.ReleasePreview) []string {
	codes := []string{}
	for _, r := range preview.Refusals {
		codes = append(codes, r.Code)
	}
	return codes
}

func Test_LineageService_PreviewRelease(t *testing.T) {
	Convey("Test lineageService PreviewRelease on a real git store\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		kva := pmock.NewMockKNVersionAccess(mockCtrl)
		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &lineageService{gs: realGS, kva: kva, ra: ra}
		ctx := context.Background()
		const branch = "project/p1"

		id := newLineageID()
		live := definitions("A part.", "A supplier.")
		_, err := realGS.InitLineage(ctx, id, live, "init", author)
		So(err, ShouldBeNil)
		_, err = svc.CreateBranch(ctx, id, branch, "")
		So(err, ShouldBeNil)
		branchHead, err := realGS.CommitFiles(ctx, id, branch, definitions("A part, edited.", "A supplier."), "edit", author)
		So(err, ShouldBeNil)

		Convey("A branch that can be prepared has no refusals and changes nothing\n", func() {
			mainHead, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)
			// No draft write, release row, tag or move of main: the mocks expect none.

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(preview.Refusals, ShouldBeEmpty)
			So(preview.LineageID, ShouldEqual, id)
			So(preview.Branch, ShouldEqual, branch)
			So(preview.Version, ShouldEqual, "1.0.0")
			So(preview.Tag, ShouldEqual, "v1.0.0")
			// Every release goes into the lineage's one versioned network.
			So(preview.NetworkID, ShouldEqual, id)
			So(preview.LiveNetworkID, ShouldEqual, id)
			So(preview.NetworkName, ShouldEqual, "Supply chain")
			So(preview.BaseVersion, ShouldBeEmpty)
			So(preview.MainCommit, ShouldEqual, mainHead)
			So(preview.BranchCommit, ShouldEqual, branchHead)
			So(preview.Changes.Changed, ShouldResemble, []string{"object_types/part.bkn"})
			So(preview.Changes.Added, ShouldBeEmpty)
			So(preview.Changes.Removed, ShouldBeEmpty)
			So(preview.Capabilities, ShouldNotBeNil)
			So(preview.Capabilities, ShouldBeEmpty)

			_, err = realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(errors.Is(err, plumbing.ErrReferenceNotFound), ShouldBeTrue)
			after, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(after, ShouldEqual, mainHead)
		})

		Convey("Every refusal preparing would meet is listed, in the order preparing checks them\n", func() {
			// main moved after the branch started, and the draft was edited since main was synced.
			moved := definitions("A part.", "A supplier, moved.")
			_, err := realGS.CommitFiles(ctx, id, "main", moved, "other project", author)
			So(err, ShouldBeNil)
			versioning := versioningAt(1, 7, true)
			versioning.Enabled = false
			expectChecks(kva, ra, id, "1.0.0", versioning,
				&interfaces.Release{LineageID: id, Version: "1.0.0", State: interfaces.RELEASE_STATE_PUBLISHED},
				[]*interfaces.Release{{LineageID: id, NetworkID: id, Version: "0.9.0", State: interfaces.RELEASE_STATE_PUBLISHED}})
			ra.EXPECT().ListReleases(gomock.Any(), draftWritersQuery(id)).Return(nil, nil)
			kva.EXPECT().ExportVersion(gomock.Any(), token, id, interfaces.KN_VERSION_DRAFT).Return(live, nil)

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(refusalCodes(preview), ShouldResemble, []string{
				perrors.BknProject_Release_NotVersioned,
				perrors.BknProject_Release_VersionExists,
				perrors.BknProject_Release_BranchBehind,
				perrors.BknProject_Release_Drift,
			})
			So(preview.Refusals[1].Details, ShouldResemble, map[string]any{"lineage_id": id, "version": "1.0.0"})
			report, ok := preview.Refusals[3].Details.(*interfaces.DriftReport)
			So(ok, ShouldBeTrue)
			So(report.KNID, ShouldEqual, id)
			So(report.Changed, ShouldResemble, []string{"object_types/supplier.bkn"})
			So(preview.LiveNetworkID, ShouldEqual, id)
			So(preview.BaseVersion, ShouldEqual, "0.9.0")
			// The package still builds, so the name is known.
			So(preview.NetworkName, ShouldEqual, "Supply chain")
		})

		Convey("A version prepared and not published is no refusal: preparing it again replaces it\n", func() {
			prepared := &interfaces.Release{ID: "r1", LineageID: id, Version: "1.0.0", State: interfaces.RELEASE_STATE_PREPARED, DraftRevision: 7}
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, true), prepared, nil)
			// What the draft holds is what preparing wrote, so it is not compared with main.
			ra.EXPECT().ListReleases(gomock.Any(), draftWritersQuery(id)).Return([]*interfaces.Release{prepared}, nil)

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(preview.Refusals, ShouldBeEmpty)
		})

		Convey("Definitions that make no release package are refused, and nothing is planned for them\n", func() {
			broken := definitions("A part, edited.", "A supplier.")
			delete(broken, "network.bkn")
			_, err := realGS.CommitFiles(ctx, id, branch, broken, "drop network", author)
			So(err, ShouldBeNil)
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(refusalCodes(preview), ShouldResemble, []string{perrors.BknProject_Lineage_InvalidDefinitions})
			So(preview.NetworkName, ShouldBeEmpty)
			So(preview.Capabilities, ShouldBeEmpty)
			// The changes are the branch's against main.
			So(preview.Changes.Removed, ShouldResemble, []string{"network.bkn"})
			So(preview.Changes.Added, ShouldBeEmpty)
		})

		Convey("A request preparing would reject outright is an error, not a refusal\n", func() {
			_, err := svc.PreviewRelease(ctx, token, id, branch, "1.0")
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Release_InvalidVersion)

			_, err = svc.PreviewRelease(ctx, token, id, "main", "1.0.0")
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Lineage_MainBranchReadOnly)
		})

		Convey("A failure to read what preparing checks is an error\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(versioningAt(1, 7, false), nil)
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), publishedQuery(id)).Return(nil, errors.New("db down"))

			_, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
		})

		Convey("A version refusal of bkn-backend while reading is worded as bkn-project's\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(nil, versionRefusal(interfaces.KN_VERSION_ERROR_NOT_VERSIONED))

			_, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_NotVersioned)
		})
	})
}
