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

		bba := pmock.NewMockBknBackendAccess(mockCtrl)
		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &lineageService{gs: realGS, bba: bba, ra: ra}
		ctx := context.Background()
		const branch = "project/p1"

		id := newLineageID()
		networkID := id + "_v1_0_0"
		live := definitions("A part.", "A supplier.")
		_, err := realGS.InitLineage(ctx, id, live, "init", author)
		So(err, ShouldBeNil)
		_, err = svc.CreateBranch(ctx, id, branch, "")
		So(err, ShouldBeNil)
		branchHead, err := realGS.CommitFiles(ctx, id, branch, definitions("A part, edited.", "A supplier."), "edit", author)
		So(err, ShouldBeNil)
		notFound := &interfaces.BknBackendError{HTTPCode: http.StatusNotFound}

		Convey("A branch that can be published has no refusals and changes nothing\n", func() {
			mainHead, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: id, Limit: 1}).Return(nil, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(nil, notFound)
			// No tag, import, release row or move of main: the mocks expect none.

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(preview.Refusals, ShouldBeEmpty)
			So(preview.LineageID, ShouldEqual, id)
			So(preview.Branch, ShouldEqual, branch)
			So(preview.Version, ShouldEqual, "1.0.0")
			So(preview.Tag, ShouldEqual, "v1.0.0")
			So(preview.NetworkID, ShouldEqual, networkID)
			So(preview.NetworkName, ShouldEqual, "Supply chain · 1.0.0")
			So(preview.LiveNetworkID, ShouldEqual, id)
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

		Convey("Every refusal publishing would meet is listed, in the order publishing checks them\n", func() {
			// main moved after the branch started, and the live network was edited since.
			moved := definitions("A part.", "A supplier, moved.")
			_, err := realGS.CommitFiles(ctx, id, "main", moved, "other project", author)
			So(err, ShouldBeNil)
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{
				{NetworkID: id + "_v0_9_0", Version: "0.9.0"},
			}, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id+"_v0_9_0").Return(live, nil)
			// Someone already created a network with the release's id.
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(live, nil)

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(refusalCodes(preview), ShouldResemble, []string{
				perrors.BknProject_Release_BranchBehind,
				perrors.BknProject_Release_Drift,
				perrors.BknProject_Release_NetworkExists,
			})
			report, ok := preview.Refusals[1].Details.(*interfaces.DriftReport)
			So(ok, ShouldBeTrue)
			So(report.KNID, ShouldEqual, id+"_v0_9_0")
			So(report.Changed, ShouldResemble, []string{"object_types/supplier.bkn"})
			So(preview.LiveNetworkID, ShouldEqual, id+"_v0_9_0")
			So(preview.BaseVersion, ShouldEqual, "0.9.0")
			// The package still builds, so the name is known.
			So(preview.NetworkName, ShouldEqual, "Supply chain · 1.0.0")
		})

		Convey("A version a release recorded is refused, and its network is the release's own\n", func() {
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(
				&interfaces.Release{LineageID: id, NetworkID: networkID, Version: "1.0.0"}, true, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(refusalCodes(preview), ShouldResemble, []string{perrors.BknProject_Release_VersionExists})
			So(preview.Refusals[0].Details, ShouldResemble,
				map[string]any{"network_id": networkID, "lineage_id": id, "version": "1.0.0"})
		})

		Convey("A tag of the version at another commit is refused as an unfinished attempt\n", func() {
			mainHead, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(realGS.Tag(ctx, id, "v1.0.0", mainHead), ShouldBeNil)
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(nil, notFound)

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(refusalCodes(preview), ShouldResemble, []string{perrors.BknProject_Release_UnfinishedAttempt})
			So(preview.Refusals[0].Details, ShouldResemble, map[string]any{
				"tag": "v1.0.0", "commit": mainHead, "branch_commit": branchHead, "network_id": networkID,
			})
		})

		Convey("A tag a failed attempt left at the branch head is a retry: its network is not refused\n", func() {
			So(realGS.Tag(ctx, id, "v1.0.0", branchHead), ShouldBeNil)
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(preview.Refusals, ShouldBeEmpty)
		})

		Convey("Definitions that make no release package are refused, and nothing is planned for them\n", func() {
			broken := definitions("A part, edited.", "A supplier.")
			delete(broken, "network.bkn")
			_, err := realGS.CommitFiles(ctx, id, branch, broken, "drop network", author)
			So(err, ShouldBeNil)
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(nil, notFound)

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(refusalCodes(preview), ShouldResemble, []string{perrors.BknProject_Lineage_InvalidDefinitions})
			So(preview.NetworkName, ShouldBeEmpty)
			So(preview.Capabilities, ShouldBeEmpty)
			// The changes are the branch's against main.
			So(preview.Changes.Removed, ShouldResemble, []string{"network.bkn"})
			So(preview.Changes.Added, ShouldBeEmpty)
		})

		Convey("A request publishing would reject outright is an error, not a refusal\n", func() {
			_, err := svc.PreviewRelease(ctx, token, id, branch, "1.0")
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Release_InvalidVersion)

			_, err = svc.PreviewRelease(ctx, token, id, "main", "1.0.0")
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Lineage_MainBranchReadOnly)
		})

		Convey("A failure to read what publishing checks is an error\n", func() {
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))

			_, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
		})
	})
}
