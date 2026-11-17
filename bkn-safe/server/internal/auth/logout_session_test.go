// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestStudioLogoutSignedSession(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"success", "expired_hint", "bad_signature", "wrong_issuer", "wrong_audience", "wrong_party", "missing_sid", "unknown_key", "wrong_algorithm", "wrong_subject", "consent_failure", "consent_no_effect", "jwks_failure"} {
		t.Run(scenario, func(t *testing.T) {
			revoked, lookupCalls := false, 0
			ids := []string{}
			server := httptest.NewUnstartedServer(nil)
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/openid-configuration":
					_ = json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL})
				case "/.well-known/jwks.json":
					if scenario == "jwks_failure" {
						w.WriteHeader(503)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{"kty": "RSA", "kid": "test-key", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
				case "/admin/oauth2/introspect":
					_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "client_id": "openbkn-studio", "sub": "different-user", "token_use": "access_token"})
				case "/admin/oauth2/auth/sessions/consent":
					if r.Method == http.MethodDelete {
						id := r.URL.Query().Get("consent_request_id")
						if len(r.URL.Query()) != 1 || id != "current-grant" {
							t.Errorf("revocation escaped its verified consent request: %s", r.URL)
						}
						ids = append(ids, id)
						if scenario == "consent_failure" {
							w.WriteHeader(503)
							return
						}
						if scenario != "consent_no_effect" {
							revoked = true
						}
						w.WriteHeader(204)
						return
					}
					lookupCalls++
					if r.URL.Query().Get("subject") != "user-1" || r.URL.Query().Get("login_session_id") != "browser-1" {
						t.Errorf("unscoped consent lookup: %s", r.URL)
					}
					rows := []map[string]any{}
					for _, item := range []struct{ id, client, sid, sub string }{{"current-grant", "openbkn-studio", "browser-1", "user-1"}, {"another-client", "cli", "browser-1", "user-1"}, {"another-device", "openbkn-studio", "browser-2", "user-1"}, {"another-user", "openbkn-studio", "browser-1", "user-2"}} {
						if revoked && item.id == "current-grant" {
							continue
						}
						rows = append(rows, map[string]any{"consent_request_id": item.id, "consent_request": map[string]any{"challenge": "", "client": map[string]any{"client_id": item.client}, "subject": item.sub, "login_session_id": item.sid}})
					}
					_ = json.NewEncoder(w).Encode(rows)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			})
			server.Start()
			defer server.Close()
			claims := logoutClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: server.URL, Subject: "user-1", Audience: jwt.ClaimStrings{"openbkn-studio"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, SID: "browser-1"}
			switch scenario {
			case "expired_hint":
				claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))
			case "wrong_issuer":
				claims.Issuer = "https://untrusted.example"
			case "wrong_audience":
				claims.Audience = jwt.ClaimStrings{"other-client"}
			case "wrong_party":
				claims.AuthorizedParty = "other-client"
			case "missing_sid":
				claims.SID = ""
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = "test-key"
			var signingKey any = key
			if scenario == "bad_signature" {
				signingKey, _ = rsa.GenerateKey(rand.Reader, 2048)
			}
			if scenario == "unknown_key" {
				token.Header["kid"] = "unknown-key"
			}
			if scenario == "wrong_algorithm" {
				token.Method = jwt.SigningMethodHS256
				token.Header["alg"] = token.Method.Alg()
				signingKey = []byte("not-an-rsa-key")
			}
			raw, err := token.SignedString(signingKey)
			if err != nil {
				t.Fatal(err)
			}
			revoker := NewStudioLogout(NewHydraAdmin(server.URL), server.URL)
			access := ""
			if scenario == "wrong_subject" {
				access = "different-user-access"
			}
			subject, err := revoker.Logout(t.Context(), access, "", raw)
			success := scenario == "success" || scenario == "expired_hint"
			if success {
				if err != nil || subject != "user-1" || len(ids) != 1 || ids[0] != "current-grant" {
					t.Fatalf("logout subject=%q err=%v revoked=%v", subject, err, ids)
				}
				subject, err = revoker.Logout(t.Context(), "", "", raw)
				if err != nil || subject != "" || len(ids) != 1 {
					t.Fatalf("repeat logout was not idempotent: %q %v %v", subject, err, ids)
				}
			} else {
				if err == nil || subject != "" {
					t.Fatalf("failed logout = %q %v", subject, err)
				}
				if scenario != "consent_failure" && scenario != "consent_no_effect" && scenario != "jwks_failure" {
					if !errors.Is(err, ErrLogoutToken) || lookupCalls != 0 || len(ids) != 0 {
						t.Fatalf("untrusted hint caused consent access: %v, calls=%d, revoked=%v", err, lookupCalls, ids)
					}
				}
			}
		})
	}
}
