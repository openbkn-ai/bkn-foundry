// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

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

// versioningAt is a versioned network whose running version is running and whose draft is at
// revision, with or without edits not published.
func versioningAt(running int, revision int64, changes bool) *interfaces.KNVersioning {
	return &interfaces.KNVersioning{Enabled: true, RunningVersion: running, PreviousVersion: max(running-1, 0),
		Draft: interfaces.KNDraftState{BaseVersion: running, HasChanges: changes, Revision: revision}}
}

func publishedQuery(id string) interfaces.ReleasesQuery {
	return interfaces.ReleasesQuery{LineageID: id, State: interfaces.RELEASE_STATE_PUBLISHED, Limit: 1}
}

// draftWritersQuery lists every release of the lineage, to find the one that wrote the draft.
func draftWritersQuery(id string) interfaces.ReleasesQuery {
	return interfaces.ReleasesQuery{LineageID: id, Limit: interfaces.MAX_RELEASE_LIMIT}
}

// versionRefusal is a refusal of the bkn-backend version API.
func versionRefusal(code string) error {
	return &interfaces.BknBackendError{HTTPCode: http.StatusConflict, BaseError: httperrors.BaseError{ErrorCode: code}}
}

// expectChecks expects the reads the checks of a release make up to the drift check: the
// network's versioning, the release of the version, if any, and the latest published release.
func expectChecks(kva *pmock.MockKNVersionAccess, ra *pmock.MockReleaseAccess, id string, version string,
	versioning *interfaces.KNVersioning, existing *interfaces.Release, latest []*interfaces.Release) {
	kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(versioning, nil)
	ra.EXPECT().GetRelease(gomock.Any(), id, version).Return(existing, existing != nil, nil)
	ra.EXPECT().ListReleases(gomock.Any(), publishedQuery(id)).Return(latest, nil)
}

func Test_LineageService_PrepareRelease(t *testing.T) {
	Convey("Test lineageService PrepareRelease on a real git store\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		kva := pmock.NewMockKNVersionAccess(mockCtrl)
		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &lineageService{gs: realGS, kva: kva, ra: ra}
		ctx := context.Background()
		const branch = "project/p1"

		// setUp starts a lineage whose network matches main and a branch with one edit.
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
		// expectWrite expects the draft written with req, replying the revision the write made, its
		// concept index built, and the release recorded; it returns where the written package and
		// the recorded release land.
		expectWrite := func(id string, req interfaces.KNReplaceImport, revision int64) (*map[string][]byte, **interfaces.Release) {
			var written map[string][]byte
			var recorded *interfaces.Release
			replaced := kva.EXPECT().ReplaceDraft(gomock.Any(), token, gomock.Any(), req).DoAndReturn(
				func(_ context.Context, _ string, files map[string][]byte, _ interfaces.KNReplaceImport) (*interfaces.BKNImportResult, int64, error) {
					written = files
					return &interfaces.BKNImportResult{KNID: id, Capabilities: &interfaces.CapabilityImportReport{}}, revision, nil
				})
			// No versioning read after the write: the reply names the revision.
			indexed := kva.EXPECT().BuildDraftIndex(gomock.Any(), token, id).After(replaced).Return(nil)
			ra.EXPECT().PrepareRelease(gomock.Any(), gomock.Any()).After(indexed).DoAndReturn(
				func(_ context.Context, release *interfaces.Release) (bool, error) {
					recorded = release
					return true, nil
				})
			return &written, &recorded
		}

		Convey("Preparing writes the branch into the draft at the revision read and records a prepared release\n", func() {
			id := newLineageID()
			_, branchHead := setUp(id)
			mainBefore, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)
			// A draft without edits of its own is overwritten without asking.
			written, recorded := expectWrite(id, interfaces.KNReplaceImport{ExpectedDraftRevision: 7, Overwrite: true}, 8)

			release, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			So(err, ShouldBeNil)
			So(release, ShouldEqual, *recorded)
			So(release.ID, ShouldNotBeEmpty)
			So(release.LineageID, ShouldEqual, id)
			So(release.NetworkID, ShouldEqual, id)
			So(release.Version, ShouldEqual, "1.0.0")
			So(release.BaseVersion, ShouldBeEmpty)
			So(release.ProjectID, ShouldEqual, "p1")
			So(release.Publisher, ShouldResemble, publisher)
			So(release.Notes, ShouldEqual, "first")
			So(release.State, ShouldEqual, interfaces.RELEASE_STATE_PREPARED)
			So(release.KNVersion, ShouldEqual, 0)
			So(release.BranchCommit, ShouldEqual, branchHead)
			// The revision recorded is the one the write left, for publishing to expect.
			So(release.DraftRevision, ShouldEqual, 8)
			// A lineage that never used a function or Skill records none.
			So(release.Capabilities, ShouldBeNil)
			So(release.CreateTime, ShouldBeGreaterThan, 0)

			// The package is the branch with only the network id set to the lineage's.
			So(string((*written)["network.bkn"]), ShouldEqual, strings.Replace(networkText, "id: supply", "id: "+id, 1))
			tagged, _ := realGS.ReadTree(ctx, id, branchHead)
			So((*written)["object_types/part.bkn"], ShouldResemble, tagged["object_types/part.bkn"])
			So(*written, ShouldHaveLength, len(tagged))

			// Nothing is tagged and main stays where it was until the release is published.
			_, err = realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(errors.Is(err, plumbing.ErrReferenceNotFound), ShouldBeTrue)
			mainAfter, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(mainAfter, ShouldEqual, mainBefore)
		})

		Convey("A release after another one builds on its version and drops the version the old scheme put in the name\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			named := definitions("A part, edited.", "A supplier.")
			named["network.bkn"] = []byte(strings.Replace(networkText, "name: Supply chain", "name: Supply chain · 1.0.0", 1))
			_, err := realGS.CommitFiles(ctx, id, branch, named, "old name", author)
			So(err, ShouldBeNil)
			expectChecks(kva, ra, id, "1.1.0", versioningAt(2, 7, false), nil,
				[]*interfaces.Release{{LineageID: id, NetworkID: id, Version: "1.0.0", State: interfaces.RELEASE_STATE_PUBLISHED}})
			written, _ := expectWrite(id, interfaces.KNReplaceImport{ExpectedDraftRevision: 7, Overwrite: true}, 8)

			req := request
			req.Version = "1.1.0"
			release, err := svc.PrepareRelease(ctx, token, id, req, publisher)

			So(err, ShouldBeNil)
			So(release.BaseVersion, ShouldEqual, "1.0.0")
			network, err := bkn.ParseNetworkFile(string((*written)["network.bkn"]), "network.bkn")
			So(err, ShouldBeNil)
			So(network.ID, ShouldEqual, id)
			So(network.Name, ShouldEqual, "Supply chain")
		})

		Convey("Edits someone made to the draft are refused as DraftHasChanges unless the owner overwrites them\n", func() {
			id := newLineageID()
			live, _ := setUp(id)
			expectDraftEdited := func() {
				expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, true), nil, nil)
				// The draft's revision is not one a release of the lineage wrote.
				ra.EXPECT().ListReleases(gomock.Any(), draftWritersQuery(id)).Return([]*interfaces.Release{
					{ID: "other", LineageID: id, Version: "0.9.0", State: interfaces.RELEASE_STATE_PREPARED, DraftRevision: 5},
				}, nil)
				// Its definitions match main, so it is no drift.
				kva.EXPECT().ExportVersion(gomock.Any(), token, id, interfaces.KN_VERSION_DRAFT).Return(live, nil)
			}
			expectDraftEdited()
			kva.EXPECT().ReplaceDraft(gomock.Any(), token, gomock.Any(), interfaces.KNReplaceImport{ExpectedDraftRevision: 7}).
				Return(nil, int64(0), versionRefusal(interfaces.KN_VERSION_ERROR_DRAFT_HAS_CHANGES))

			_, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_DraftHasChanges)

			Convey("and once the owner confirms, the draft is overwritten\n", func() {
				expectDraftEdited()
				_, recorded := expectWrite(id, interfaces.KNReplaceImport{ExpectedDraftRevision: 7, Overwrite: true}, 8)

				req := request
				req.OverwriteDraftChanges = true
				release, err := svc.PrepareRelease(ctx, token, id, req, publisher)

				So(err, ShouldBeNil)
				So(release, ShouldEqual, *recorded)
			})
		})

		Convey("A re-prepare of the same version overwrites its own draft and keeps the release\n", func() {
			id := newLineageID()
			_, branchHead := setUp(id)
			earlier := &interfaces.Release{ID: "r1", LineageID: id, NetworkID: id, Version: "1.0.0",
				State: interfaces.RELEASE_STATE_PREPARED, DraftRevision: 7, CreateTime: 5}
			// The draft has edits, all of them what preparing 1.0.0 wrote: no drift check, no question.
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, true), earlier, nil)
			ra.EXPECT().ListReleases(gomock.Any(), draftWritersQuery(id)).Return([]*interfaces.Release{earlier}, nil)
			kva.EXPECT().ReplaceDraft(gomock.Any(), token, gomock.Any(), interfaces.KNReplaceImport{ExpectedDraftRevision: 7, Overwrite: true}).
				Return(&interfaces.BKNImportResult{KNID: id}, int64(9), nil)
			kva.EXPECT().BuildDraftIndex(gomock.Any(), token, id).Return(nil)
			prepared := ra.EXPECT().PrepareRelease(gomock.Any(), gomock.Any())

			Convey("under the id and create time of the release it replaces\n", func() {
				prepared.DoAndReturn(func(_ context.Context, release *interfaces.Release) (bool, error) {
					So(release.Version, ShouldEqual, "1.0.0")
					So(release.State, ShouldEqual, interfaces.RELEASE_STATE_PREPARED)
					release.ID, release.CreateTime = earlier.ID, earlier.CreateTime
					return true, nil
				})

				release, err := svc.PrepareRelease(ctx, token, id, request, publisher)

				So(err, ShouldBeNil)
				So(release.ID, ShouldEqual, "r1")
				So(release.CreateTime, ShouldEqual, 5)
				So(release.DraftRevision, ShouldEqual, 9)
				So(release.BranchCommit, ShouldEqual, branchHead)
			})

			Convey("but a version published in the meantime replies 409\n", func() {
				prepared.Return(false, nil)

				_, err := svc.PrepareRelease(ctx, token, id, request, publisher)

				status, code := httpCode(err)
				So(status, ShouldEqual, http.StatusConflict)
				So(code, ShouldEqual, perrors.BknProject_Release_VersionExists)
				So(errorDetails(err), ShouldResemble, map[string]any{"lineage_id": id, "version": "1.0.0"})
			})
		})

		Convey("After a switch back, the draft a published release wrote is still the project's: no drift check\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			// 1.0.1 published as version 2 at draft revision 4, then version 1 runs again: the draft is
			// based on 2, so bkn-backend reports changes against the running version.
			published := &interfaces.Release{ID: "r2", LineageID: id, NetworkID: id, Version: "1.0.1",
				State: interfaces.RELEASE_STATE_PUBLISHED, KNVersion: 2, DraftRevision: 4}
			versioning := versioningAt(1, 4, true)
			versioning.PreviousVersion, versioning.Draft.BaseVersion, versioning.Draft.Outdated = 2, 2, true
			expectChecks(kva, ra, id, "1.0.0", versioning, nil, []*interfaces.Release{published})
			ra.EXPECT().ListReleases(gomock.Any(), draftWritersQuery(id)).Return([]*interfaces.Release{published}, nil)
			kva.EXPECT().ReplaceDraft(gomock.Any(), token, gomock.Any(), interfaces.KNReplaceImport{ExpectedDraftRevision: 4, Overwrite: true}).
				Return(&interfaces.BKNImportResult{KNID: id}, int64(6), nil)
			kva.EXPECT().BuildDraftIndex(gomock.Any(), token, id).Return(nil)
			ra.EXPECT().PrepareRelease(gomock.Any(), gomock.Any()).Return(true, nil)

			release, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			So(err, ShouldBeNil)
			So(release.DraftRevision, ShouldEqual, 6)
		})

		Convey("Drift between main and the draft replies 409 with the paths and writes nothing\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, true), nil, nil)
			ra.EXPECT().ListReleases(gomock.Any(), draftWritersQuery(id)).Return([]*interfaces.Release{}, nil)
			kva.EXPECT().ExportVersion(gomock.Any(), token, id, interfaces.KN_VERSION_DRAFT).
				Return(definitions("A part.", "A supplier, edited in Studio."), nil)

			_, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_Drift)
			report := errorDetails(err).(*interfaces.DriftReport)
			So(report.KNID, ShouldEqual, id)
			So(report.Changed, ShouldResemble, []string{"object_types/supplier.bkn"})
			So(report.Added, ShouldBeEmpty)
			So(report.Removed, ShouldBeEmpty)
		})

		Convey("A branch without the head of main replies 409 and writes nothing\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			_, err := realGS.CommitFiles(ctx, id, "main", definitions("A part.", "A supplier, edited live."), "drift", author)
			So(err, ShouldBeNil)
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)

			_, err = svc.PrepareRelease(ctx, token, id, request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_BranchBehind)
		})

		Convey("A version whose publish was sent with no answer is not prepared again\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(versioningAt(2, 7, false), nil)
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(&interfaces.Release{
				LineageID: id, Version: "1.0.0", State: interfaces.RELEASE_STATE_PREPARED,
				PublishRequest: &interfaces.KNVersionPublish{ExpectedDraftRevision: 7, IdempotencyKey: "p1:1.0.0:x"}}, true, nil)

			_, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_PublishPending)
		})

		Convey("A version a release of the one-network-per-release scheme took replies 409\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(versioningAt(2, 7, false), nil)
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(&interfaces.Release{
				LineageID: id, Version: "1.0.0", State: interfaces.RELEASE_STATE_LEGACY}, true, nil)

			_, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			_, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Release_VersionExists)
		})

		Convey("A version already published replies 409\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(versioningAt(2, 7, false), nil)
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(&interfaces.Release{
				LineageID: id, Version: "1.0.0", State: interfaces.RELEASE_STATE_PUBLISHED, KNVersion: 2}, true, nil)

			_, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_VersionExists)
			So(errorDetails(err), ShouldResemble, map[string]any{"lineage_id": id, "version": "1.0.0"})
		})

		Convey("A network that is not versioned replies 409\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(&interfaces.KNVersioning{}, nil)

			_, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_NotVersioned)
		})

		Convey("A branch that moved since the review replies 409 and writes nothing\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)

			req := request
			req.ExpectedBranchCommit = "previous-reviewed-head"
			_, err := svc.PrepareRelease(ctx, token, id, req, publisher)

			_, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Release_ReviewOutdated)
		})

		Convey("Skipped capabilities fail preparing without recording it\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			skipped := []*interfaces.CapabilitySkip{{CapabilityType: "skill", Name: "planner", Reason: "no_execute_permission"}}
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)
			kva.EXPECT().ReplaceDraft(gomock.Any(), token, gomock.Any(), gomock.Any()).Return(&interfaces.BKNImportResult{
				KNID: id, Capabilities: &interfaces.CapabilityImportReport{Skipped: skipped}}, int64(8), nil)

			_, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_CapabilitiesSkipped)
			So(errorDetails(err), ShouldResemble, map[string]any{"network_id": id, "version": "1.0.0", "skipped": skipped})
		})

		Convey("A concept index the draft cannot get built fails preparing without recording it\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)
			kva.EXPECT().ReplaceDraft(gomock.Any(), token, gomock.Any(), gomock.Any()).Return(&interfaces.BKNImportResult{KNID: id}, int64(8), nil)
			kva.EXPECT().BuildDraftIndex(gomock.Any(), token, id).Return(versionRefusal(interfaces.KN_VERSION_ERROR_CONCEPT_INDEX_FAILED))
			// No PrepareRelease: the mock expects none.

			_, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusServiceUnavailable)
			So(code, ShouldEqual, perrors.BknProject_Release_NotReady)
		})

		Convey("Definitions the release cannot rename reply 400 and write nothing\n", func() {
			id := newLineageID()
			_, _ = setUp(id)
			// A block name loads but cannot be swapped line for line.
			broken := definitions("A part, edited.", "A supplier.")
			broken["network.bkn"] = []byte(strings.Replace(networkText, "name: Supply chain", "name: |\n  Supply chain", 1))
			_, err := realGS.CommitFiles(ctx, id, branch, broken, "block name", author)
			So(err, ShouldBeNil)
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)

			_, err = svc.PrepareRelease(ctx, token, id, request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidDefinitions)
		})

		Convey("Bad versions and branches are refused before anything is read\n", func() {
			for _, version := range []string{"", "1.0", "v1.0.0", "01.0.0", "1.0.0-rc1", "1.0.0.0", strings.Repeat("1", 31) + ".0.0"} {
				req := request
				req.Version = version
				_, err := svc.PrepareRelease(ctx, token, "orders", req, publisher)
				status, code := httpCode(err)
				So(status, ShouldEqual, http.StatusBadRequest)
				So(code, ShouldEqual, perrors.BknProject_Release_InvalidVersion)
			}

			req := request
			req.Branch = "main"
			_, err := svc.PrepareRelease(ctx, token, "orders", req, publisher)
			_, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_MainBranchReadOnly)
		})

		Convey("A project id or notes the release table cannot hold are refused before anything is read\n", func() {
			req := request
			req.ProjectID = strings.Repeat("项", 41)
			_, err := svc.PrepareRelease(ctx, token, "orders", req, publisher)
			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Release_InvalidProjectID)

			req = request
			req.Notes = strings.Repeat("n", 65536)
			_, err = svc.PrepareRelease(ctx, token, "orders", req, publisher)
			status, code = httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Release_InvalidNotes)

			Convey("while the longest values that fit pass\n", func() {
				id := newLineageID()
				_, _ = setUp(id)
				kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(versioningAt(1, 7, false), nil)
				ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").
					Return(&interfaces.Release{LineageID: id, Version: "1.0.0", State: interfaces.RELEASE_STATE_PUBLISHED}, true, nil)
				req := request
				req.ProjectID = strings.Repeat("项", 40)
				req.Notes = strings.Repeat("n", 65535)

				_, err := svc.PrepareRelease(ctx, token, id, req, publisher)

				_, code := httpCode(err)
				So(code, ShouldEqual, perrors.BknProject_Release_VersionExists)
			})
		})

		Convey("An unknown lineage replies 404 before anything is read\n", func() {
			_, err := svc.PrepareRelease(ctx, token, "missing", request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusNotFound)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NotFound)
		})
	})
}

func Test_LineageService_PublishRelease(t *testing.T) {
	Convey("Test lineageService PublishRelease on a real git store\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		kva := pmock.NewMockKNVersionAccess(mockCtrl)
		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &lineageService{gs: realGS, kva: kva, ra: ra}
		ctx := context.Background()
		const branch = "project/p1"
		request := interfaces.PublishReleaseRequest{Branch: branch, Version: "1.0.0", Notes: "go live"}
		const notes = "go live\n\nfirst"

		// setUp starts a lineage with a branch of one edit, prepared as 1.0.0.
		setUp := func() (id string, release *interfaces.Release) {
			id = newLineageID()
			_, err := realGS.InitLineage(ctx, id, definitions("A part.", "A supplier."), "init", author)
			So(err, ShouldBeNil)
			_, err = svc.CreateBranch(ctx, id, branch, "")
			So(err, ShouldBeNil)
			branchHead, err := realGS.CommitFiles(ctx, id, branch, definitions("A part, edited.", "A supplier."), "edit", author)
			So(err, ShouldBeNil)
			return id, &interfaces.Release{ID: "r-" + id, LineageID: id, NetworkID: id, Version: "1.0.0", ProjectID: "p1",
				Publisher: publisher, Notes: "first", State: interfaces.RELEASE_STATE_PREPARED, BranchCommit: branchHead, DraftRevision: 8}
		}
		// running expects the network's running version read for the publish to expect.
		running := func(id string, version int) {
			kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(versioningAt(version, 8, true), nil)
		}
		// keyed sets the idempotency key of req: the release's owner and version and a digest of req.
		keyed := func(release *interfaces.Release, req interfaces.KNVersionPublish) interfaces.KNVersionPublish {
			key, err := publishKey(release, req)
			So(err, ShouldBeNil)
			So(key, ShouldStartWith, "bknp-")
			So(len(key), ShouldBeLessThanOrEqualTo, 64)
			req.IdempotencyKey = key
			return req
		}
		// publishOf is the publish of the release's draft revision, refusing sources the network's
		// agents cannot read, under the idempotency key of its own content.
		publishOf := func(release *interfaces.Release, expectedRunning int) interfaces.KNVersionPublish {
			return keyed(release, interfaces.KNVersionPublish{ExpectedRunningVersion: expectedRunning, ExpectedDraftRevision: release.DraftRevision,
				Notes: notes, Label: "v" + release.Version, FailOnSkippedSources: true,
				Source: interfaces.KNVersionSource{Type: interfaces.KN_VERSION_SOURCE_TYPE,
					ProjectID: release.ProjectID, Ref: release.BranchCommit}})
		}
		// expectPublish expects req kept with the release, then sent.
		expectPublish := func(release *interfaces.Release, req interfaces.KNVersionPublish) *gomock.Call {
			saved := ra.EXPECT().SavePublishRequest(gomock.Any(), release.ID, &req).Return(nil)
			return kva.EXPECT().Publish(gomock.Any(), token, release.LineageID, req).After(saved)
		}
		// expectForget expects the kept request forgotten after a refusal.
		expectForget := func(release *interfaces.Release) {
			ra.EXPECT().SavePublishRequest(gomock.Any(), release.ID, gomock.Nil()).Return(nil)
		}
		// exported is what the running version reads back: bkn-backend re-serializes network.bkn.
		exported := func(id string, commit string) map[string][]byte {
			files, err := realGS.ReadTree(ctx, id, commit)
			So(err, ShouldBeNil)
			files = copyFiles(files)
			files["network.bkn"] = append(append([]byte{}, files["network.bkn"]...), []byte("\nRe-serialized.\n")...)
			return files
		}
		noTag := func(id string) {
			_, err := realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(errors.Is(err, plumbing.ErrReferenceNotFound), ShouldBeTrue)
		}

		Convey("Publishing makes the draft the running version, then tags the release and brings main and the branch to it\n", func() {
			id, release := setUp()
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)
			running(id, 1)
			warnings := []interfaces.KNVersionWarning{{Category: "metric", ID: "m1", Description: "not indexed"}}
			expectPublish(release, publishOf(release, 1)).Return(&interfaces.KNVersionPublished{
				KNVersion: interfaces.KNVersion{Version: 2, Label: "v1.0.0"}, Warnings: warnings,
				PublishedMetrics: []interfaces.KNPublishedMetric{{ID: "m1", Version: 2}}, PurgedVersion: 0}, nil)
			ra.EXPECT().MarkPublished(gomock.Any(), release.ID, 2, notes, gomock.Any()).Return(nil)
			kva.EXPECT().ExportVersion(gomock.Any(), token, id, interfaces.KN_VERSION_RUNNING).Return(exported(id, release.BranchCommit), nil)

			result, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			So(err, ShouldBeNil)
			So(result.NetworkID, ShouldEqual, id)
			So(result.Tag, ShouldEqual, "v1.0.0")
			So(result.KNVersion, ShouldEqual, 2)
			So(result.Warnings, ShouldResemble, warnings)
			So(result.PublishedMetrics, ShouldResemble, []interfaces.KNPublishedMetric{{ID: "m1", Version: 2}})
			So(result.Release, ShouldEqual, release)
			So(release.State, ShouldEqual, interfaces.RELEASE_STATE_PUBLISHED)
			So(release.KNVersion, ShouldEqual, 2)
			So(release.Notes, ShouldEqual, notes)

			tagHead, err := realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(err, ShouldBeNil)
			So(tagHead, ShouldEqual, release.BranchCommit)

			// main moved past the tag to the commit of the re-serialized export.
			mainHead, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(result.Commit, ShouldEqual, mainHead)
			repo, err := git.PlainOpen(filepath.Join(gitRoot, id+".git"))
			So(err, ShouldBeNil)
			commit, err := repo.CommitObject(plumbing.NewHash(mainHead))
			So(err, ShouldBeNil)
			So(commit.ParentHashes, ShouldResemble, []plumbing.Hash{plumbing.NewHash(release.BranchCommit)})
			So(commit.Message, ShouldEqual, "sync live definitions after release v1.0.0")
			onMain, _ := realGS.ReadTree(ctx, id, "main")
			So(string(onMain["network.bkn"]), ShouldEndWith, "Re-serialized.\n")

			// The project branch follows main, so the project's next release does not start behind it.
			branchAfter, _ := realGS.ResolveRef(ctx, id, "refs/heads/"+branch)
			So(branchAfter, ShouldEqual, mainHead)

			expectChecks(kva, ra, id, "1.1.0", versioningAt(2, 9, false), nil, []*interfaces.Release{release})
			next, err := svc.PreviewRelease(ctx, token, id, branch, "1.1.0")
			So(err, ShouldBeNil)
			So(next.Refusals, ShouldBeEmpty)
			So(next.BaseVersion, ShouldEqual, "1.0.0")
		})

		Convey("The first publish onto a network without a running version expects none\n", func() {
			id, release := setUp()
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)
			kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(versioningAt(0, 8, true), nil)
			expectPublish(release, publishOf(release, 0)).
				Return(&interfaces.KNVersionPublished{KNVersion: interfaces.KNVersion{Version: 1}}, nil)
			ra.EXPECT().MarkPublished(gomock.Any(), release.ID, 1, notes, gomock.Any()).Return(nil)
			// A failed export after the publish leaves main at the tag; the release stands.
			kva.EXPECT().ExportVersion(gomock.Any(), token, id, interfaces.KN_VERSION_RUNNING).
				Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusBadGateway})

			result, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			So(err, ShouldBeNil)
			So(result.KNVersion, ShouldEqual, 1)
			So(result.Commit, ShouldEqual, release.BranchCommit)
			mainHead, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(mainHead, ShouldEqual, release.BranchCommit)
		})

		Convey("A retry after a lost reply resends the kept request as it was and takes the first result\n", func() {
			id, release := setUp()
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)
			running(id, 1)
			var kept, first *interfaces.KNVersionPublish
			saved := ra.EXPECT().SavePublishRequest(gomock.Any(), release.ID, gomock.Not(gomock.Nil())).DoAndReturn(
				func(_ context.Context, _ string, req *interfaces.KNVersionPublish) error {
					kept = req
					return nil
				})
			// bkn-backend publishes, but the reply is lost on the way back.
			kva.EXPECT().Publish(gomock.Any(), token, id, gomock.Any()).After(saved).DoAndReturn(
				func(_ context.Context, _ string, _ string, req interfaces.KNVersionPublish) (*interfaces.KNVersionPublished, error) {
					first = &req
					return nil, errors.New("connection reset")
				})

			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, _ := httpCode(err)
			So(status, ShouldEqual, http.StatusInternalServerError)
			So(release.State, ShouldEqual, interfaces.RELEASE_STATE_PREPARED)
			So(kept, ShouldNotBeNil)
			So(*kept, ShouldResemble, *first)
			So(first.IdempotencyKey, ShouldNotBeEmpty)
			noTag(id)

			// The retry loads the release with the kept request. The running version has moved and
			// the caller sends other notes and a confirmation: the request goes as it was kept, with
			// no versioning read, and bkn-backend answers its key with the first result.
			loaded := *release
			loaded.PublishRequest = kept
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(&loaded, true, nil)
			var second *interfaces.KNVersionPublish
			kva.EXPECT().Publish(gomock.Any(), token, id, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, _ string, req interfaces.KNVersionPublish) (*interfaces.KNVersionPublished, error) {
					second = &req
					return &interfaces.KNVersionPublished{KNVersion: interfaces.KNVersion{Version: 2, Label: "v1.0.0"}}, nil
				})
			// The notes recorded are those the version carries, from the first request.
			ra.EXPECT().MarkPublished(gomock.Any(), release.ID, 2, notes, gomock.Any()).Return(nil)
			kva.EXPECT().ExportVersion(gomock.Any(), token, id, interfaces.KN_VERSION_RUNNING).Return(exported(id, release.BranchCommit), nil)

			retry := request
			retry.Notes, retry.AllowOutdatedBase = "go live, again", true
			result, err := svc.PublishRelease(ctx, token, id, retry, publisher, author)

			So(err, ShouldBeNil)
			So(result.KNVersion, ShouldEqual, 2)
			So(*second, ShouldResemble, *first)
			firstBody, err := json.Marshal(first)
			So(err, ShouldBeNil)
			secondBody, err := json.Marshal(second)
			So(err, ShouldBeNil)
			So(string(secondBody), ShouldEqual, string(firstBody))
			So(second.IdempotencyKey, ShouldEqual, first.IdempotencyKey)
			So(loaded.Notes, ShouldEqual, notes)
			tagHead, _ := realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(tagHead, ShouldEqual, release.BranchCommit)
		})

		Convey("A bad gateway keeps the request for the retry, a refusal forgets it\n", func() {
			id, release := setUp()
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)
			running(id, 1)
			// No forget: the mock expects only the save.
			expectPublish(release, publishOf(release, 1)).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusBadGateway})

			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, _ := httpCode(err)
			So(status, ShouldEqual, http.StatusBadGateway)

			Convey("while a 409 forgets it\n", func() {
				loaded := *release
				kept := publishOf(release, 1)
				loaded.PublishRequest = &kept
				ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(&loaded, true, nil)
				kva.EXPECT().Publish(gomock.Any(), token, id, kept).Return(nil, versionRefusal(interfaces.KN_VERSION_ERROR_DRAFT_REVISION_MISMATCH))
				expectForget(&loaded)

				_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

				status, code := httpCode(err)
				So(status, ShouldEqual, http.StatusConflict)
				So(code, ShouldEqual, perrors.BknProject_Release_DraftChanged)
			})
		})

		Convey("A release no project made is keyed by its lineage\n", func() {
			id, release := setUp()
			release.ProjectID = ""
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)
			running(id, 1)
			byLineage := publishOf(release, 1)
			So(byLineage.IdempotencyKey, ShouldStartWith, "bknp-")
			expectPublish(release, byLineage).Return(&interfaces.KNVersionPublished{KNVersion: interfaces.KNVersion{Version: 2}}, nil)
			ra.EXPECT().MarkPublished(gomock.Any(), release.ID, 2, notes, gomock.Any()).Return(nil)
			kva.EXPECT().ExportVersion(gomock.Any(), token, id, interfaces.KN_VERSION_RUNNING).Return(exported(id, release.BranchCommit), nil)

			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			So(err, ShouldBeNil)
		})

		Convey("Refusals of the publish are worded as bkn-project's and tag nothing\n", func() {
			for _, refusal := range []struct {
				backend, code string
				status        int
			}{
				{interfaces.KN_VERSION_ERROR_NOTHING_TO_PUBLISH, perrors.BknProject_Release_NothingToPublish, http.StatusConflict},
				{interfaces.KN_VERSION_ERROR_DRAFT_BASE_OUTDATED, perrors.BknProject_Release_DraftOutdated, http.StatusConflict},
				{interfaces.KN_VERSION_ERROR_DRAFT_REVISION_MISMATCH, perrors.BknProject_Release_DraftChanged, http.StatusConflict},
				// Someone else published or switched versions since the running version was read.
				{interfaces.KN_VERSION_ERROR_RUNNING_MISMATCH, perrors.BknProject_Release_RunningChanged, http.StatusConflict},
				// The key names another publish: nothing the owner can fix.
				{interfaces.KN_VERSION_ERROR_IDEMPOTENCY_KEY_REUSED, perrors.BknProject_Release_InternalError, http.StatusInternalServerError},
			} {
				id, release := setUp()
				ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)
				running(id, 1)
				expectPublish(release, publishOf(release, 1)).Return(nil, versionRefusal(refusal.backend))
				// A refusal published nothing: the request is forgotten, the next attempt is a new one.
				expectForget(release)

				_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

				status, got := httpCode(err)
				So(status, ShouldEqual, refusal.status)
				So(got, ShouldEqual, refusal.code)
				So(release.State, ShouldEqual, interfaces.RELEASE_STATE_PREPARED)
				noTag(id)
			}
		})

		Convey("An outdated base the owner confirmed is published\n", func() {
			id, release := setUp()
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)
			running(id, 1)
			confirmed := publishOf(release, 1)
			confirmed.AllowOutdatedBase = true
			confirmed = keyed(release, confirmed)
			expectPublish(release, confirmed).Return(&interfaces.KNVersionPublished{KNVersion: interfaces.KNVersion{Version: 3}}, nil)
			ra.EXPECT().MarkPublished(gomock.Any(), release.ID, 3, notes, gomock.Any()).Return(nil)
			kva.EXPECT().ExportVersion(gomock.Any(), token, id, interfaces.KN_VERSION_RUNNING).Return(exported(id, release.BranchCommit), nil)

			req := request
			req.AllowOutdatedBase = true
			result, err := svc.PublishRelease(ctx, token, id, req, publisher, author)

			So(err, ShouldBeNil)
			So(result.KNVersion, ShouldEqual, 3)
		})

		Convey("A release published already only has its git steps finished\n", func() {
			id, release := setUp()
			release.State, release.KNVersion, release.Notes = interfaces.RELEASE_STATE_PUBLISHED, 2, notes
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)
			// No versioning read and no Publish or MarkPublished: the mocks expect none.
			kva.EXPECT().ExportVersion(gomock.Any(), token, id, interfaces.KN_VERSION_RUNNING).Return(exported(id, release.BranchCommit), nil)

			result, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			So(err, ShouldBeNil)
			So(result.KNVersion, ShouldEqual, 2)
			tagHead, _ := realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(tagHead, ShouldEqual, release.BranchCommit)
			mainHead, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(result.Commit, ShouldEqual, mainHead)
			So(mainHead, ShouldNotEqual, release.BranchCommit)

			Convey("and once they are, a retry changes nothing\n", func() {
				ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)

				again, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

				So(err, ShouldBeNil)
				So(again.Commit, ShouldEqual, mainHead)
				after, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
				So(after, ShouldEqual, mainHead)
			})
		})

		Convey("A tag of the version at another commit replies 409\n", func() {
			id, release := setUp()
			release.State, release.KNVersion = interfaces.RELEASE_STATE_PUBLISHED, 2
			mainHead, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(realGS.Tag(ctx, id, "v1.0.0", mainHead), ShouldBeNil)
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)

			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_VersionExists)
			So(errorDetails(err), ShouldResemble, map[string]any{"tag": "v1.0.0", "commit": mainHead})
		})

		Convey("Notes that would not fit with the prepared ones are refused before publishing\n", func() {
			id, _ := setUp()
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(&interfaces.Release{ID: "r1", LineageID: id,
				Version: "1.0.0", State: interfaces.RELEASE_STATE_PREPARED, Notes: strings.Repeat("a", 40000)}, true, nil)
			long := request
			long.Notes = strings.Repeat("b", 30000)

			_, err := svc.PublishRelease(ctx, token, id, long, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Release_InvalidNotes)
			noTag(id)
		})

		Convey("A version that was not prepared replies 409\n", func() {
			id, _ := setUp()
			ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(nil, false, nil)

			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_NotPrepared)
			noTag(id)
		})

		Convey("Bad versions, branches and notes are refused before anything is read\n", func() {
			req := request
			req.Version = "1.0"
			_, err := svc.PublishRelease(ctx, token, "orders", req, publisher, author)
			_, code := httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Release_InvalidVersion)

			req = request
			req.Branch = "main"
			_, err = svc.PublishRelease(ctx, token, "orders", req, publisher, author)
			_, code = httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_MainBranchReadOnly)

			req = request
			req.Notes = strings.Repeat("n", 65536)
			_, err = svc.PublishRelease(ctx, token, "orders", req, publisher, author)
			_, code = httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Release_InvalidNotes)
		})

		Convey("An unknown lineage replies 404 before anything is read\n", func() {
			_, err := svc.PublishRelease(ctx, token, "missing", request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusNotFound)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NotFound)
		})
	})
}

func Test_CopySuffix(t *testing.T) {
	Convey("Test copySuffix\n", t, func() {
		So(copySuffix("supply_ontology_hand", "1.1.0"), ShouldEqual, "supply_ontology_hand_v1_1_0")

		Convey("Two versions of a lineage name their copies apart\n", func() {
			So(copySuffix("supply", "1.0.0"), ShouldNotEqual, copySuffix("supply", "1.1.0"))
			So(copySuffix("supply", "1.10.0"), ShouldNotEqual, copySuffix("supply", "1.1.0"))
		})

		Convey("A long lineage id is cut and loses the separator the cut leaves\n", func() {
			lineageID := strings.Repeat("a", 121) + "_bcdef"
			So(lineageID, ShouldHaveLength, 127)
			suffix := copySuffix(lineageID, "1.10.0")
			So(suffix, ShouldEqual, strings.Repeat("a", 120)+"_v1_10_0")
			So(len(suffix), ShouldBeLessThanOrEqualTo, maxNetworkIDLength)
		})

		Convey("The longest version still leaves part of the lineage id\n", func() {
			suffix := copySuffix(strings.Repeat("b", 128), strings.Repeat("9", 28)+".0.0")
			So(suffix, ShouldHaveLength, maxNetworkIDLength)
			So(suffix, ShouldStartWith, strings.Repeat("b", 94)+"_v")
		})
	})
}

func Test_SetNetworkHeader(t *testing.T) {
	Convey("Test setNetworkHeader\n", t, func() {
		Convey("Only the id and name lines change\n", func() {
			text := "---\ntype: knowledge_network\nid: supply\nname: Supply\ntags: [a]\n---\n\n# Supply\n\nid: in the body\n"
			out, err := setNetworkHeader(text, "orders", "Orders")
			So(err, ShouldBeNil)
			So(out, ShouldEqual, "---\ntype: knowledge_network\nid: orders\nname: Orders\ntags: [a]\n---\n\n# Supply\n\nid: in the body\n")
		})

		Convey("A name YAML would misread is quoted, and CRLF lines keep their ending\n", func() {
			text := "---\r\ntype: knowledge_network\r\nid: supply\r\nname: \"Supply\"\r\n---\r\n"
			out, err := setNetworkHeader(text, "orders", "Supply: core")
			So(err, ShouldBeNil)
			fm, err := bkn.ParseFrontmatter(out)
			So(err, ShouldBeNil)
			So(fm["name"], ShouldEqual, "Supply: core")
			So(fm["id"], ShouldEqual, "orders")
			So(out, ShouldContainSubstring, "\r\n---\r\n")
		})

		Convey("A frontmatter without name, or with a block name, is refused\n", func() {
			_, err := setNetworkHeader("---\ntype: knowledge_network\nid: supply\n---\n", "x", "X")
			So(err, ShouldNotBeNil)
			_, err = setNetworkHeader("---\nid: supply\nname: |\n  Supply\n---\n", "x", "X")
			So(err, ShouldNotBeNil)
			_, err = setNetworkHeader("# no frontmatter\n", "x", "X")
			So(err, ShouldNotBeNil)
		})
	})
}

func TestPublishKeyChangesWithTheRequest(t *testing.T) {
	release := &interfaces.Release{ID: "r1", ProjectID: "p1", LineageID: "l1", Version: "1.0.0", DraftRevision: 8}
	req := interfaces.KNVersionPublish{ExpectedRunningVersion: 1, ExpectedDraftRevision: 8, Notes: "n", Label: "v1.0.0",
		FailOnSkippedSources: true, Source: interfaces.KNVersionSource{Type: interfaces.KN_VERSION_SOURCE_TYPE, ProjectID: "p1"}}
	key := func(r *interfaces.Release, req interfaces.KNVersionPublish) string {
		k, err := publishKey(r, req)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	first := key(release, req)
	if !strings.HasPrefix(first, "bknp-") || len(first) > 64 {
		t.Fatal("the key fits bkn-backend's 64 characters", first)
	}
	if key(release, req) != first {
		t.Fatal("the same request must have the same key")
	}
	// A key already set is not part of the digest.
	withKey := req
	withKey.IdempotencyKey = first
	if key(release, withKey) != first {
		t.Fatal("the key must not depend on itself")
	}
	prepared := req
	prepared.ExpectedDraftRevision = 12
	confirmed := req
	confirmed.AllowOutdatedBase = true
	for name, other := range map[string]interfaces.KNVersionPublish{"draft revision": prepared, "outdated base": confirmed} {
		if key(release, other) == first {
			t.Fatal("a request differing in its "+name+" must not reuse the key", first)
		}
	}
	noProject := *release
	noProject.ProjectID = ""
	if key(&noProject, req) == first {
		t.Fatal("a release no project made is keyed by its lineage, not the project")
	}
	long := *release
	long.ProjectID, long.Version = strings.Repeat("l", 128), "123456789.123456789.123456789"
	if k := key(&long, req); len(k) > 64 {
		t.Fatal("a long lineage id must still give a key bkn-backend takes", len(k))
	}
}
