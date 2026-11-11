// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

// Basic information belongs to the network: an update writes it to every branch, so the
// network's concept document of every branch must carry it too (#3096).
func Test_knowledgeNetworkService_UpdateKN_RewritesOtherBranchDocuments(t *testing.T) {
	Convey("UpdateKN rewrites the network document of the other branches", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		kna := bmock.NewMockKNAccess(mockCtrl)
		ps := bmock.NewMockPermissionService(mockCtrl)
		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		mfs := bmock.NewMockModelFactoryService(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		service := &knowledgeNetworkService{
			appSetting: &common.AppSetting{ServerSetting: common.ServerSetting{DefaultSmallModelEnabled: true}},
			kna:        kna,
			ps:         ps,
			vbs:        vbs,
			mfs:        mfs,
			db:         db,
		}

		// Each text gets a vector naming the branch content it came from, so a test can tell
		// which text a written document was embedded from.
		vectorOf := func(text string) []float32 {
			for i, raw := range []string{"main-raw", "v1-raw", "v2-raw"} {
				if strings.HasSuffix(text, raw) {
					return []float32{float32(i + 1)}
				}
			}
			return []float32{0}
		}
		calls := [][]string{}
		failEmbedding := false
		mfs.EXPECT().GetDefaultModel(gomock.Any()).Return(&interfaces.SmallModel{ModelID: "m"}, nil).AnyTimes()
		mfs.EXPECT().GetVector(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *interfaces.SmallModel, in []string) ([]*cond.VectorResp, error) {
				calls = append(calls, in)
				if failEmbedding && len(calls) > 1 {
					return nil, errors.New("model unavailable")
				}
				vectors := make([]*cond.VectorResp, 0, len(in))
				for _, text := range in {
					vectors = append(vectors, &cond.VectorResp{Vector: vectorOf(text)})
				}
				return vectors, nil
			}).AnyTimes()
		written := map[string]map[string]any{}
		writes := map[string]int{}
		failOn := ""
		vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, docID string, doc map[string]any) error {
				writes[docID]++
				if docID == failOn {
					return errors.New("vega unavailable")
				}
				written[docID] = doc
				return nil
			}).AnyTimes()
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		kna.EXPECT().UpdateKN(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		smock.ExpectBegin()
		smock.ExpectCommit()

		docID := func(branch string) string {
			return interfaces.GenerateConceptDocuemtnID("kn1", interfaces.MODULE_TYPE_KN, "kn1", branch)
		}
		row := func(branch, name, raw string) *interfaces.KN {
			return &interfaces.KN{
				KNID: "kn1", KNName: name, Branch: branch, ModuleType: interfaces.MODULE_TYPE_KN,
				CommonInfo: interfaces.CommonInfo{Tags: []string{"t"}, Comment: "c", BKNRawContent: raw},
				Creator:    interfaces.AccountInfo{ID: "creator-" + branch},
			}
		}
		kn := &interfaces.KN{
			KNID: "kn1", KNName: "renamed", Branch: interfaces.DRAFT_BRANCH,
			CommonInfo: interfaces.CommonInfo{Tags: []string{"t"}, Comment: "c"},
		}

		Convey("main and every version get the new name with their own content", func() {
			kna.EXPECT().ListKNBranches(gomock.Any(), "kn1").Return([]string{"draft", "main", "v1"}, nil)
			kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "main").Return(row("main", "old", "main-raw"), nil)
			kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "v1").Return(row("v1", "old", "v1-raw"), nil)

			So(service.UpdateKN(ctx, nil, kn, false), ShouldBeNil)

			So(written, ShouldContainKey, docID("draft"))
			for _, branch := range []string{"main", "v1"} {
				So(written, ShouldContainKey, docID(branch))
				So(written[docID(branch)]["name"], ShouldEqual, "renamed")
				So(written[docID(branch)]["branch"], ShouldEqual, branch)
			}
			So(calls, ShouldHaveLength, 2)
			So(calls[1], ShouldResemble, []string{"renamed\nt\nc\nmain-raw", "renamed\nt\nc\nv1-raw"})
		})

		// A versioned network gains a branch with every published version; the rename runs
		// under the network lock, so it must not wait on one embedding per branch (#3104).
		Convey("the other branches are embedded in one request and each written once", func() {
			kna.EXPECT().ListKNBranches(gomock.Any(), "kn1").Return([]string{"draft", "main", "v1", "v2"}, nil)
			kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "main").Return(row("main", "old", "main-raw"), nil)
			kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "v1").Return(row("v1", "old", "v1-raw"), nil)
			kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "v2").Return(row("v2", "old", "v2-raw"), nil)

			So(service.UpdateKN(ctx, nil, kn, false), ShouldBeNil)

			// One request for the requested branch, one for all the others.
			So(calls, ShouldHaveLength, 2)
			So(calls[1], ShouldHaveLength, 3)
			for i, branch := range []string{"main", "v1", "v2"} {
				So(writes[docID(branch)], ShouldEqual, 1)
				So(written[docID(branch)]["name"], ShouldEqual, "renamed")
				So(written[docID(branch)]["_vector"], ShouldResemble, []any{float64(i + 1)})
			}
			So(writes[docID("draft")], ShouldEqual, 1)
		})

		Convey("a failed embedding of the other branches leaves the update standing", func() {
			kna.EXPECT().ListKNBranches(gomock.Any(), "kn1").Return([]string{"draft", "main", "v1"}, nil)
			kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "main").Return(row("main", "old", "main-raw"), nil)
			kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "v1").Return(row("v1", "old", "v1-raw"), nil)
			failEmbedding = true

			So(service.UpdateKN(ctx, nil, kn, false), ShouldBeNil)

			So(written, ShouldContainKey, docID("draft"))
			So(len(written), ShouldEqual, 1)
		})

		Convey("a branch whose basic information already matches is not rewritten", func() {
			kna.EXPECT().ListKNBranches(gomock.Any(), "kn1").Return([]string{"draft", "main"}, nil)
			kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "main").Return(row("main", "renamed", "main-raw"), nil)

			So(service.UpdateKN(ctx, nil, kn, false), ShouldBeNil)

			So(written, ShouldContainKey, docID("draft"))
			So(written, ShouldNotContainKey, docID("main"))
		})

		Convey("a failure on another branch leaves the update standing", func() {
			kna.EXPECT().ListKNBranches(gomock.Any(), "kn1").Return([]string{"draft", "main", "v1"}, nil)
			kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "main").Return(row("main", "old", "main-raw"), nil)
			kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "v1").Return(row("v1", "old", "v1-raw"), nil)
			failOn = docID("main")

			So(service.UpdateKN(ctx, nil, kn, false), ShouldBeNil)

			So(written, ShouldContainKey, docID("v1"))
		})

		Convey("listing the branches failing leaves the update standing", func() {
			kna.EXPECT().ListKNBranches(gomock.Any(), "kn1").Return(nil, errors.New("db down"))

			So(service.UpdateKN(ctx, nil, kn, false), ShouldBeNil)

			So(written, ShouldContainKey, docID("draft"))
			So(len(written), ShouldEqual, 1)
		})
	})
}
