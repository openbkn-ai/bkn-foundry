// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func Test_BKNRestHandler_DiffKNs(t *testing.T) {
	Convey("Test BKNHandler DiffKNs\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := bmock.NewMockAuthService(mockCtrl)
		kns := bmock.NewMockKNService(mockCtrl)
		bs := bmock.NewMockBKNService(mockCtrl)

		handler := MockNewBKNRestHandler(&common.AppSetting{}, as, kns, bs)
		handler.RegisterPublic(engine)

		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{}, nil)

		url := "/api/bkn-backend/v1/bkns/diff"
		post := func(body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			return w
		}

		Convey("Success returns the comparison\n", func() {
			bs.EXPECT().DiffNetworks(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ any, req interfaces.KNDiffRequest) (*interfaces.KNDiffResult, error) {
					So(req.Base.KNID, ShouldEqual, "kn1")
					So(req.Target.KNID, ShouldEqual, "kn2")
					So(req.FallbackByName, ShouldBeTrue)
					return &interfaces.KNDiffResult{
						Base:   interfaces.KNDiffSide{KNID: "kn1", Branch: "main", Name: "供应链主网"},
						Target: interfaces.KNDiffSide{KNID: "kn2", Branch: "main", Name: "试点网"},
						NetworkDiff: &bknsdk.NetworkDiff{
							Summary: bknsdk.DiffSummary{Updated: 1, Unchanged: 29},
							Lineage: bknsdk.Lineage{CommonIDs: 30, TotalIDs: 32, Related: true},
							Entries: []bknsdk.DefinitionDiff{{Type: "object_type", ID: "bom", Action: bknsdk.DiffUpdate}},
						},
					}, nil
				})

			w := post(`{"base":{"kn_id":"kn1"},"target":{"kn_id":"kn2"},"fallback_by_name":true}`)
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)

			var payload struct {
				Base    interfaces.KNDiffSide `json:"base"`
				Target  interfaces.KNDiffSide `json:"target"`
				Summary bknsdk.DiffSummary    `json:"summary"`
				Lineage bknsdk.Lineage        `json:"lineage"`
				Entries []struct {
					ID string `json:"id"`
				} `json:"entries"`
			}
			So(json.Unmarshal(w.Body.Bytes(), &payload), ShouldBeNil)
			So(payload.Base.Name, ShouldEqual, "供应链主网")
			So(payload.Target.KNID, ShouldEqual, "kn2")
			So(payload.Summary.Updated, ShouldEqual, 1)
			So(payload.Summary.Unchanged, ShouldEqual, 29)
			So(payload.Lineage.CommonIDs, ShouldEqual, 30)
			So(len(payload.Entries), ShouldEqual, 1)
			So(payload.Entries[0].ID, ShouldEqual, "bom")
		})

		// An unnamed branch means the default one. Sending "" through to the service would look up
		// a branch that does not exist and come back empty rather than refused.
		Convey("A missing branch falls back to the default branch\n", func() {
			bs.EXPECT().DiffNetworks(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ any, req interfaces.KNDiffRequest) (*interfaces.KNDiffResult, error) {
					So(req.Base.Branch, ShouldEqual, interfaces.MAIN_BRANCH)
					So(req.Target.Branch, ShouldEqual, interfaces.MAIN_BRANCH)
					return &interfaces.KNDiffResult{NetworkDiff: &bknsdk.NetworkDiff{}}, nil
				})

			So(post(`{"base":{"kn_id":"kn1"},"target":{"kn_id":"kn2"}}`).Result().StatusCode, ShouldEqual, http.StatusOK)
		})

		Convey("Failed when a side names no network\n", func() {
			So(post(`{"base":{"kn_id":""},"target":{"kn_id":"kn2"}}`).Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(post(`{"base":{"kn_id":"kn1"}}`).Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed when the body is not JSON\n", func() {
			So(post(`not json`).Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		// The service authorizes each network on its own. Its refusal must reach the caller as the
		// refusal it is, not flattened into a server error.
		Convey("A refusal from the service keeps its own status\n", func() {
			forbidden := rest.NewHTTPError(context.Background(), http.StatusForbidden,
				berrors.BknBackend_KnowledgeNetwork_InvalidParameter)
			bs.EXPECT().DiffNetworks(gomock.Any(), gomock.Any()).Return(nil, forbidden)

			So(post(`{"base":{"kn_id":"kn1"},"target":{"kn_id":"kn2"}}`).Result().StatusCode, ShouldEqual, http.StatusForbidden)
		})
	})
}

func Test_BKNRestHandler_ObjectDataStats(t *testing.T) {
	Convey("Test BKNHandler ObjectDataStats\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := bmock.NewMockAuthService(mockCtrl)
		odss := bmock.NewMockObjectDataStatsService(mockCtrl)

		handler := MockNewBKNRestHandler(&common.AppSetting{}, as, bmock.NewMockKNService(mockCtrl), bmock.NewMockBKNService(mockCtrl))
		handler.odss = odss
		handler.RegisterPublic(engine)

		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{}, nil)

		url := "/api/bkn-backend/v1/bkns/diff/object-data-stats"
		post := func(body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			return w
		}

		Convey("Success returns both sides and the delta\n", func() {
			distinct := int64(1150)
			odss.EXPECT().ObjectDataStats(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ any, req interfaces.ObjectDataStatsRequest) (*interfaces.ObjectDataStatsResult, error) {
					So(req.Base.OTID, ShouldEqual, "bom")
					So(req.Base.Branch, ShouldEqual, interfaces.MAIN_BRANCH)
					return &interfaces.ObjectDataStatsResult{
						Base:   &interfaces.ObjectDataStats{RowCount: 1000},
						Target: &interfaces.ObjectDataStats{RowCount: 1200, PrimaryKeyDistinct: &distinct},
						Delta:  interfaces.ObjectDataStatsDelta{RowCount: 200},
					}, nil
				})

			w := post(`{"base":{"kn_id":"kn1","ot_id":"bom"},"target":{"kn_id":"kn2","ot_id":"bom"}}`)
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)

			var payload interfaces.ObjectDataStatsResult
			So(json.Unmarshal(w.Body.Bytes(), &payload), ShouldBeNil)
			So(payload.Base.RowCount, ShouldEqual, 1000)
			So(payload.Delta.RowCount, ShouldEqual, 200)
			So(*payload.Target.PrimaryKeyDistinct, ShouldEqual, 1150)
		})

		Convey("Failed when a side names no object type\n", func() {
			So(post(`{"base":{"kn_id":"kn1"},"target":{"kn_id":"kn2","ot_id":"bom"}}`).Result().StatusCode,
				ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed when a side names no network\n", func() {
			So(post(`{"base":{"ot_id":"bom"},"target":{"kn_id":"kn2","ot_id":"bom"}}`).Result().StatusCode,
				ShouldEqual, http.StatusBadRequest)
		})

		Convey("A refusal from the service keeps its own status\n", func() {
			notFound := rest.NewHTTPError(context.Background(), http.StatusNotFound,
				berrors.BknBackend_KNDiff_ObjectTypeNotFound)
			odss.EXPECT().ObjectDataStats(gomock.Any(), gomock.Any()).Return(nil, notFound)

			So(post(`{"base":{"kn_id":"kn1","ot_id":"bom"},"target":{"kn_id":"kn2","ot_id":"bom"}}`).Result().StatusCode,
				ShouldEqual, http.StatusNotFound)
		})
	})
}
