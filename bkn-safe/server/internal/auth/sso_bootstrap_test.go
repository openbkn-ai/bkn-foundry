package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReconcileSSOExchange(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		existing, fresh, fail bool
	}{
		{name: "fresh install"}, {name: "restart reuses trust", existing: true, fresh: true},
		{name: "renew expiring trust", existing: true}, {name: "registration failure", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			cfg := SSOExchangeRegistration{ClientID: "safe-exchange", ClientSecret: "private-client-secret", Issuer: "https://safe.example/sso", KeyID: "key-1", Scopes: []string{"openid"}, PublicJWK: map[string]any{"kid": "key-1", "kty": "RSA", "n": "public-modulus", "e": "AQAB"}}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/admin/clients/safe-exchange":
					if !tc.existing {
						w.WriteHeader(404)
					} else {
						_, _ = w.Write([]byte(`{}`))
					}
				case r.Method == http.MethodPost && r.URL.Path == "/admin/clients", r.Method == http.MethodPut && r.URL.Path == "/admin/clients/safe-exchange":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["client_secret"] != cfg.ClientSecret || body["token_endpoint_auth_method"] != "client_secret_basic" || body["scope"] != "openid" {
						t.Error("wrong client credentials/policy")
					}
					if !reflect.DeepEqual(body["grant_types"], []any{"urn:ietf:params:oauth:grant-type:jwt-bearer"}) || !reflect.DeepEqual(body["audience"], []any{"bkn-safe"}) {
						t.Error("wrong grant/audience")
					}
					if tc.fail {
						w.WriteHeader(500)
						_, _ = w.Write([]byte(cfg.ClientSecret))
						return
					}
					_, _ = w.Write([]byte(`{}`))
				case r.Method == http.MethodGet && r.URL.Path == "/admin/trust/grants/jwt-bearer/issuers":
					if r.URL.Query().Get("issuer") != cfg.Issuer {
						t.Error("missing issuer filter")
					}
					trusts := []any{}
					if tc.existing {
						expiry := time.Now().Add(time.Hour)
						if tc.fresh {
							expiry = time.Now().Add(365 * 24 * time.Hour)
						}
						trusts = append(trusts, map[string]any{"id": "our-trust", "issuer": cfg.Issuer, "public_key": map[string]string{"kid": cfg.KeyID}, "allow_any_subject": true, "scope": cfg.Scopes, "expires_at": expiry})
					}
					// Unrelated trust must survive even if a server ignores the issuer filter.
					trusts = append(trusts, map[string]any{"id": "foreign-trust", "issuer": "https://other.example", "public_key": map[string]string{"kid": cfg.KeyID}})
					_ = json.NewEncoder(w).Encode(trusts)
				case r.Method == http.MethodDelete && r.URL.Path == "/admin/trust/grants/jwt-bearer/issuers/our-trust":
					w.WriteHeader(204)
				case r.Method == http.MethodPost && r.URL.Path == "/admin/trust/grants/jwt-bearer/issuers":
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["issuer"] != cfg.Issuer || body["allow_any_subject"] != true || !reflect.DeepEqual(body["jwk"], cfg.PublicJWK) {
						t.Error("wrong trust policy/public key")
					}
					w.WriteHeader(201)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			err := NewHydraAdmin(srv.URL).ReconcileSSOExchange(context.Background(), cfg)
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected setup error: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), cfg.ClientSecret) {
				t.Fatal("error leaked credentials")
			}
			want := []string{"GET /admin/clients/safe-exchange"}
			if tc.existing {
				want = append(want, "PUT /admin/clients/safe-exchange")
			} else {
				want = append(want, "POST /admin/clients")
			}
			if !tc.fail {
				want = append(want, "GET /admin/trust/grants/jwt-bearer/issuers")
				if !tc.fresh {
					if tc.existing {
						want = append(want, "DELETE /admin/trust/grants/jwt-bearer/issuers/our-trust")
					}
					want = append(want, "POST /admin/trust/grants/jwt-bearer/issuers")
				}
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls %v, want %v", calls, want)
			}
		})
	}
}
