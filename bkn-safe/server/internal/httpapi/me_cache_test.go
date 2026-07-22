// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/database"
	"bkn-safe/internal/directory"
	"bkn-safe/internal/model"
)

// revocableVerifier resolves any non-empty token to itself until Revoke flips
// it, after which every call fails — simulating a token revoked mid-session.
type revocableVerifier struct{ revoked atomic.Bool }

func (v *revocableVerifier) VerifyToken(_ context.Context, token string) (string, error) {
	if v.revoked.Load() || token == "" {
		return "", errors.New("revoked")
	}
	return token, nil
}

// TestMeMutatingEndpointsBypassCache proves the introspection cache is scoped to
// the READ-ONLY /me endpoints. After a token is revoked, GET /me/permissions may
// still succeed within the cache TTL, but mutating requests (POST /me/api-keys,
// PUT /me) must fail immediately because they run on the uncached verifier —
// otherwise a revoked token could mint a long-lived API key inside the cache
// window.
func TestMeMutatingEndpointsBypassCache(t *testing.T) {
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
	users := auth.NewUserStore(db)
	if err := users.CreateLocalUser(t.Context(),
		&model.User{ID: "u-k", Account: "uk", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}

	v := &revocableVerifier{}
	r := New(Deps{
		Enforcer: e, DB: db, Directory: directory.New(db), Users: users,
		TokenVerifier: v,
	})

	call := func(method, path, body string) int {
		var rdr *strings.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		var req *http.Request
		if rdr != nil {
			req = httptest.NewRequest(method, path, rdr)
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		req.Header.Set("Authorization", "Bearer u-k")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	// Warm the read cache for token u-k.
	if code := call(http.MethodGet, "/api/safe/v1/me/permissions", ""); code != http.StatusOK {
		t.Fatalf("warm read: want 200, got %d", code)
	}

	// Revoke the token.
	v.revoked.Store(true)

	// Read-only endpoint is still served from the cache within its TTL.
	if code := call(http.MethodGet, "/api/safe/v1/me/permissions", ""); code != http.StatusOK {
		t.Fatalf("cached read after revoke: want 200, got %d", code)
	}

	// Mutating endpoints must reject immediately — they run on the uncached
	// verifier, so a revoked token cannot mint a long-lived API key or edit the
	// profile inside the read cache window.
	if code := call(http.MethodPost, "/api/safe/v1/me/api-keys",
		`{"name":"k","never_expire":true}`); code != http.StatusUnauthorized {
		t.Fatalf("api-key create after revoke: want 401, got %d", code)
	}
	if code := call(http.MethodPut, "/api/safe/v1/me", `{"name":"x"}`); code != http.StatusUnauthorized {
		t.Fatalf("profile update after revoke: want 401, got %d", code)
	}
}
