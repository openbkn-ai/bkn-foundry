// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
)

// stubHydra stands in for Hydra; it records whether it was asked.
type stubHydra struct{ asked bool }

func (h *stubHydra) Introspect(context.Context, string) (hydra.TokenIntrospectInfo, error) {
	return hydra.TokenIntrospectInfo{}, errors.New("not used")
}

func (h *stubHydra) VerifyToken(context.Context, *gin.Context) (hydra.Visitor, error) {
	h.asked = true
	return hydra.Visitor{ID: "oauth-user", Type: hydra.VisitorType_User}, nil
}

// safeWith answers key introspection the way bkn-safe does, from keys to owners.
func safeWith(t *testing.T, owners map[string]map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != apiKeyIntrospectPath {
			http.NotFound(w, r)
			return
		}
		var body struct{ Token string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		owner, ok := owners[body.Token]
		if !ok {
			owner = map[string]any{"active": false}
		}
		_ = json.NewEncoder(w).Encode(owner)
	}))
	t.Cleanup(server.Close)
	return server
}

func contextWith(authorization string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/workshop/v1/mcp", nil)
	c.Request.Header.Set("Authorization", authorization)
	return c
}

func TestAPIKeyActsAsItsOwner(t *testing.T) {
	safe := safeWith(t, map[string]map[string]any{
		"bak_person": {"active": true, "sub": "u-1", "account_type": "user"},
		"bak_app":    {"active": true, "sub": "a-1", "account_type": "app"},
	})
	h := &stubHydra{}
	access := &hydraAuthAccess{hydra: h, apiKeys: newAPIKeyVerifier(safe.URL)}

	visitor, err := access.VerifyToken(context.Background(), contextWith("Bearer bak_person"))
	if err != nil || visitor.ID != "u-1" || visitor.Type != hydra.VisitorType_User {
		t.Fatalf("person's key = %+v, %v", visitor, err)
	}
	visitor, err = access.VerifyToken(context.Background(), contextWith("Bearer bak_app"))
	if err != nil || visitor.ID != "a-1" || visitor.Type != hydra.VisitorType_App {
		t.Fatalf("application's key = %+v, %v", visitor, err)
	}
	if h.asked {
		t.Fatal("an API key was sent to Hydra")
	}
}

func TestAPIKeyBknSafeDoesNotAcceptIsRefused(t *testing.T) {
	safe := safeWith(t, map[string]map[string]any{
		// Active but naming no one is not an owner either.
		"bak_nobody": {"active": true},
	})
	access := &hydraAuthAccess{hydra: &stubHydra{}, apiKeys: newAPIKeyVerifier(safe.URL)}
	for _, key := range []string{"bak_revoked", "bak_nobody"} {
		if visitor, err := access.VerifyToken(context.Background(), contextWith("Bearer "+key)); err == nil {
			t.Fatalf("%s accepted as %+v", key, visitor)
		}
	}

	down := &hydraAuthAccess{hydra: &stubHydra{}, apiKeys: newAPIKeyVerifier("http://127.0.0.1:1")}
	if _, err := down.VerifyToken(context.Background(), contextWith("Bearer bak_person")); err == nil {
		t.Fatal("a key was accepted with bkn-safe unreachable")
	}
}

func TestOAuthTokenStillGoesToHydra(t *testing.T) {
	h := &stubHydra{}
	access := &hydraAuthAccess{hydra: h, apiKeys: newAPIKeyVerifier("http://127.0.0.1:1")}
	visitor, err := access.VerifyToken(context.Background(), contextWith("Bearer ory_at_x"))
	if err != nil || !h.asked || visitor.ID != "oauth-user" {
		t.Fatalf("OAuth token = %+v, %v, Hydra asked %v", visitor, err, h.asked)
	}
}
