package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func newTestServer(t *testing.T) (*gin.Engine, *authz.Enforcer, *gorm.DB) {
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
	r := New(Deps{Enforcer: e, DB: db, Directory: directory.New(db)})
	return r, e, db
}

func do(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHealth(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := do(t, r, http.MethodGet, "/health/ready", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health = %d", w.Code)
	}
}

func TestAuthzCheckEndpoint(t *testing.T) {
	r, e, _ := newTestServer(t)
	// grant app-admin agent:* use, bind user.
	const role, user = "role-app", "u-1"
	_ = e.GrantRolePermission(role, "agent", "*", "use")
	_ = e.AssignRole(user, role)

	body := map[string]any{
		"accessor_id": user,
		"resource":    map[string]string{"type": "agent", "id": "probe"},
		"operation":   "use",
	}
	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", body)
	if w.Code != http.StatusOK {
		t.Fatalf("check = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Allowed bool `json:"allowed"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Allowed {
		t.Error("expected allowed=true")
	}

	// a denied op
	body["operation"] = "delete"
	w = do(t, r, http.MethodPost, "/api/safe/v1/authz/check", body)
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Allowed {
		t.Error("expected allowed=false for delete")
	}
}

func TestDirectoryNamesEndpoint(t *testing.T) {
	r, _, db := newTestServer(t)
	db.Create(&model.User{ID: "u1", Account: "alice", Name: "Alice", Enabled: true})

	w := do(t, r, http.MethodPost, "/api/safe/v1/directory/names",
		map[string]any{"user_ids": []string{"u1", "ghost"}})
	if w.Code != http.StatusOK {
		t.Fatalf("names = %d", w.Code)
	}
	var resp struct {
		UserNames []directory.NamedRef `json:"user_names"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.UserNames) != 1 || resp.UserNames[0].Name != "Alice" {
		t.Errorf("user_names = %v", resp.UserNames)
	}
}

func TestSelfServiceChangePassword(t *testing.T) {
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
	ctx := t.Context()
	u := &model.User{ID: "u-cp", Account: "erin", Enabled: true, MustChangePassword: true}
	if err := users.CreateLocalUser(ctx, u, "initial0"); err != nil {
		t.Fatal(err)
	}
	r := New(Deps{Enforcer: e, DB: db, Users: users})
	const path = "/api/safe/v1/auth/change-password"

	// wrong old password -> 401
	w := do(t, r, http.MethodPost, path, map[string]string{"account": "erin", "old_password": "nope", "new_password": "brandnew1"})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong old: want 401, got %d (%s)", w.Code, w.Body.String())
	}
	// new == old -> 400
	w = do(t, r, http.MethodPost, path, map[string]string{"account": "erin", "old_password": "initial0", "new_password": "initial0"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("new==old: want 400, got %d", w.Code)
	}
	// success -> 204, new password works, flag cleared
	w = do(t, r, http.MethodPost, path, map[string]string{"account": "erin", "old_password": "initial0", "new_password": "brandnew1"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("change: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	got, err := users.Verify(ctx, "erin", "brandnew1")
	if err != nil {
		t.Fatalf("verify new: %v", err)
	}
	if got.MustChangePassword {
		t.Error("change-password must clear MustChangePassword")
	}
}

func TestAuthzBadRequest(t *testing.T) {
	r, _, _ := newTestServer(t)
	// missing required fields -> 400
	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", map[string]any{"accessor_id": "x"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
