// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/smartystreets/goconvey/convey"

	"bkn-backend/common"
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
