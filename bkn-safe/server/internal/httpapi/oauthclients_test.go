// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

const plainUserSub = "user-1"

type fakeConnectedClients struct {
	grants  map[string][]auth.ConnectedClient // subject -> clients
	clients []auth.ConnectedClient
	revoked [][2]string
	deleted []string
}

func (f *fakeConnectedClients) ListConnectedClients(_ context.Context, subject string) ([]auth.ConnectedClient, error) {
	return f.grants[subject], nil
}

func (f *fakeConnectedClients) RevokeConnectedClient(_ context.Context, subject, clientID string) (string, error) {
	name, ok := f.name(clientID)
	if !ok {
		return "", auth.ErrNotDynamicClient
	}
	f.revoked = append(f.revoked, [2]string{subject, clientID})
	return name, nil
}

func (f *fakeConnectedClients) ListDynamicClients(context.Context) ([]auth.ConnectedClient, error) {
	return f.clients, nil
}

func (f *fakeConnectedClients) DeleteDynamicClient(_ context.Context, clientID string) (string, error) {
	name, ok := f.name(clientID)
	if !ok {
		return "", auth.ErrNotDynamicClient
	}
	f.deleted = append(f.deleted, clientID)
	return name, nil
}

func (f *fakeConnectedClients) name(clientID string) (string, bool) {
	for _, c := range f.clients {
		if c.ClientID == clientID {
			return c.ClientName, true
		}
	}
	return "", false
}

func newConnectedClientsServer(t *testing.T) (*gin.Engine, *fakeConnectedClients) {
	r, fake, _ := newConnectedClientsServerWithDB(t)
	return r, fake
}

func newConnectedClientsServerWithDB(t *testing.T) (*gin.Engine, *fakeConnectedClients, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
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
	for _, id := range []string{adminSub, plainUserSub} {
		if err := db.Create(&model.User{ID: id, Account: id, Enabled: true}).Error; err != nil {
			t.Fatalf("create account %s: %v", id, err)
		}
	}
	granted := time.Date(2026, 10, 23, 9, 0, 24, 0, time.UTC)
	workBuddy := auth.ConnectedClient{
		ClientID: "wb-1", ClientName: "WorkBuddy Connector",
		RedirectURIs:   []string{"http://127.0.0.1:61503/oauth/callback"},
		RegisteredAt:   granted.Add(-time.Second),
		RegisteredFrom: "10.211.55.2",
	}
	grant := workBuddy
	grant.GrantedAt = granted
	grant.Scopes = []string{"openid", "offline"}
	fake := &fakeConnectedClients{
		grants:  map[string][]auth.ConnectedClient{plainUserSub: {grant}},
		clients: []auth.ConnectedClient{workBuddy},
	}
	router := New(Deps{
		Enforcer: e, DB: db, Directory: directory.New(db), Users: auth.NewUserStore(db),
		TokenVerifier:    stubVerifier{},
		ClientAdmin:      newFakeClientManager(),
		ConnectedClients: fake,
		Audit:            audit.New(db),
	})
	return router, fake, db
}

func TestUserListsOwnConnectedClients(t *testing.T) {
	r, _ := newConnectedClientsServer(t)
	w := tokReq(t, r, http.MethodGet, "/api/safe/v1/me/oauth/clients", nil, plainUserSub)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var body struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || body.Entries[0]["client_name"] != "WorkBuddy Connector" || body.Entries[0]["granted_at"] == nil {
		t.Fatalf("entries = %v", body.Entries)
	}
	if _, leaked := body.Entries[0]["registered_from"]; leaked {
		t.Fatalf("a user's own listing should not carry the registration address: %v", body.Entries[0])
	}

	// Another user sees nothing of it.
	w = tokReq(t, r, http.MethodGet, "/api/safe/v1/me/oauth/clients", nil, adminSub)
	if w.Code != http.StatusOK || !jsonContains(t, w.Body.Bytes(), `"total":0`) {
		t.Fatalf("other user: %d %s", w.Code, w.Body)
	}
}

func TestUserDisconnectsOnlyForThemselves(t *testing.T) {
	r, fake := newConnectedClientsServer(t)
	w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/oauth/clients/wb-1", nil, plainUserSub)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if len(fake.revoked) != 1 || fake.revoked[0] != [2]string{plainUserSub, "wb-1"} {
		t.Fatalf("revoked = %v", fake.revoked)
	}
}

func TestDisconnectRefusesFirstPartyClients(t *testing.T) {
	r, fake := newConnectedClientsServer(t)
	w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/oauth/clients/openbkn-studio", nil, plainUserSub)
	if w.Code != http.StatusNotFound || len(fake.revoked) != 0 {
		t.Fatalf("status %d, revoked %v", w.Code, fake.revoked)
	}
}

func TestConnectedClientsNeedSignIn(t *testing.T) {
	r, _ := newConnectedClientsServer(t)
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/me/oauth/clients", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
}

func TestAdminListsAndRemovesSelfRegisteredClients(t *testing.T) {
	r, fake := newConnectedClientsServer(t)
	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/oauth/clients", nil)
	if w.Code != http.StatusOK || !jsonContains(t, w.Body.Bytes(), `"registered_from":"10.211.55.2"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/oauth/clients/wb-1", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != "wb-1" {
		t.Fatalf("deleted = %v", fake.deleted)
	}
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/oauth/clients/openbkn-cli", nil); w.Code != http.StatusNotFound {
		t.Fatalf("first-party delete: status %d", w.Code)
	}
}

func TestAdminClientListNeedsManagePermission(t *testing.T) {
	r, fake := newConnectedClientsServer(t)
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/oauth/clients", nil, plainUserSub); w.Code != http.StatusForbidden {
		t.Fatalf("list as plain user: status %d", w.Code)
	}
	if w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/admin/oauth/clients/wb-1", nil, plainUserSub); w.Code != http.StatusForbidden {
		t.Fatalf("delete as plain user: status %d", w.Code)
	}
	if len(fake.deleted) != 0 {
		t.Fatalf("plain user deleted %v", fake.deleted)
	}
}

func jsonContains(t *testing.T, raw []byte, want string) bool {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("not json: %s", raw)
	}
	compact, _ := json.Marshal(v)
	return strings.Contains(string(compact), want)
}

// Regression: the audit middleware drops a record with no target name, so a
// disconnect or removal used to answer 204 and leave nothing in the audit log.
func TestDisconnectAndRemovalAreAuditedWithTheClientName(t *testing.T) {
	r, _, db := newConnectedClientsServerWithDB(t)
	if w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/oauth/clients/wb-1", nil, plainUserSub); w.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", w.Code, w.Body)
	}
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/oauth/clients/wb-1", nil); w.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	for _, action := range []string{"disconnect_client", "remove_client"} {
		var entry model.AuditLog
		if err := db.Where("resource = ? AND action = ?", "oauth", action).First(&entry).Error; err != nil {
			t.Fatalf("%s: no audit row: %v", action, err)
		}
		if entry.TargetID != "wb-1" || entry.TargetName != "OAuth client WorkBuddy Connector" {
			t.Fatalf("%s audit target = id=%q name=%q", action, entry.TargetID, entry.TargetName)
		}
	}
}

func TestAuditNameFallsBackToClientID(t *testing.T) {
	if got := auditClientName("c-9", ""); got != "OAuth client c-9" {
		t.Fatalf("auditClientName = %q", got)
	}
}
