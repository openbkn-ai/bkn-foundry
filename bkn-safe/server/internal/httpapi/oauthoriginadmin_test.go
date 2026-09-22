// Copyright openbkn.ai
//
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
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/oauthorigin"
)

type fakeOAuthClient struct {
	uris   auth.OAuthClientURIs
	getErr error
}

func (f *fakeOAuthClient) GetOAuthClientURIs(context.Context, string) (auth.OAuthClientURIs, error) {
	if f.getErr != nil {
		return auth.OAuthClientURIs{}, f.getErr
	}
	return f.uris, nil
}

func (f *fakeOAuthClient) SetOAuthClientURIs(_ context.Context, _ string, uris auth.OAuthClientURIs) error {
	f.uris = uris
	return nil
}

func newAccessOriginAdminServer(t *testing.T) (*gin.Engine, *fakeOAuthClient) {
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
	if err := db.Create(&model.User{ID: adminSub, Account: adminSub, Enabled: true}).Error; err != nil {
		t.Fatalf("create admin account: %v", err)
	}
	client := &fakeOAuthClient{}
	manager, err := oauthorigin.New(db, client, []string{"https://public.example/studio/callback"})
	if err != nil {
		t.Fatalf("new origin manager: %v", err)
	}
	router := New(Deps{
		Enforcer: e, DB: db, Directory: directory.New(db), Users: auth.NewUserStore(db),
		TokenVerifier:      stubVerifier{},
		ClientAdmin:        newFakeClientManager(),
		OAuthAccessOrigins: manager,
	})
	return router, client
}

func TestLegacyStudioRedirectEndpointUsesDurableOrigins(t *testing.T) {
	router, _ := newAccessOriginAdminServer(t)
	const legacy = "/api/safe/v1/admin/clients/openbkn-studio/redirect-uris"
	const collection = "/api/safe/v1/admin/oauth/access-origins"
	const callback = "http://10.0.0.9:30080/studio/callback"

	response := adminReq(t, router, http.MethodPost, legacy, map[string]string{"redirect_uri": callback})
	if response.Code != http.StatusOK || !contains(redirectURIs(t, response.Body.Bytes()), callback) {
		t.Fatalf("legacy add status/body = %d: %s", response.Code, response.Body.String())
	}
	response = adminReq(t, router, http.MethodGet, collection, nil)
	entries := decodeOriginEntries(t, response.Body.Bytes())
	if len(entries) != 2 || entries[1].Origin != "http://10.0.0.9:30080" || entries[1].Source != "runtime" {
		t.Fatalf("durable entries after legacy add = %+v", entries)
	}

	response = adminReq(t, router, http.MethodDelete, legacy, map[string]string{"redirect_uri": callback})
	if response.Code != http.StatusOK || contains(redirectURIs(t, response.Body.Bytes()), callback) {
		t.Fatalf("legacy delete status/body = %d: %s", response.Code, response.Body.String())
	}
}

func decodeOriginEntries(t *testing.T, body []byte) []oauthorigin.Entry {
	t.Helper()
	var response struct {
		Entries []oauthorigin.Entry `json:"entries"`
		Total   int                 `json:"total"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode response %q: %v", body, err)
	}
	if response.Total != len(response.Entries) {
		t.Fatalf("total = %d, entries = %d", response.Total, len(response.Entries))
	}
	return response.Entries
}

func TestOAuthAccessOriginAdminAddListDelete(t *testing.T) {
	router, client := newAccessOriginAdminServer(t)
	const collection = "/api/safe/v1/admin/oauth/access-origins"

	response := adminReq(t, router, http.MethodPost, collection, map[string]string{"origin": "http://10.0.0.8:30080/"})
	if response.Code != http.StatusCreated {
		t.Fatalf("add status = %d: %s", response.Code, response.Body.String())
	}
	var created oauthorigin.Entry
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created entry: %v", err)
	}
	if created.Origin != "http://10.0.0.8:30080" || created.Source != "runtime" || created.ReadOnly {
		t.Fatalf("created entry = %+v", created)
	}
	if !contains(client.uris.RedirectURIs, "http://10.0.0.8:30080/studio/callback") ||
		!contains(client.uris.PostLogoutRedirectURIs, "http://10.0.0.8:30080/studio") {
		t.Fatalf("Hydra URIs = %+v", client.uris)
	}

	response = adminReq(t, router, http.MethodGet, collection, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", response.Code, response.Body.String())
	}
	entries := decodeOriginEntries(t, response.Body.Bytes())
	if len(entries) != 2 || entries[0].Source != "system" || entries[1].ID != created.ID {
		t.Fatalf("entries = %+v", entries)
	}

	response = adminReq(t, router, http.MethodDelete, collection+"/"+created.ID, nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", response.Code, response.Body.String())
	}
	if contains(client.uris.RedirectURIs, created.RedirectURI) {
		t.Fatalf("deleted callback remains in Hydra: %v", client.uris.RedirectURIs)
	}
}

func TestOAuthAccessOriginAdminValidationAndOwnership(t *testing.T) {
	router, _ := newAccessOriginAdminServer(t)
	const collection = "/api/safe/v1/admin/oauth/access-origins"

	if response := adminReq(t, router, http.MethodPost, collection, map[string]string{"origin": "https://host.example/path"}); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid origin status = %d: %s", response.Code, response.Body.String())
	}
	response := adminReq(t, router, http.MethodGet, collection, nil)
	entries := decodeOriginEntries(t, response.Body.Bytes())
	if response := adminReq(t, router, http.MethodDelete, collection+"/"+entries[0].ID, nil); response.Code != http.StatusForbidden {
		t.Fatalf("delete baseline status = %d: %s", response.Code, response.Body.String())
	}
	response = adminReq(t, router, http.MethodPost, collection, map[string]string{"origin": "https://public.example"})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), entries[0].ID) {
		t.Fatalf("duplicate status/body = %d: %s", response.Code, response.Body.String())
	}
	if response := tokReq(t, router, http.MethodGet, collection, nil, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list status = %d", response.Code)
	}
}

func TestOAuthAccessOriginAdminReturnsAcceptedDuringHydraOutage(t *testing.T) {
	router, client := newAccessOriginAdminServer(t)
	client.getErr = errors.New("hydra unavailable")
	response := adminReq(t, router, http.MethodPost, "/api/safe/v1/admin/oauth/access-origins", map[string]string{
		"origin": "https://external.example",
	})
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"sync_state":"error"`) {
		t.Fatalf("outage status/body = %d: %s", response.Code, response.Body.String())
	}
}
