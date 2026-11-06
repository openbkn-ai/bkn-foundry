// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/workshop/server/locale"
)

func init() {
	gin.SetMode(gin.TestMode)
	locale.Register()
}

type fakePinger struct{ err error }

func (p fakePinger) PingContext(context.Context) error { return p.err }

type fakeAuth struct {
	visitor hydra.Visitor
	err     error
}

func (a fakeAuth) VerifyToken(context.Context, *gin.Context) (hydra.Visitor, error) {
	return a.visitor, a.err
}

func serve(engine *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestProbes(t *testing.T) {
	engine := gin.New()
	RegisterBase(engine, fakePinger{})
	if w := serve(engine, PublicPrefix+"/health"); w.Code != http.StatusOK {
		t.Fatalf("health = %d", w.Code)
	}
	if w := serve(engine, PublicPrefix+"/ready"); w.Code != http.StatusOK {
		t.Fatalf("ready = %d", w.Code)
	}

	down := gin.New()
	RegisterBase(down, fakePinger{err: errors.New("db down")})
	if w := serve(down, PublicPrefix+"/ready"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready with the database down = %d, want 503", w.Code)
	}
}

// An unlicensed paid binary must answer a gated route exactly as the community
// binary answers a route it does not have.
func TestRequireEditionMatchesAnUnmountedRoute(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(entitlement.ResetForTest)

	gated := gin.New()
	group := gated.Group(PublicPrefix, RequireEdition(licverify.EditionProfessional), rest.PrivateNoCacheMiddleware())
	group.GET("/apps", func(c *gin.Context) { c.String(http.StatusOK, "apps") })
	community := gin.New()

	got := serve(gated, PublicPrefix+"/apps")
	want := serve(community, PublicPrefix+"/apps")
	if got.Code != want.Code || got.Body.String() != want.Body.String() ||
		got.Header().Get("Content-Type") != want.Header().Get("Content-Type") ||
		got.Header().Get("Cache-Control") != "" {
		t.Fatalf("gated route = %d %q %v, want %d %q %v", got.Code, got.Body.String(), got.Header(),
			want.Code, want.Body.String(), want.Header())
	}

	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))
	if w := serve(gated, PublicPrefix+"/apps"); w.Code != http.StatusOK {
		t.Fatalf("licensed route = %d, want 200", w.Code)
	}
}

func TestAuthenticate(t *testing.T) {
	handle := func(c *gin.Context) {
		visitor, ok := VisitorFrom(c)
		if !ok {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.String(http.StatusOK, visitor.ID)
	}

	ok := gin.New()
	ok.GET("/x", Authenticate(fakeAuth{visitor: hydra.Visitor{ID: "u1"}}), handle)
	if w := serve(ok, "/x"); w.Code != http.StatusOK || w.Body.String() != "u1" {
		t.Fatalf("authenticated = %d %q", w.Code, w.Body.String())
	}

	denied := gin.New()
	denied.GET("/x", Authenticate(fakeAuth{err: errors.New("invalid token")}), handle)
	if w := serve(denied, "/x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("rejected token = %d, want 401", w.Code)
	}
}
