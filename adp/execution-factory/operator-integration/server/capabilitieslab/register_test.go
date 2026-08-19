// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capabilitieslab

import (
	"fmt"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/smartystreets/goconvey/convey"
)

// expectedRoutes is all the routes exposed by the original capabilities-lab independent service, copied one by one from before the merger.
// handler.RegisterRoutes.
//
// The only regression aspect of the merger is "whether the route is registered as it is" - the entire internal migration is implemented without any changes, so as long as.
// This set remains unchanged, and the interface seen by the consumer remains unchanged. Any additions or deletions should be explicit: when changing this table.
// Explain the impact on the consumer side.
var expectedRoutes = []string{
	"DELETE /api/capabilities-lab/v1/capabilities/:id",
	"GET /api/capabilities-lab/v1/capabilities",
	"GET /api/capabilities-lab/v1/capabilities/:id",
	"GET /api/capabilities-lab/v1/capabilities/:id/export",
	"GET /api/capabilities-lab/v1/capabilities/:id/mcp/tools",
	"GET /api/capabilities-lab/v1/capabilities/:id/orchestration",
	"GET /api/capabilities-lab/v1/capabilities/:id/skill/content",
	"GET /api/capabilities-lab/v1/capabilities/:id/skill/download",
	"GET /api/capabilities-lab/v1/capabilities/:id/versions",
	"GET /api/capabilities-lab/v1/catalog",
	"GET /api/capabilities-lab/v1/categories",
	"GET /api/capabilities-lab/v1/groups",
	"GET /api/capabilities-lab/v1/health",
	"GET /api/capabilities-lab/v1/meta",
	"GET /api/capabilities-lab/v1/metrics",
	"GET /api/capabilities-lab/v1/template/python",
	"PATCH /api/capabilities-lab/v1/capabilities/:id",
	"POST /api/capabilities-lab/v1/capabilities/:id/debug",
	"POST /api/capabilities-lab/v1/capabilities/:id/orchestration/config",
	"POST /api/capabilities-lab/v1/capabilities/:id/orchestration/disable",
	"POST /api/capabilities-lab/v1/capabilities/:id/orchestration/enable",
	"POST /api/capabilities-lab/v1/capabilities/:id/publish",
	"POST /api/capabilities-lab/v1/capabilities/:id/skill/files/read",
	"POST /api/capabilities-lab/v1/capabilities/:id/versions/republish",
	"POST /api/capabilities-lab/v1/capabilities/function",
	"POST /api/capabilities-lab/v1/capabilities/http",
	"POST /api/capabilities-lab/v1/capabilities/http/import",
	"POST /api/capabilities-lab/v1/capabilities/import",
	"POST /api/capabilities-lab/v1/capabilities/mcp",
	"POST /api/capabilities-lab/v1/capabilities/mcp/parse-sse",
	"POST /api/capabilities-lab/v1/capabilities/skill",
	"POST /api/capabilities-lab/v1/catalog/install",
	"POST /api/capabilities-lab/v1/function/execute",
	"POST /api/capabilities-lab/v1/groups/:group_id/publish",
	"PUT /api/capabilities-lab/v1/capabilities/:id/skill/package",
}

func registeredRoutes(t *testing.T) []string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterRouter(engine.Group("/api/capabilities-lab/v1"))

	routes := make([]string, 0, len(engine.Routes()))
	for _, r := range engine.Routes() {
		routes = append(routes, fmt.Sprintf("%s %s", r.Method, r.Path))
	}
	sort.Strings(routes)
	return routes
}

func TestRegisterRouterPreservesRouteSurface(t *testing.T) {
	Convey("合并后的能力面路由与合并前逐条一致", t, func() {
		actual := registeredRoutes(t)
		expected := append([]string(nil), expectedRoutes...)
		sort.Strings(expected)

		Convey("路由条数不变", func() {
			So(len(actual), ShouldEqual, len(expected))
		})

		Convey("没有丢失路由", func() {
			registered := make(map[string]bool, len(actual))
			for _, r := range actual {
				registered[r] = true
			}
			for _, want := range expected {
				So(registered[want], ShouldBeTrue)
			}
		})

		Convey("没有多出未声明的路由", func() {
			declared := make(map[string]bool, len(expected))
			for _, r := range expected {
				declared[r] = true
			}
			for _, got := range actual {
				So(declared[got], ShouldBeTrue)
			}
		})

		Convey("路径前缀全部落在 /api/capabilities-lab/v1 下", func() {
			for _, r := range actual {
				So(r, ShouldContainSubstring, "/api/capabilities-lab/v1")
			}
		})
	})
}

func TestRegisterRouterIsIdempotentPerGroup(t *testing.T) {
	Convey("在独立的路由组上重复装配不会相互影响", t, func() {
		// Main anti-regression point: After the middleware is changed from engine level to group level mounting, the middleware on the same engine.
		// Other routing groups should not be affected by capabilities-lab's middleware chain.
		gin.SetMode(gin.TestMode)
		engine := gin.New()

		other := engine.Group("/api/agent-operator-integration/v1")
		other.GET("/probe", func(c *gin.Context) { c.Status(204) })

		RegisterRouter(engine.Group("/api/capabilities-lab/v1"))

		var probeFound bool
		for _, r := range engine.Routes() {
			if r.Path == "/api/agent-operator-integration/v1/probe" {
				probeFound = true
			}
		}
		So(probeFound, ShouldBeTrue)
		So(len(engine.Routes()), ShouldEqual, len(expectedRoutes)+1)
	})
}
