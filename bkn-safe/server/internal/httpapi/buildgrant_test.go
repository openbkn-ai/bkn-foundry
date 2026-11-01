// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/buildgrant"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

type buildPermission struct {
	err     error
	allowed bool
}

func (p *buildPermission) CheckContext(context.Context, string, string, string, string) (bool, error) {
	return p.allowed, p.err
}
func TestBuildGrantHTTPDoesNotAcceptScopeEscapeOrRevokedOwner(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.User{ID: "owner", Account: "owner", Enabled: true, AccountType: model.AccountTypeOther})
	p := &buildPermission{allowed: true}
	r := gin.New()
	registerBuildGrants(r, buildgrant.New(db, p))
	req := buildgrant.CreateRequest{ID: "grant-1", ProjectID: "project-1", BuildID: "build-1", DelegatorID: "owner", ModelFingerprint: strings.Repeat("a", 64), Versions: []buildgrant.Version{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8"}}}
	w := do(t, r, http.MethodPost, "/api/safe/in/v1/build-grants", req)
	if w.Code != 201 {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var g buildgrant.Grant
	if err = json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	check := buildgrant.CheckRequest{GrantID: g.ID, ProjectID: g.ProjectID, BuildID: g.BuildID, PrincipalID: g.PrincipalID, Revision: g.Revision, Action: buildgrant.ActionRead, Versions: req.Versions}
	for _, scenario := range []string{"ok", "wrong_version", "revoked_owner", "dependency_down", "wrong_principal"} {
		changed := check
		p.allowed = true
		p.err = nil
		expected := 200
		switch scenario {
		case "wrong_version":
			changed.Versions = []buildgrant.Version{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "99"}}
			expected = 403
		case "revoked_owner":
			p.allowed = false
			expected = 403
		case "dependency_down":
			p.err = errors.New("do not echo this dependency response")
			expected = 503
		case "wrong_principal":
			changed.PrincipalID = "owner"
			expected = 403
		}
		w = do(t, r, http.MethodPost, "/api/safe/in/v1/build-grants/check", changed)
		if w.Code != expected || strings.Contains(w.Body.String(), "do not echo") {
			t.Fatalf("%s status=%d body=%s", scenario, w.Code, w.Body.String())
		}
	}
	w = do(t, r, http.MethodDelete, "/api/safe/in/v1/build-grants/grant-1", map[string]string{"project_id": "project-1", "build_id": "build-1"})
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	check.Action = buildgrant.ActionCleanup
	check.Versions = nil
	w = do(t, r, http.MethodPost, "/api/safe/in/v1/build-grants/check", check)
	if w.Code != 200 {
		t.Fatal("revocation prevents cleanup", w.Code)
	}
	w = do(t, r, http.MethodPost, "/api/safe/in/v1/build-grants/grant-1/cleanup:complete", map[string]string{"project_id": "project-1", "build_id": "build-1"})
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = do(t, r, http.MethodPost, "/api/safe/in/v1/build-grants", req)
	if w.Code != 409 {
		t.Fatal("late create restored cancelled grant", w.Code)
	}
}
