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
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// community installs a gate that grants nothing — the tier of an enterprise
// binary with no certificate, and of a community one always.
func community(t *testing.T) {
	t.Helper()
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Edition: licverify.EditionCommunity}
	}))
}

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
	community(t)

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
	RegisterMounter(licverify.EditionProfessional, func(g *gin.RouterGroup, _ Services) {
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

// The tier is judged per request, so installing a certificate activates routes
// that were registered while unlicensed — no restart, no re-registration.
func TestLicenceInstalledAfterAssemblyActivatesTheRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetForTest()
	edition := licverify.EditionCommunity
	entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
		return entitlement.Snapshot{Edition: edition, Licensed: edition != licverify.EditionCommunity}
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
	edition = licverify.EditionProfessional
	if got := call(); got != http.StatusOK {
		t.Fatalf("装证后 = %d，want 200——证书必须无需重启即生效", got)
	}
	edition = licverify.EditionCommunity
	if got := call(); got != http.StatusNotFound {
		t.Fatalf("撤证后 = %d，want 404——降档必须同样即时", got)
	}
}

// A tier above the declared minimum must pass. Without this, an enterprise or
// industry customer would lose a professional capability by paying more —
// which is what an == comparison does and AtLeast exists to prevent
// (ee-design.md §3.1).
func TestHigherTiersInheritTheCapability(t *testing.T) {
	for _, ed := range []licverify.Edition{
		licverify.EditionProfessional,
		licverify.EditionEnterprise,
		licverify.EditionIndustry,
	} {
		t.Run(string(ed), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			resetForTest()
			entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
				return entitlement.Snapshot{Edition: ed, Licensed: true}
			}))

			r := gin.New()
			gatedMounter()
			Mount(r.Group("/admin", Gate(), requireAuth()), nil)

			req := httptest.NewRequest(http.MethodPost, "/admin/x", nil)
			req.Header.Set("Authorization", "Bearer whatever")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("%s = %d，want 200——上层档位必须继承下层能力", ed, w.Code)
			}
		})
	}
}

// A capability registered without a tier would be a paid capability registered
// as free. The socket delegates that check to MarkAssembled; this test pins
// that it is in fact reached, so removing the delegation cannot pass silently.
func TestRegisterWithoutATierPanics(t *testing.T) {
	resetForTest()
	community(t)
	defer func() {
		if recover() == nil {
			t.Fatal("零值 MinEdition 必须 panic——否则付费能力会被登记成免费的")
		}
	}()
	RegisterMounter("", func(*gin.RouterGroup, Services) {})
}

// A second mounter is an assembly bug: the socket holds exactly one, so the
// later registration would replace the earlier one and the write routes served
// would not be the ones the assembly intended.
//
// The guard has been here all along; the test has not. entitlement.MarkAssembled
// is idempotent by name and catches nothing here, so this is the socket's own
// invariant to keep — and the equivalent guard in permobject was lost exactly
// this way, by being deleted along with the package that used to hold it.
func TestSecondMounterPanics(t *testing.T) {
	resetForTest()
	community(t)
	gatedMounter()

	defer func() {
		if recover() == nil {
			t.Fatal("第二个 mounter 必须 panic——否则先注册的写路由被静默替换")
		}
	}()
	gatedMounter()
}

// Registering must also put the capability in the process-wide assembly table.
//
// This is the bug that shipped: the socket recorded its mounter and its tier
// but never registered the capability, so an enterprise binary carrying the
// write routes reported extensions:[] — the capabilities endpoint could not
// tell it apart from a community image, and support had no way to distinguish
// "wrong image" from "wrong certificate".
func TestRegisteringPutsTheCapabilityInTheAssemblyTable(t *testing.T) {
	resetForTest()
	community(t)
	gatedMounter()

	for _, cap := range entitlement.Assembled() {
		if cap.Name != Capability {
			continue
		}
		if cap.MinEdition != licverify.EditionProfessional {
			t.Fatalf("MinEdition = %q, want professional——档位必须一并登记，运维才知道补哪张证", cap.MinEdition)
		}
		return
	}
	t.Fatalf("%q 不在装配表里：%v——企业镜像会被报成社区镜像", Capability, entitlement.Assembled())
}

// resetForTest must not leak assembly state into the next test.
func TestResetClearsTheDeclaredTier(t *testing.T) {
	resetForTest()
	community(t)
	gatedMounter()
	if minEdition == "" {
		t.Fatal("前置条件不成立")
	}
	resetForTest()
	if minEdition != "" {
		t.Fatal("resetForTest 没清 minEdition——下一个测试会继承上一个的装配")
	}
}
