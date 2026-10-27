// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

var publisher = interfaces.AccountInfo{ID: "u1", Type: "user"}

func errorDetails(err error) any {
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		return nil
	}
	return httpErr.ErrorDetails
}

func copyFiles(files map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for p, data := range files {
		out[p] = data
	}
	return out
}

func Test_LineageService_PublishRelease(t *testing.T) {
	Convey("Test lineageService PublishRelease on a real git store\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		bba := pmock.NewMockBknBackendAccess(mockCtrl)
		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &lineageService{gs: realGS, bba: bba, ra: ra}
		ctx := context.Background()
		const branch = "project/p1"

		// setUp starts a lineage whose live network matches main and a branch with one edit.
		setUp := func(id string) (live map[string][]byte, branchHead string) {
			live = definitions("A part.", "A supplier.")
			_, err := realGS.InitLineage(ctx, id, live, "init", author)
			So(err, ShouldBeNil)
			_, err = svc.CreateBranch(ctx, id, branch, "")
			So(err, ShouldBeNil)
			branchHead, err = realGS.CommitFiles(ctx, id, branch, definitions("A part, edited.", "A supplier."), "edit", author)
			So(err, ShouldBeNil)
			return live, branchHead
		}
		request := interfaces.PublishReleaseRequest{Branch: branch, Version: "1.0.0", ProjectID: "p1", Notes: "first"}

		Convey("A release tags the branch, imports it as a new network and records it\n", func() {
			id := newLineageID()
			live, branchHead := setUp(id)
			networkID := id + "_v1_0_0"
			var imported map[string][]byte
			var recorded *interfaces.Release

			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: id, Limit: 1}).Return([]*interfaces.Release{}, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)
			// The target network does not exist yet.
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusNotFound})
			bba.EXPECT().ImportBKN(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, files map[string][]byte) (*interfaces.BKNImportResult, error) {
					imported = files
					return &interfaces.BKNImportResult{KNID: networkID, Capabilities: &interfaces.CapabilityImportReport{}}, nil
				})
			ra.EXPECT().CreateRelease(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, release *interfaces.Release) error {
					recorded = release
					return nil
				})
			// The import re-serializes network.bkn; the export brings that back.
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).DoAndReturn(
				func(context.Context, string, string) (map[string][]byte, error) {
					exported := copyFiles(imported)
					exported["network.bkn"] = append(exported["network.bkn"], []byte("\nRe-serialized.\n")...)
					return exported, nil
				})

			result, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			So(err, ShouldBeNil)
			So(result.NetworkID, ShouldEqual, networkID)
			So(result.Tag, ShouldEqual, "v1.0.0")
			So(result.Release, ShouldEqual, recorded)
			So(recorded.LineageID, ShouldEqual, id)
			So(recorded.NetworkID, ShouldEqual, networkID)
			So(recorded.Version, ShouldEqual, "1.0.0")
			So(recorded.BaseVersion, ShouldBeEmpty)
			So(recorded.ProjectID, ShouldEqual, "p1")
			So(recorded.Publisher, ShouldResemble, publisher)
			So(recorded.Notes, ShouldEqual, "first")
			So(recorded.State, ShouldEqual, interfaces.RELEASE_STATE_LIVE)
			So(recorded.ID, ShouldNotBeEmpty)
			So(recorded.CreateTime, ShouldBeGreaterThan, 0)

			tagHead, err := realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(err, ShouldBeNil)
			So(tagHead, ShouldEqual, branchHead)

			// The package is the tag with only the network id and name changed.
			network, err := bkn.ParseNetworkFile(string(imported["network.bkn"]), "network.bkn")
			So(err, ShouldBeNil)
			So(network.ID, ShouldEqual, networkID)
			So(network.Name, ShouldEqual, "Supply chain · 1.0.0")
			So(string(imported["network.bkn"]), ShouldEqual, strings.Replace(strings.Replace(networkText,
				"id: supply", "id: "+networkID, 1), "name: Supply chain", "name: Supply chain · 1.0.0", 1))
			tagged, _ := realGS.ReadTree(ctx, id, "refs/tags/v1.0.0")
			So(imported["object_types/part.bkn"], ShouldResemble, tagged["object_types/part.bkn"])
			So(imported, ShouldHaveLength, len(tagged))

			// main moved past the tag to the commit of the re-serialized export.
			mainHead, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(result.Commit, ShouldEqual, mainHead)
			repo, err := git.PlainOpen(filepath.Join(gitRoot, id+".git"))
			So(err, ShouldBeNil)
			commit, err := repo.CommitObject(plumbing.NewHash(mainHead))
			So(err, ShouldBeNil)
			So(commit.ParentHashes, ShouldResemble, []plumbing.Hash{plumbing.NewHash(branchHead)})
			So(commit.Message, ShouldEqual, "sync live definitions after release v1.0.0")
			onMain, _ := realGS.ReadTree(ctx, id, "main")
			So(string(onMain["network.bkn"]), ShouldEndWith, "Re-serialized.\n")
		})

		Convey("A release after another one checks drift against it and builds on its version\n", func() {
			id := newLineageID()
			live, _ := setUp(id)
			previous := &interfaces.Release{NetworkID: id + "_v1_0_0", Version: "1.0.0"}
			networkID := id + "_v1_1_0"

			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{previous}, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, previous.NetworkID).Return(live, nil)
			// The target network does not exist yet.
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusNotFound})
			bba.EXPECT().ImportBKN(gomock.Any(), token, gomock.Any()).Return(&interfaces.BKNImportResult{KNID: networkID}, nil)
			ra.EXPECT().CreateRelease(gomock.Any(), gomock.Any()).Return(nil)
			// A failed export after the release leaves main at the tag; the release stands.
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusBadGateway})

			req := request
			req.Version = "1.1.0"
			result, err := svc.PublishRelease(ctx, token, id, req, publisher, author)

			So(err, ShouldBeNil)
			So(result.Release.BaseVersion, ShouldEqual, "1.0.0")
			tagHead, _ := realGS.ResolveRef(ctx, id, "refs/tags/v1.1.0")
			So(result.Commit, ShouldEqual, tagHead)
		})

		Convey("A branch without the head of main replies 409 and tags nothing\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			_, err := realGS.CommitFiles(ctx, id, "main", definitions("A part.", "A supplier, edited live."), "drift", author)
			So(err, ShouldBeNil)
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), gomock.Any()).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)

			_, err = svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_BranchBehind)
			_, err = realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(errors.Is(err, plumbing.ErrReferenceNotFound), ShouldBeTrue)
		})

		Convey("Drift on the live network replies 409 with the paths and tags nothing\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), gomock.Any()).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(definitions("A part.", "A supplier, edited in Studio."), nil)

			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_Drift)
			report := errorDetails(err).(*interfaces.DriftReport)
			So(report.KNID, ShouldEqual, id)
			So(report.Changed, ShouldResemble, []string{"object_types/supplier.bkn"})
			So(report.Added, ShouldBeEmpty)
			So(report.Removed, ShouldBeEmpty)
			_, err = realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(errors.Is(err, plumbing.ErrReferenceNotFound), ShouldBeTrue)
		})

		Convey("Skipped capabilities fail the release without recording it\n", func() {
			id := newLineageID()
			live, branchHead := setUp(id)
			networkID := id + "_v1_0_0"
			mainBefore, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			skipped := []*interfaces.CapabilitySkip{{CapabilityType: "skill", Name: "planner", Reason: "no_execute_permission"}}

			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)
			// The target network does not exist yet.
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusNotFound})
			bba.EXPECT().ImportBKN(gomock.Any(), token, gomock.Any()).Return(&interfaces.BKNImportResult{
				KNID: networkID, Capabilities: &interfaces.CapabilityImportReport{Skipped: skipped}}, nil)

			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_CapabilitiesSkipped)
			So(errorDetails(err), ShouldResemble, map[string]any{"network_id": networkID, "tag": "v1.0.0", "skipped": skipped})
			mainAfter, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(mainAfter, ShouldEqual, mainBefore)
			tagHead, _ := realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(tagHead, ShouldEqual, branchHead)

			Convey("and the same version from the same branch head publishes on retry\n", func() {
				ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
				ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
				bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)
				bba.EXPECT().ImportBKN(gomock.Any(), token, gomock.Any()).Return(&interfaces.BKNImportResult{KNID: networkID}, nil)
				ra.EXPECT().CreateRelease(gomock.Any(), gomock.Any()).Return(nil)
				bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(definitions("A part, edited.", "A supplier."), nil)

				result, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

				So(err, ShouldBeNil)
				So(result.Tag, ShouldEqual, "v1.0.0")
				tagAgain, _ := realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
				So(tagAgain, ShouldEqual, branchHead)
			})

			Convey("and the same version from a moved branch replies 409\n", func() {
				_, err := realGS.CommitFiles(ctx, id, branch, definitions("A part, edited again.", "A supplier."), "edit", author)
				So(err, ShouldBeNil)
				ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)

				_, err = svc.PublishRelease(ctx, token, id, request, publisher, author)

				status, code := httpCode(err)
				So(status, ShouldEqual, http.StatusConflict)
				So(code, ShouldEqual, perrors.BknProject_Release_VersionExists)
			})
		})

		Convey("A network that already exists but no release created replies 409 and is not overwritten\n", func() {
			id := newLineageID()
			live, _ := setUp(id)
			networkID := id + "_v1_0_0"
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(live, nil)

			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_NetworkExists)
			_, err = realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(errors.Is(err, plumbing.ErrReferenceNotFound), ShouldBeTrue)
		})

		Convey("A version already released replies 409\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), id+"_v1_0_0").
				Return(&interfaces.Release{LineageID: id, Version: "1.0.0"}, true, nil)

			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_VersionExists)
		})

		Convey("A 38-character lineage id is cut so the network id fits 40 characters\n", func() {
			seq++
			id := fmt.Sprintf("%s_%04d", strings.Repeat("s", 33), seq)
			So(id, ShouldHaveLength, 38)
			live, _ := setUp(id)
			networkID := strings.Repeat("s", 33) + "_v1_0_0"
			var imported map[string][]byte

			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)
			// The target network does not exist yet.
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusNotFound})
			bba.EXPECT().ImportBKN(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, files map[string][]byte) (*interfaces.BKNImportResult, error) {
					imported = files
					return &interfaces.BKNImportResult{KNID: networkID}, nil
				})
			ra.EXPECT().CreateRelease(gomock.Any(), gomock.Any()).Return(nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(live, nil)

			result, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			So(err, ShouldBeNil)
			So(result.NetworkID, ShouldEqual, networkID)
			network, err := bkn.ParseNetworkFile(string(imported["network.bkn"]), "network.bkn")
			So(err, ShouldBeNil)
			So(network.ID, ShouldEqual, networkID)
		})

		Convey("Bad versions and branches are refused before anything is read\n", func() {
			for _, version := range []string{"", "1.0", "v1.0.0", "01.0.0", "1.0.0-rc1", "1.0.0.0", strings.Repeat("1", 31) + ".0.0"} {
				req := request
				req.Version = version
				_, err := svc.PublishRelease(ctx, token, "orders", req, publisher, author)
				status, code := httpCode(err)
				So(status, ShouldEqual, http.StatusBadRequest)
				So(code, ShouldEqual, perrors.BknProject_Release_InvalidVersion)
			}

			req := request
			req.Branch = "main"
			_, err := svc.PublishRelease(ctx, token, "orders", req, publisher, author)
			_, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_MainBranchReadOnly)
		})

		Convey("A project id or notes the release table cannot hold are refused before anything is read\n", func() {
			req := request
			req.ProjectID = strings.Repeat("项", 41)
			_, err := svc.PublishRelease(ctx, token, "orders", req, publisher, author)
			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Release_InvalidProjectID)

			req = request
			req.Notes = strings.Repeat("n", 65536)
			_, err = svc.PublishRelease(ctx, token, "orders", req, publisher, author)
			status, code = httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Release_InvalidNotes)

			Convey("while the longest values that fit pass\n", func() {
				id := newLineageID()
				_, _ = setUp(id)
				ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), id+"_v1_0_0").
					Return(&interfaces.Release{LineageID: id, Version: "1.0.0"}, true, nil)
				req := request
				req.ProjectID = strings.Repeat("项", 40)
				req.Notes = strings.Repeat("n", 65535)

				_, err := svc.PublishRelease(ctx, token, id, req, publisher, author)

				_, code := httpCode(err)
				So(code, ShouldEqual, perrors.BknProject_Release_VersionExists)
			})
		})

		Convey("Definitions the release cannot rename reply 400 and tag nothing, so a fixed branch publishes the same version\n", func() {
			id := newLineageID()
			live, _ := setUp(id)
			networkID := id + "_v1_0_0"
			// A block name loads but cannot be swapped line for line.
			broken := definitions("A part, edited.", "A supplier.")
			broken["network.bkn"] = []byte(strings.Replace(networkText, "name: Supply chain", "name: |\n  Supply chain", 1))
			_, err := realGS.CommitFiles(ctx, id, branch, broken, "block name", author)
			So(err, ShouldBeNil)

			// The reads may run in any order; the network exists only once imported.
			var imported map[string][]byte
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil).AnyTimes()
			ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil).AnyTimes()
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).DoAndReturn(
				func(context.Context, string, string) (map[string][]byte, error) {
					if imported == nil {
						return nil, &interfaces.BknBackendError{HTTPCode: http.StatusNotFound}
					}
					return imported, nil
				}).AnyTimes()

			_, err = svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidDefinitions)
			_, err = realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(errors.Is(err, plumbing.ErrReferenceNotFound), ShouldBeTrue)

			fixedHead, err := realGS.CommitFiles(ctx, id, branch, definitions("A part, edited.", "A supplier."), "fix name", author)
			So(err, ShouldBeNil)
			bba.EXPECT().ImportBKN(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, files map[string][]byte) (*interfaces.BKNImportResult, error) {
					imported = files
					return &interfaces.BKNImportResult{KNID: networkID}, nil
				})
			ra.EXPECT().CreateRelease(gomock.Any(), gomock.Any()).Return(nil)

			result, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			So(err, ShouldBeNil)
			So(result.Tag, ShouldEqual, "v1.0.0")
			tagHead, _ := realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(tagHead, ShouldEqual, fixedHead)
		})

		Convey("An unknown lineage replies 404 before anything is read\n", func() {
			_, err := svc.PublishRelease(ctx, token, "missing", request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusNotFound)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NotFound)
		})
	})
}

func Test_ReleaseNetworkID(t *testing.T) {
	Convey("Test releaseNetworkID\n", t, func() {
		So(releaseNetworkID("supply_ontology_hand", "1.1.0"), ShouldEqual, "supply_ontology_hand_v1_1_0")

		Convey("A long lineage id is cut and loses the separator the cut leaves\n", func() {
			lineageID := strings.Repeat("a", 31) + "_bcdef"
			So(lineageID, ShouldHaveLength, 37)
			id := releaseNetworkID(lineageID, "1.10.0")
			So(id, ShouldEqual, strings.Repeat("a", 31)+"_v1_10_0")
			So(len(id), ShouldBeLessThanOrEqualTo, maxNetworkIDLength)
		})

		Convey("The longest version still leaves part of the lineage id\n", func() {
			id := releaseNetworkID(strings.Repeat("b", 40), strings.Repeat("9", 28)+".0.0")
			So(id, ShouldHaveLength, maxNetworkIDLength)
			So(id, ShouldStartWith, "bbbbbb_v")
		})
	})
}

func Test_ReleaseNetworkName(t *testing.T) {
	Convey("Test releaseNetworkName\n", t, func() {
		So(releaseNetworkName("Supply chain", "1.1.0"), ShouldEqual, "Supply chain · 1.1.0")

		Convey("The version of an earlier release is replaced, not appended to\n", func() {
			So(releaseNetworkName("Supply chain · 1.0.0", "1.1.0"), ShouldEqual, "Supply chain · 1.1.0")
		})

		Convey("A long base name is cut by characters to fit 40\n", func() {
			name := releaseNetworkName(strings.Repeat("供应链", 20), "1.10.0")
			So(utf8.RuneCountInString(name), ShouldEqual, maxNetworkNameRunes)
			So(name, ShouldEndWith, " · 1.10.0")
			So(utf8.ValidString(name), ShouldBeTrue)

			cut := releaseNetworkName(strings.Repeat("x", 31)+" tail", "1.0.0")
			So(cut, ShouldEqual, strings.Repeat("x", 31)+" · 1.0.0")
		})
	})
}

func Test_SetNetworkHeader(t *testing.T) {
	Convey("Test setNetworkHeader\n", t, func() {
		Convey("Only the id and name lines change\n", func() {
			text := "---\ntype: knowledge_network\nid: supply\nname: Supply\ntags: [a]\n---\n\n# Supply\n\nid: in the body\n"
			out, err := setNetworkHeader(text, "supply_v1_0_0", "Supply · 1.0.0")
			So(err, ShouldBeNil)
			So(out, ShouldEqual, "---\ntype: knowledge_network\nid: supply_v1_0_0\nname: Supply · 1.0.0\ntags: [a]\n---\n\n# Supply\n\nid: in the body\n")
		})

		Convey("A name YAML would misread is quoted, and CRLF lines keep their ending\n", func() {
			text := "---\r\ntype: knowledge_network\r\nid: supply\r\nname: \"Supply\"\r\n---\r\n"
			out, err := setNetworkHeader(text, "supply_v1_0_0", "Supply: core · 1.0.0")
			So(err, ShouldBeNil)
			fm, err := bkn.ParseFrontmatter(out)
			So(err, ShouldBeNil)
			So(fm["name"], ShouldEqual, "Supply: core · 1.0.0")
			So(fm["id"], ShouldEqual, "supply_v1_0_0")
			So(out, ShouldContainSubstring, "\r\n---\r\n")
		})

		Convey("A frontmatter without name, or with a block name, is refused\n", func() {
			_, err := setNetworkHeader("---\ntype: knowledge_network\nid: supply\n---\n", "x_v1_0_0", "X · 1.0.0")
			So(err, ShouldNotBeNil)
			_, err = setNetworkHeader("---\nid: supply\nname: |\n  Supply\n---\n", "x_v1_0_0", "X · 1.0.0")
			So(err, ShouldNotBeNil)
			_, err = setNetworkHeader("# no frontmatter\n", "x_v1_0_0", "X · 1.0.0")
			So(err, ShouldNotBeNil)
		})
	})
}
