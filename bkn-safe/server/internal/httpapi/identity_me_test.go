// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/federation"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// An extension's /me reads go through the router's own /me read group: a token
// is required, a disabled account is turned away even with a valid one, and the
// extension reads the signed-in caller.
func TestIdentityMeReadsKeepCoreAuthentication(t *testing.T) {
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
	for _, id := range []string{"u-on", "u-off"} {
		if err := db.Create(&model.User{ID: id, Account: id, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&model.User{}).Where("id = ?", "u-off").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	server := func(ext *federation.Extension) *gin.Engine {
		return New(Deps{Enforcer: e, DB: db, Directory: directory.New(db), Users: auth.NewUserStore(db),
			TokenVerifier: stubVerifier{}, IdentityProvider: ext})
	}
	extension := &federation.Extension{MountMe: func(get func(string, gin.HandlerFunc), caller func(*gin.Context) string) {
		get("/external-identities", func(c *gin.Context) { c.String(http.StatusOK, caller(c)) })
	}}
	for _, tc := range []struct {
		name, token string
		ext         *federation.Extension
		status      int
		body        string
	}{
		{name: "community adds no route", token: "u-on", status: 404},
		{name: "missing token", ext: extension, status: 401},
		{name: "inactive token", token: "bad", ext: extension, status: 401},
		{name: "disabled account with a valid token", token: "u-off", ext: extension, status: 401},
		{name: "signed-in caller", token: "u-on", ext: extension, status: 200, body: "u-on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/safe/v1/me/external-identities", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			server(tc.ext).ServeHTTP(w, req)
			if w.Code != tc.status || (tc.body != "" && w.Body.String() != tc.body) {
				t.Fatalf("status=%d body=%q, want %d %q", w.Code, w.Body.String(), tc.status, tc.body)
			}
		})
	}
}
