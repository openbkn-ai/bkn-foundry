// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package adminwrite

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension"
)

// requireAuth stands in for RequireAdmin: it refuses anything without a
// credential, exactly as the real one does. The tests below mount the gate the
// way router.go does — in FRONT of it — because an earlier version of this file
// used a bare group with no middleware at all, and so proved a proposition that
// held only in that simplified wiring.
func requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		c.Next()
	}
}

// answer is everything an outside caller can observe.
type answer struct {
	code   int
	body   string
	header http.Header
}

func (a answer) equal(b answer) bool {
	if a.code != b.code || a.body != b.body || len(a.header) != len(b.header) {
		return false
	}
	for k, v := range a.header {
		if len(b.header[k]) != len(v) {
			return false
		}
		for i := range v {
			if b.header[k][i] != v[i] {
				return false
			}
		}
	}
	return true
}

// serve builds a router the way router.go does and returns what a probe sees.
//
// Over a real listener, not httptest.NewRecorder: the recorder reports the
// headers the handler happened to set, while a client sees what net/http
// actually put on the wire — which differs, notably for Content-Length. The
// property under test is what an outside prober can observe, so measure that.
func serve(t *testing.T, register func(), authHeader string) answer {
	t.Helper()
	gin.SetMode(gin.TestMode)
	resetForTest()
	extension.SetGateForTest(extension.GateFunc(func(extension.Feature) bool { return false }))

	r := gin.New()
	register()
	Mount(r.Group("/admin", Gate(), requireAuth()), nil)

	srv := httptest.NewServer(r)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	// Date changes between the two calls by construction; it is set by net/http
	// for every response and carries nothing about this binary.
	h := resp.Header.Clone()
	h.Del("Date")
	return answer{resp.StatusCode, string(body), h}
}

func gatedMounter() {
	RegisterMounterGated(extension.FeatureRBACBasic, func(g *gin.RouterGroup, _ Services) {
		g.POST("/x", func(c *gin.Context) { c.String(http.StatusOK, "served") })
	})
}

// The community answer is the reference. Compare the WHOLE response — an
// earlier version compared only status and body, and missed that the hand-built
// 404 carried "text/plain; charset=utf-8" where gin writes "text/plain".
func TestUnlicensedRouteIsIndistinguishableFromTheCommunityAnswer(t *testing.T) {
	for _, tc := range []struct{ name, auth string }{
		{"无凭据", ""},
		{"有凭据", "Bearer whatever"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			community := serve(t, func() {}, tc.auth)
			gated := serve(t, gatedMounter, tc.auth)
			if !gated.equal(community) {
				t.Fatalf("未授权的付费路由与社区应答不一致——付费面被暴露了\n社区  : %d %q %v\n未授权: %d %q %v",
					community.code, community.body, community.header,
					gated.code, gated.body, gated.header)
			}
			if gated.code != http.StatusNotFound {
				t.Fatalf("状态码 = %d，档位不足必须是 404（ee-design.md §4.5）", gated.code)
			}
		})
	}
}

// The unauthenticated case is the one that was broken: the gate sat behind
// RequireAdmin, so an enterprise binary answered 401 where a community one
// answers 404 — identifiable with no credential at all.
func TestGateRunsBeforeAuthentication(t *testing.T) {
	if got := serve(t, gatedMounter, "").code; got != http.StatusNotFound {
		t.Fatalf("无凭据探测 = %d，want 404。档位判定排到鉴权之后了", got)
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
	gatedMounter()
	Mount(r.Group("/admin", Gate(), requireAuth()), nil)

	call := func() int {
		req := httptest.NewRequest(http.MethodPost, "/admin/x", nil)
		req.Header.Set("Authorization", "Bearer whatever")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
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

// The legacy entry point keeps working: Gate() is a no-op, and the caller's own
// middleware decides. Without this, switching ee over would have to be atomic.
func TestLegacyRegisterMounterStillMounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetForTest()
	extension.SetGateForTest(extension.GateFunc(func(extension.Feature) bool { return false }))

	r := gin.New()
	RegisterMounter(func(g *gin.RouterGroup, _ Services) {
		g.POST("/x", func(c *gin.Context) { c.String(http.StatusOK, "served") })
	})
	Mount(r.Group("/admin", Gate(), requireAuth()), nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/x", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy 入口 = %d，want 200——它自己管门控，插座不该插手", w.Code)
	}
}

// resetForTest must not leak assembly state into the next test.
func TestResetClearsTheGatedFeature(t *testing.T) {
	resetForTest()
	gatedMounter()
	if gatedOn == "" {
		t.Fatal("前置条件不成立")
	}
	resetForTest()
	if gatedOn != "" {
		t.Fatal("resetForTest 没清 gatedOn——下一个测试会继承上一个的装配")
	}
}
