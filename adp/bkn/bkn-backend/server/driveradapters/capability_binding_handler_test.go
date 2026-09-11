// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
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

	"bkn-backend/common"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

// Test_CapabilityRoutes_AllPrefixes locks the four-prefix registration (#1257). Registering only
// /api/bkn-backend/* would hand a 404 to every client still on the ontology-manager alias.
func Test_CapabilityRoutes_AllPrefixes(t *testing.T) {
	Convey("能力绑定端点在四个前缀上都注册", t, func() {
		restore := setGinMode()
		defer restore()

		engine := gin.New()
		engine.Use(gin.Recovery())
		handler := &restHandler{appSetting: &common.AppSetting{}}
		// RegisterPublic wires both the public and the internal groups.
		handler.RegisterPublic(engine)

		registered := map[string]bool{}
		for _, route := range engine.Routes() {
			registered[route.Method+" "+route.Path] = true
		}

		for _, prefix := range []string{
			"/api/bkn-backend/v1", "/api/ontology-manager/v1",
			"/api/bkn-backend/in/v1", "/api/ontology-manager/in/v1",
		} {
			So(registered[http.MethodPost+" "+prefix+"/knowledge-networks/:kn_id/capabilities"], ShouldBeTrue)
			So(registered[http.MethodGet+" "+prefix+"/knowledge-networks/:kn_id/capabilities"], ShouldBeTrue)
			So(registered[http.MethodDelete+" "+prefix+"/knowledge-networks/:kn_id/capabilities/:binding_ids"], ShouldBeTrue)
		}
	})
}

// Test_CapabilityAudit_TargetFromPath keeps the released binding IDs in the audit row. Without a
// path-parameter entry the target falls back to a synthesised "<target_type>:<request_id>", so the
// audit trail no longer says which binding was released — found on the test server, where DELETE
// rows carried the placeholder instead of the ID.
func Test_CapabilityAudit_TargetFromPath(t *testing.T) {
	Convey("解绑审计的 target 取路径上的 binding_ids", t, func() {
		restore := setGinMode()
		defer restore()

		engine := gin.New()
		var target string
		engine.DELETE("/knowledge-networks/:kn_id/capabilities/:binding_ids", func(c *gin.Context) {
			target = operationAuditPathTarget(c, "kn_capability_binding")
		})
		req := httptest.NewRequest(http.MethodDelete, "/knowledge-networks/kn1/capabilities/bind-1,bind-2", nil)
		engine.ServeHTTP(httptest.NewRecorder(), req)

		So(target, ShouldEqual, "bind-1,bind-2")
	})
}

// Test_CapabilityRoutes_Audited keeps mount and release on the operation-audit table. A binding
// change is a governance fact: without a rule here the write happens with no audit trail.
func Test_CapabilityRoutes_Audited(t *testing.T) {
	Convey("挂载与解绑登记进操作审计", t, func() {
		for _, tc := range []struct {
			method, path, action string
		}{
			{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/:kn_id/capabilities", "attach"},
			{http.MethodDelete, "/api/ontology-manager/v1/knowledge-networks/:kn_id/capabilities/:binding_ids", "detach"},
		} {
			rule, ok := registeredOperationAudit(tc.method, tc.path, "")
			So(ok, ShouldBeTrue)
			So(rule.Action, ShouldEqual, tc.action)
			So(rule.TargetType, ShouldEqual, "kn_capability_binding")
		}
	})
}

func Test_ListCapabilities_KNReadAccess(t *testing.T) {
	Convey("The capability list honors knowledge-network read access", t, func() {
		restore := setGinMode()
		defer restore()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		kns := bmock.NewMockKNService(ctrl)
		cbs := bmock.NewMockCapabilityBindingService(ctrl)
		handler := &restHandler{kns: kns, cbs: cbs}

		serve := func() *httptest.ResponseRecorder {
			engine := gin.New()
			engine.GET("/knowledge-networks/:kn_id/capabilities", func(c *gin.Context) {
				handler.ListCapabilities(c, hydra.Visitor{ID: "user-1", Type: hydra.VisitorType_User})
			})
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
				"/knowledge-networks/kn1/capabilities?branch=main", nil))
			return w
		}

		Convey("Navigation-only access is passed to the list service", func() {
			kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", "main").Return("Network 1", true, nil)
			kns.EXPECT().ResolveKNReadAccess(gomock.Any(), "kn1", "main").
				Return(interfaces.KN_READ_ACCESS_NAVIGATION_ONLY, nil)
			cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, query interfaces.CapabilityBindingsQueryParams) (*interfaces.CapabilityBindingsList, error) {
					So(query.ReadAccessMode, ShouldEqual, interfaces.KN_READ_ACCESS_NAVIGATION_ONLY)
					return &interfaces.CapabilityBindingsList{
						Entries: make([]*interfaces.CapabilityBinding, 0), Boxes: make([]*interfaces.CapabilityBoxSummary, 0),
						MetadataAvailable: true, SourcesAvailable: true,
					}, nil
				})

			w := serve()

			So(w.Code, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"boxes":[]`)
			var body interfaces.CapabilityBindingsList
			So(json.Unmarshal(w.Body.Bytes(), &body), ShouldBeNil)
			So(body.Entries, ShouldNotBeNil)
			So(body.Entries, ShouldBeEmpty)
			So(body.TotalCount, ShouldEqual, 0)
			So(body.Boxes, ShouldNotBeNil)
			So(body.Boxes, ShouldBeEmpty)
		})

		Convey("A permission dependency failure remains an error", func() {
			kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", "main").Return("Network 1", true, nil)
			kns.EXPECT().ResolveKNReadAccess(gomock.Any(), "kn1", "main").Return(interfaces.KNReadAccessMode(""),
				rest.NewHTTPError(context.Background(), http.StatusServiceUnavailable, rest.PublicError_ServiceUnavailable))

			w := serve()

			So(w.Code, ShouldEqual, http.StatusServiceUnavailable)
		})
	})
}

func Test_AttachCapabilities_ResponseArrayContract(t *testing.T) {
	Convey("The attach response serializes boxes as an empty array", t, func() {
		restore := setGinMode()
		defer restore()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		kns := bmock.NewMockKNService(ctrl)
		cbs := bmock.NewMockCapabilityBindingService(ctrl)
		handler := &restHandler{kns: kns, cbs: cbs}
		engine := gin.New()
		engine.POST("/knowledge-networks/:kn_id/capabilities", func(c *gin.Context) {
			handler.AttachCapabilities(c, hydra.Visitor{ID: "user-1", Type: hydra.VisitorType_User})
		})

		kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", "main").Return("Network 1", true, nil)
		cbs.EXPECT().AttachCapabilities(gomock.Any(), nil, "kn1", "main", gomock.Any()).
			Return([]*interfaces.CapabilityBinding{{
				ID: "bind-1", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1",
			}}, nil)

		request := httptest.NewRequest(http.MethodPost, "/knowledge-networks/kn1/capabilities?branch=main",
			strings.NewReader(`{"capabilities":[{"capability_type":"skill","capability_id":"skill-1"}]}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)

		So(response.Code, ShouldEqual, http.StatusOK)
		So(response.Body.String(), ShouldContainSubstring, `"boxes":[]`)
		So(response.Body.String(), ShouldNotContainSubstring, `"boxes":null`)
		var body interfaces.CapabilityBindingsList
		So(json.Unmarshal(response.Body.Bytes(), &body), ShouldBeNil)
		So(body.Boxes, ShouldNotBeNil)
		So(body.Boxes, ShouldBeEmpty)
	})
}
