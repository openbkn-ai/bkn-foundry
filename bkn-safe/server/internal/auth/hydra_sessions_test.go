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
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestRevokeUserSessions(t *testing.T) {
	for _, failure := range []string{"success", "consent", "login"} {
		t.Run(failure, func(t *testing.T) {
			calls := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls[r.URL.Path]++
				if r.Method != http.MethodDelete || r.URL.Query().Get("subject") != "user +/1" {
					t.Errorf("unexpected revocation: %s %s", r.Method, r.URL)
				}
				switch r.URL.Path {
				case "/admin/oauth2/auth/sessions/consent":
					if r.URL.Query().Get("all") != "true" || r.URL.Query().Has("client") {
						t.Errorf("revocation must cover all clients: %s", r.URL)
					}
				case "/admin/oauth2/auth/sessions/login":
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if strings.HasSuffix(r.URL.Path, "/"+failure) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":"temporarily unavailable"}`))
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			h := NewHydraAdmin(server.URL)
			ctx, cancel := context.WithCancel(t.Context())
			cancel() // A disconnected administrator must not cancel cleanup.
			err := h.RevokeUserSessions(ctx, "user +/1")
			if (err != nil) != (failure != "success") {
				t.Fatalf("revoke error = %v, failure=%s", err, failure)
			}
			if calls["/admin/oauth2/auth/sessions/consent"] != 1 || calls["/admin/oauth2/auth/sessions/login"] != 1 {
				t.Fatalf("both revocations must be attempted: %v", calls)
			}
			if err := h.RevokeUserSessions(t.Context(), " "); err == nil {
				t.Fatal("empty subject must be refused")
			}
		})
	}
}

func TestConsentRejectsDisabledAccount(t *testing.T) {
	users := newStore(t)
	if err := users.CreateLocalUser(t.Context(), &model.User{ID: "consent-user", Account: "consent-user", Enabled: false}, "password"); err != nil {
		t.Fatal(err)
	}
	accepted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/admin/oauth2/auth/requests/consent" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"challenge": "old-challenge", "subject": "consent-user", "requested_scope": []string{"openid"},
			})
			return
		}
		accepted = true
		t.Errorf("disabled subject must not reach consent acceptance: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	provider := NewProvider(users, NewHydraAdmin(server.URL), users)
	if _, err := provider.Consent(t.Context(), "old-challenge", "127.0.0.1", "web", false); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("pending consent = %v, want disabled", err)
	}
	if accepted {
		t.Fatal("disabled subject's pending consent was accepted")
	}
}
