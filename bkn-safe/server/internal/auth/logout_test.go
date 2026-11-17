// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestStudioLogout(t *testing.T) {
	for _, scenario := range []string{"success", "access_only", "refresh_only", "expired_access", "other_client", "other_subject", "wrong_token_use", "introspect_failure", "refresh_failure", "access_failure", "no_effect", "missing_credentials"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			active := map[string]bool{"access": true, "refresh": true, "other-device": true, "other-client": true}
			if scenario == "expired_access" {
				active["access"] = false
			}
			revoked := []string{}
			fail := scenario
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodPost || r.URL.RawQuery != "" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				token := r.PostForm.Get("token")
				switch r.URL.Path {
				case "/admin/oauth2/introspect":
					if fail == "introspect_failure" {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"error":"access refresh"}`))
						return
					}
					use, client, sub := "access_token", "openbkn-studio", "user-1"
					if token == "refresh" {
						use = "refresh_token"
					}
					if r.PostForm.Get("token_type_hint") != use {
						t.Errorf("wrong token hint: %v", r.PostForm)
					}
					if scenario == "other_client" {
						client = "cli-client"
					}
					if scenario == "other_subject" && token == "refresh" {
						sub = "user-2"
					}
					if scenario == "wrong_token_use" && token == "refresh" {
						use = "access_token"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"active": active[token], "client_id": client, "sub": sub, "token_use": use})
				case "/oauth2/revoke":
					if r.PostForm.Get("client_id") != "openbkn-studio" {
						t.Error("must use the fixed public SPA client")
					}
					revoked = append(revoked, token)
					if fail == token+"_failure" {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"error":"access refresh"}`))
						return
					}
					if scenario != "no_effect" {
						active[token] = false
					}
					w.WriteHeader(http.StatusOK)
				default:
					t.Errorf("must never revoke subject/client consent or login sessions: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			revoker := NewStudioLogout(NewHydraAdmin(server.URL), server.URL)
			access, refresh := "access", "refresh"
			if scenario == "access_only" {
				refresh = ""
			}
			if scenario == "missing_credentials" {
				access, refresh = "", ""
			}
			if scenario == "refresh_only" {
				access = ""
			}
			subject, err := revoker.Logout(t.Context(), access, refresh, "")
			success := scenario == "success" || scenario == "expired_access" || scenario == "access_only" || scenario == "refresh_only"
			if success {
				if err != nil || subject != "user-1" {
					t.Fatalf("logout = %q,%v", subject, err)
				}
				if (access != "" && active["access"]) || (refresh != "" && active["refresh"]) {
					t.Fatal("provided credentials survived logout")
				}
				subject, err = revoker.Logout(t.Context(), access, refresh, "")
				if err != nil || subject != "" {
					t.Fatalf("repeat logout = %q,%v; want idempotent success without another fact", subject, err)
				}
			} else {
				if err == nil || subject != "" {
					t.Fatalf("failed logout = %q,%v", subject, err)
				}
				if !errors.Is(err, ErrLogoutToken) && strings.Contains(err.Error(), "access refresh") {
					t.Fatal("upstream response leaked credentials")
				}
			}
			if !active["other-device"] || !active["other-client"] {
				t.Fatal("unrelated sessions were affected")
			}
			if scenario == "other_subject" || scenario == "other_client" || scenario == "wrong_token_use" || scenario == "missing_credentials" || scenario == "introspect_failure" {
				if len(revoked) != 0 {
					t.Fatalf("invalid credentials caused revocation: %v", revoked)
				}
			}
			if scenario == "refresh_failure" || scenario == "access_failure" {
				if len(revoked) != 2 {
					t.Fatalf("must attempt both tokens: %v", revoked)
				}
				fail = ""
				if _, err := revoker.Logout(t.Context(), access, refresh, ""); err != nil {
					t.Fatalf("retry after partial failure: %v", err)
				}
				if active["access"] || active["refresh"] {
					t.Fatal("retry left credentials active")
				}
			}
		})
	}
}
