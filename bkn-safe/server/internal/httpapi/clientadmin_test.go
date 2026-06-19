package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"bkn-safe/internal/audit"
	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/database"
	"bkn-safe/internal/directory"
)

// fakeClientManager is an in-memory ClientManager (clientID -> redirect_uris) so
// the client-admin API can be tested without a live hydra.
type fakeClientManager struct {
	uris map[string][]string
}

func newFakeClientManager() *fakeClientManager {
	return &fakeClientManager{uris: map[string][]string{
		"openbkn-studio": {"https://host/callback"},
	}}
}

func (f *fakeClientManager) GetClientRedirectURIs(_ context.Context, id string) ([]string, error) {
	return f.uris[id], nil
}

func (f *fakeClientManager) AddClientRedirectURI(_ context.Context, id, uri string) ([]string, error) {
	for _, u := range f.uris[id] {
		if u == uri {
			return f.uris[id], nil // idempotent
		}
	}
	f.uris[id] = append(f.uris[id], uri)
	return f.uris[id], nil
}

func (f *fakeClientManager) RemoveClientRedirectURI(_ context.Context, id, uri string) ([]string, error) {
	out := make([]string, 0, len(f.uris[id]))
	for _, u := range f.uris[id] {
		if u != uri {
			out = append(out, u)
		}
	}
	f.uris[id] = out
	return out, nil
}

// newClientAdminServer builds a server with the client-admin API mounted on top of
// a fake ClientManager, with adminSub seeded as super-admin (Bearer adminSub passes
// RequireAdmin).
func newClientAdminServer(t *testing.T) (*gin.Engine, *fakeClientManager) {
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
	if err := e.Grant(adminSub, "*", "*"); err != nil {
		t.Fatalf("grant super-admin: %v", err)
	}
	fake := newFakeClientManager()
	r := New(Deps{
		Enforcer: e, DB: db, Directory: directory.New(db), Users: auth.NewUserStore(db),
		Audit:         audit.New(db),
		TokenVerifier: stubVerifier{},
		ClientAdmin:   fake,
	})
	return r, fake
}

// redirectURIs decodes the { "redirect_uris": [...] } body.
func redirectURIs(t *testing.T, body []byte) []string {
	t.Helper()
	var out struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}
	return out.RedirectURIs
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func TestClientRedirectAuthGate(t *testing.T) {
	r, _ := newClientAdminServer(t)
	const path = "/api/safe/v1/admin/clients/openbkn-studio/redirect-uris"

	if w := tokReq(t, r, http.MethodGet, path, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", w.Code)
	}
	if w := tokReq(t, r, http.MethodGet, path, nil, "bad"); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: want 401, got %d", w.Code)
	}
	// valid token, but not a super-admin -> 403
	if w := tokReq(t, r, http.MethodGet, path, nil, "user-2"); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin: want 403, got %d", w.Code)
	}
}

func TestClientRedirectAddListDelete(t *testing.T) {
	r, _ := newClientAdminServer(t)
	const path = "/api/safe/v1/admin/clients/openbkn-studio/redirect-uris"
	const uri = "http://localhost:8000/studio/callback"

	// add
	w := adminReq(t, r, http.MethodPost, path, map[string]string{"redirect_uri": uri})
	if w.Code != http.StatusOK {
		t.Fatalf("add: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(redirectURIs(t, w.Body.Bytes()), uri) {
		t.Fatalf("add: %q not in result %v", uri, redirectURIs(t, w.Body.Bytes()))
	}

	// add again -> idempotent (length unchanged)
	w = adminReq(t, r, http.MethodPost, path, map[string]string{"redirect_uri": uri})
	got := redirectURIs(t, w.Body.Bytes())
	if w.Code != http.StatusOK || len(got) != 2 {
		t.Fatalf("re-add: want 200 & len 2, got %d & %v", w.Code, got)
	}

	// list
	w = adminReq(t, r, http.MethodGet, path, nil)
	if w.Code != http.StatusOK || !contains(redirectURIs(t, w.Body.Bytes()), uri) {
		t.Fatalf("list: want uri present, got %d & %v", w.Code, redirectURIs(t, w.Body.Bytes()))
	}

	// delete
	w = adminReq(t, r, http.MethodDelete, path, map[string]string{"redirect_uri": uri})
	if w.Code != http.StatusOK || contains(redirectURIs(t, w.Body.Bytes()), uri) {
		t.Fatalf("delete: want uri gone, got %d & %v", w.Code, redirectURIs(t, w.Body.Bytes()))
	}
}

func TestClientRedirectWhitelist(t *testing.T) {
	r, _ := newClientAdminServer(t)
	const path = "/api/safe/v1/admin/clients/some-third-party/redirect-uris"

	if w := adminReq(t, r, http.MethodGet, path, nil); w.Code != http.StatusForbidden {
		t.Fatalf("GET non-whitelisted: want 403, got %d", w.Code)
	}
	if w := adminReq(t, r, http.MethodPost, path, map[string]string{"redirect_uri": "https://h/cb"}); w.Code != http.StatusForbidden {
		t.Fatalf("POST non-whitelisted: want 403, got %d", w.Code)
	}
}

func TestClientRedirectValidation(t *testing.T) {
	r, _ := newClientAdminServer(t)
	const path = "/api/safe/v1/admin/clients/openbkn-studio/redirect-uris"

	bad := []string{
		"not-a-url",
		"ftp://host/cb",
		"https://host/cb#frag",
		"https://*.host/cb",
		"/just/a/path",
	}
	for _, uri := range bad {
		w := adminReq(t, r, http.MethodPost, path, map[string]string{"redirect_uri": uri})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("uri %q: want 400, got %d: %s", uri, w.Code, w.Body.String())
		}
	}

	// missing field -> 400
	if w := adminReq(t, r, http.MethodPost, path, map[string]string{}); w.Code != http.StatusBadRequest {
		t.Fatalf("missing redirect_uri: want 400, got %d", w.Code)
	}
}
