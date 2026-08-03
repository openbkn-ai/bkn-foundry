// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
)

// routerWithMounter builds the REAL router — the one New() assembles, with
// RequireAdmin and the audit middleware — around whatever mounter is given.
//
// The adminwrite package's own tests build a router by hand, so they cannot
// catch an ordering mistake in New(): they proved the socket refuses correctly
// when it is wired first, not that anything wires it first. That is precisely
// how the licence gate ended up behind RequireAdmin. This test uses New().
func routerWithMounter(t *testing.T, register func()) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	e, err := authz.New(db)
	if err != nil {
		t.Fatalf("authz: %v", err)
	}
	adminwrite.ResetForTest()
	register()
	return New(Deps{
		Enforcer: e, DB: db, Directory: directory.New(db), Users: auth.NewUserStore(db),
		Audit: audit.New(db), TokenVerifier: stubVerifier{},
	})
}

// An unauthenticated probe must not be able to tell the two binaries apart.
// This is the case that was broken: with the gate behind RequireAdmin the
// enterprise build answered 401 where the community build answers 404, so the
// paid surface was identifiable with no credential at all.
func TestUnlicensedWriteRouteIsHiddenFromAnUnauthenticatedProbe(t *testing.T) {
	extension.SetGateForTest(extension.GateFunc(func(extension.Feature) bool { return false }))

	community := routerWithMounter(t, func() {})
	enterprise := routerWithMounter(t, func() {
		adminwrite.RegisterMounterGated(extension.FeatureRBACBasic, adminwrite.Routes)
	})

	probe := func(r *gin.Engine) (int, string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/safe/v1/admin/roles", nil))
		return w.Code, w.Body.String()
	}

	wantCode, wantBody := probe(community)
	gotCode, gotBody := probe(enterprise)

	if gotCode != wantCode || gotBody != wantBody {
		t.Fatalf("无凭据探测能区分社区与无证企业——付费面被暴露了\n社区: %d %q\n企业: %d %q\n"+
			"多半是 New() 把 adminwrite.Gate() 排到了 RequireAdmin 之后",
			wantCode, wantBody, gotCode, gotBody)
	}
	if gotCode != http.StatusNotFound {
		t.Fatalf("状态码 = %d，want 404", gotCode)
	}
}
