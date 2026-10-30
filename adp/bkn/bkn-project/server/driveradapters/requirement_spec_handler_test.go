// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_RequirementSpecRestHandler(t *testing.T) {
	Convey("Test requirement specification handlers\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		rqs := pmock.NewMockRequirementService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, rqs: rqs}
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().
			Return(hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User, TokenID: "Bearer t"}, nil)
		caller := interfaces.AccountInfo{ID: "u1", Type: string(hydra.VisitorType_User)}
		const base = "/api/bkn-project/v1/projects/p1/requirements"
		spec := &interfaces.Requirement{ProjectID: "p1", Version: 1, Status: interfaces.REQUIREMENT_STATUS_READY, UpdateTime: 8}

		Convey("The specification is read with what is computed from it\n", func() {
			rqs.EXPECT().GetRequirement(gomock.Any(), "Bearer t", "p1").
				Return(&interfaces.RequirementView{Requirement: spec, Stale: true, UnresolvedConflicts: 2}, nil)

			w := serve(handler, http.MethodGet, base)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"stale":true`)
			So(w.Body.String(), ShouldContainSubstring, `"unresolved_conflicts":2`)
			So(w.Body.String(), ShouldContainSubstring, `"status":"ready"`)
		})

		Convey("The specification is exported as a YAML file for the target asked for\n", func() {
			rqs.EXPECT().ExportRequirement(gomock.Any(), "Bearer t", "p1", "eval").
				Return([]byte("answer_set_access: forbidden\n"), "p1-rq-1-eval.yaml", nil)

			w := serve(handler, http.MethodGet, base+"/export?target=eval")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Header().Get("Content-Type"), ShouldEqual, "application/yaml; charset=utf-8")
			So(w.Header().Get("Content-Disposition"), ShouldEqual, `attachment; filename="p1-rq-1-eval.yaml"`)
			So(w.Body.String(), ShouldEqual, "answer_set_access: forbidden\n")
		})

		Convey("An export the service refuses is replied as its error\n", func() {
			rqs.EXPECT().ExportRequirement(gomock.Any(), "Bearer t", "p1", "").
				Return(nil, "", httpError(http.StatusBadRequest, perrors.BknProject_Requirement_InvalidExportTarget))

			w := serve(handler, http.MethodGet, base+"/export")

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_InvalidExportTarget)
			So(w.Header().Get("Content-Disposition"), ShouldBeEmpty)
		})

		Convey("requirements:draft starts drafting and replies 202, discarding decisions only when asked\n", func() {
			drafting := &interfaces.Requirement{ProjectID: "p1", Status: interfaces.REQUIREMENT_STATUS_DRAFTING}
			rqs.EXPECT().GenerateRequirement(gomock.Any(), "Bearer t", "p1", caller, false).Return(drafting, nil)
			rqs.EXPECT().GenerateRequirement(gomock.Any(), "Bearer t", "p1", caller, true).Return(drafting, nil)

			w := serve(handler, http.MethodPost, "/api/bkn-project/v1/projects/p1/requirements:draft")
			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)
			So(w.Body.String(), ShouldContainSubstring, `"status":"drafting"`)
			w = serveBody(handler, http.MethodPost, "/api/bkn-project/v1/projects/p1/requirements:draft", []byte(`{"discard_decisions":true}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusAccepted)
			w = serveBody(handler, http.MethodPost, "/api/bkn-project/v1/projects/p1/requirements:draft", []byte(`{"discard_decisions":`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_InvalidRequestBody)
		})

		Convey("An item is added, edited and deleted by kind\n", func() {
			rqs.EXPECT().AddItem(gomock.Any(), "Bearer t", "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, gomock.Any(), caller).DoAndReturn(
				func(_ context.Context, _, _, _ string, edit interfaces.RequirementItemEdit, _ interfaces.AccountInfo) (*interfaces.Requirement, error) {
					So(edit.UpdateTime, ShouldEqual, 7)
					So(*edit.Text, ShouldEqual, "Which customers?")
					So(*edit.Needs, ShouldResemble, []interfaces.RequirementItemRef{{Kind: "object_type", ID: "customer"}})
					return spec, nil
				})
			rqs.EXPECT().EditItem(gomock.Any(), "Bearer t", "p1", interfaces.REQUIREMENT_KIND_RULES, "r1", gomock.Any(), caller).DoAndReturn(
				func(_ context.Context, _, _, _, _ string, edit interfaces.RequirementItemEdit, _ interfaces.AccountInfo) (*interfaces.Requirement, error) {
					So(*edit.Statement, ShouldEqual, "Signed only")
					So(edit.Name, ShouldBeNil)
					return spec, nil
				})
			rqs.EXPECT().DeleteItem(gomock.Any(), "Bearer t", "p1", interfaces.REQUIREMENT_KIND_CONCEPTS, "c1", int64(7), caller).Return(spec, nil)

			w := serveBody(handler, http.MethodPost, base+"/items/questions",
				[]byte(`{"update_time":7,"text":"Which customers?","needs":[{"kind":"object_type","id":"customer"}]}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusCreated)
			So(w.Body.String(), ShouldContainSubstring, `"update_time":8`)
			w = serveBody(handler, http.MethodPatch, base+"/items/rules/r1", []byte(`{"update_time":7,"statement":"Signed only"}`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			w = serve(handler, http.MethodDelete, base+"/items/concepts/c1?update_time=7")
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})

		Convey("A malformed body or update time is refused before the service\n", func() {
			w := serveBody(handler, http.MethodPatch, base+"/items/rules/r1", []byte(`not json`))
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_InvalidRequestBody)
			// A deletion has no body: an update time that does not parse is a missing one.
			w = serve(handler, http.MethodDelete, base+"/items/concepts/c1?update_time=soon")
			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, perrors.BknProject_Requirement_MissingUpdateTime)
			for _, path := range []string{"/concepts/c1/maps-to", "/conflicts/k1", "/choices/f1"} {
				w = serveBody(handler, http.MethodPut, base+path, []byte(`[`))
				So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			}
		})

		Convey("A concept is mapped, a conflict resolved and a choice decided from the body\n", func() {
			rqs.EXPECT().MapConcept(gomock.Any(), "Bearer t", "p1", "c1", interfaces.RequirementMapping{UpdateTime: 7,
				MapsTo: &interfaces.RequirementItemRef{Kind: "object_type", ID: "customer"}}, caller).Return(spec, nil)
			rqs.EXPECT().MapConcept(gomock.Any(), "Bearer t", "p1", "c2", interfaces.RequirementMapping{UpdateTime: 7}, caller).Return(spec, nil)
			rqs.EXPECT().ResolveConflict(gomock.Any(), "Bearer t", "p1", "k1", interfaces.RequirementResolve{UpdateTime: 7,
				Deferred: true, Reason: "Ask sales"}, caller).Return(spec, nil)
			rqs.EXPECT().DecideChoice(gomock.Any(), "Bearer t", "p1", "f1", interfaces.RequirementDecide{UpdateTime: 7,
				OptionID: "o2", Basis: "rare"}, caller).Return(spec, nil)

			for path, body := range map[string]string{
				"/concepts/c1/maps-to": `{"update_time":7,"maps_to":{"kind":"object_type","id":"customer"}}`,
				"/concepts/c2/maps-to": `{"update_time":7,"maps_to":null}`,
				"/conflicts/k1":        `{"update_time":7,"deferred":true,"reason":"Ask sales"}`,
				"/choices/f1":          `{"update_time":7,"option_id":"o2","basis":"rare"}`,
			} {
				w := serveBody(handler, http.MethodPut, base+path, []byte(body))
				So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			}
		})
	})
}
