// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package adminwrite

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension"
)

// probe drives POST /x through a router built the way the server builds it, and
// reports what an outside caller sees.
func probe(t *testing.T, register func()) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	resetForTest()
	extension.SetGateForTest(extension.GateFunc(func(f extension.Feature) bool { return false }))

	r := gin.New()
	register()
	Mount(r.Group("/admin"), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/x", nil))
	return w.Code, w.Body.String()
}

// The community answer is the reference: nothing registered, so gin's own
// not-found handler replies. Every other unlicensed answer has to equal it.
func TestUnlicensedGatedRouteIsByteIdenticalToTheCommunityAnswer(t *testing.T) {
	communityCode, communityBody := probe(t, func() {})

	gatedCode, gatedBody := probe(t, func() {
		RegisterMounterGated(extension.FeatureRBACBasic, func(g *gin.RouterGroup, _ Services) {
			g.POST("/x", func(c *gin.Context) { c.String(http.StatusOK, "served") })
		})
	})

	if gatedCode != communityCode || gatedBody != communityBody {
		t.Fatalf("未授权的付费路由与社区应答不一致——付费面被暴露了\n社区  : %d %q\n未授权: %d %q",
			communityCode, communityBody, gatedCode, gatedBody)
	}
	if gatedCode != http.StatusNotFound {
		t.Fatalf("状态码 = %d，档位不足必须是 404（ee-design.md §4.5）", gatedCode)
	}
}

// The licence is judged per request, so installing one activates routes that
// were registered while unlicensed — no restart, no re-registration.
func TestLicenceInstalledAfterAssemblyActivatesTheRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetForTest()
	on := false
	extension.SetGateForTest(extension.GateFunc(func(f extension.Feature) bool {
		return on && f == extension.FeatureRBACBasic
	}))

	r := gin.New()
	RegisterMounterGated(extension.FeatureRBACBasic, func(g *gin.RouterGroup, _ Services) {
		g.POST("/x", func(c *gin.Context) { c.String(http.StatusOK, "served") })
	})
	Mount(r.Group("/admin"), nil)

	call := func() int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/x", nil))
		return w.Code
	}

	if got := call(); got != http.StatusNotFound {
		t.Fatalf("装证前 = %d，want 404", got)
	}
	on = true
	if got := call(); got != http.StatusOK {
		t.Fatalf("装证后 = %d，want 200——证书必须无需重启即生效", got)
	}
	on = false
	if got := call(); got != http.StatusNotFound {
		t.Fatalf("撤证后 = %d，want 404——降档必须同样即时", got)
	}
}
