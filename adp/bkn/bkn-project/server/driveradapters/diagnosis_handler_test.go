// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_DiagnosisRestHandler(t *testing.T) {
	Convey("Test network diagnosis handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		dgs := pmock.NewMockDiagnosisService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, dgs: dgs}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
			Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User, TokenID: "Bearer t"}, nil)
		caller := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
		const base = "/api/bkn-project/v1/projects/p1/ontology"

		Convey("diagnosis:run starts a diagnosis as the caller and replies 202 with its run\n", func() {
			dgs.EXPECT().Run(gomock.Any(), "Bearer t", "p1", caller).
				Return(&interfaces.DiagnosisRun{State: interfaces.DIAGNOSIS_STATE_RUNNING, RunID: "r1"}, nil)

			w := serve(handler, http.MethodPost, base+"/diagnosis:run")

			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(w.Body.String(), ShouldContainSubstring, `"state":"running"`)
			So(w.Body.String(), ShouldContainSubstring, `"run_id":"r1"`)
		})

		Convey("diagnosis:run replies the refusal of a running one\n", func() {
			dgs.EXPECT().Run(gomock.Any(), "Bearer t", "p1", caller).DoAndReturn(
				func(ctx context.Context, _, _ string, _ interfaces.AccountInfo) (*interfaces.DiagnosisRun, error) {
					return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Diagnosis_Running)
				})

			w := serve(handler, http.MethodPost, base+"/diagnosis:run")

			So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Diagnosis_Running)
		})

		Convey("The latest diagnosis is replied; none is 404\n", func() {
			dgs.EXPECT().Get(gomock.Any(), "Bearer t", "p1").Return(&interfaces.Diagnosis{
				State: interfaces.DIAGNOSIS_STATE_READY, Components: [][]string{{"a", "b"}},
				Suggestions: []*interfaces.DiagnosisSuggestion{{ID: "s1", Kind: interfaces.DIAGNOSIS_KIND_DATA_QUALITY,
					Apply: []byte("null"), Status: interfaces.DIAGNOSIS_STATUS_OPEN}}}, nil)

			w := serve(handler, http.MethodGet, base+"/diagnosis")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"components":[["a","b"]]`)
			So(w.Body.String(), ShouldContainSubstring, `"apply":null`)
			So(w.Body.String(), ShouldContainSubstring, `"stale":false`)

			dgs.EXPECT().Get(gomock.Any(), "Bearer t", "p1").DoAndReturn(
				func(ctx context.Context, _, _ string) (*interfaces.Diagnosis, error) {
					return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Diagnosis_NotFound)
				})
			So(serve(handler, http.MethodGet, base+"/diagnosis").Result().StatusCode, ShouldEqual, http.StatusNotFound)
		})

		Convey(":apply hands the suggestion and the draft read to the service\n", func() {
			dgs.EXPECT().Apply(gomock.Any(), "Bearer t", "p1", "abc123", interfaces.DiagnosisApply{DraftUpdateTime: 7}, caller).
				Return(&interfaces.DiagnosisApplied{DraftUpdateTime: 8, Suggestion: &interfaces.DiagnosisSuggestion{ID: "abc123",
					Status: interfaces.DIAGNOSIS_STATUS_APPLIED, Apply: []byte("null")}}, nil)

			w := serveBody(handler, http.MethodPost, base+"/diagnosis/suggestions/abc123:apply", []byte(`{"draft_update_time":7}`))

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"draft_update_time":8`)
			So(w.Body.String(), ShouldContainSubstring, `"status":"applied"`)
			So(serveBody(handler, http.MethodPost, base+"/diagnosis/suggestions/abc123:apply", []byte(`{`)).Result().StatusCode,
				ShouldEqual, http.StatusBadRequest)
		})

		Convey(":dismiss takes an optional reason\n", func() {
			dgs.EXPECT().Dismiss(gomock.Any(), "Bearer t", "p1", "abc123", interfaces.DiagnosisDismiss{Reason: "巧合"}, caller).
				Return(&interfaces.DiagnosisSuggestion{ID: "abc123", Status: interfaces.DIAGNOSIS_STATUS_DISMISSED, Apply: []byte("null")}, nil)
			dgs.EXPECT().Dismiss(gomock.Any(), "Bearer t", "p1", "abc123", interfaces.DiagnosisDismiss{}, caller).
				Return(&interfaces.DiagnosisSuggestion{ID: "abc123", Status: interfaces.DIAGNOSIS_STATUS_DISMISSED, Apply: []byte("null")}, nil)

			w := serveBody(handler, http.MethodPost, base+"/diagnosis/suggestions/abc123:dismiss", []byte(`{"reason":"巧合"}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"status":"dismissed"`)
			So(serve(handler, http.MethodPost, base+"/diagnosis/suggestions/abc123:dismiss").Result().StatusCode,
				ShouldEqual, http.StatusOK)
		})

		Convey("Another method of a suggestion is not found\n", func() {
			So(serve(handler, http.MethodPost, base+"/diagnosis/suggestions/abc123:delete").Result().StatusCode,
				ShouldEqual, http.StatusNotFound)
			So(serve(handler, http.MethodPost, base+"/diagnosis/suggestions/abc123").Result().StatusCode,
				ShouldEqual, http.StatusNotFound)
		})
	})
}
