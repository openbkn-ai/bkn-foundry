package sso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

func TestJWTBearerAssertionUsesBrowserTokenAudience(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	e := &Exchange{
		tokenURL: "http://hydra-public:4444/oauth2/token",
		audience: "https://openbkn.example/oauth2/token",
		issuer:   "https://openbkn.example/sso-exchange",
		keyID:    "exchange-v1",
		key:      key,
	}
	assertion, err := e.sign("local-user", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatal("invalid signed assertion")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims assertionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if !contains(claims.Audience, e.audience) || contains(claims.Audience, e.tokenURL) {
		t.Fatalf("wrong JWT bearer audience: %v", claims.Audience)
	}
	if _, err := e.verify(assertion); err != nil {
		t.Fatalf("hook rejected signed assertion: %v", err)
	}
}

func TestSwapSeparatesExternalAndHydraScopes(t *testing.T) {
	t.Setenv("TEST_CASDOOR_SECRET", "casdoor-test-secret")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, external, hydra, legacy, wantHydra string
	}{
		{"read user token to openid Hydra token", "read", "openid", "openid", "openid"},
		{"legacy configuration", "", "", "read", "read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })
			if err := db.AutoMigrate(&model.User{}, &model.SSOProvider{}, &model.SSOIdentity{}); err != nil {
				t.Fatal(err)
			}
			var baseURL string
			var exchange *Exchange
			var issuedSubject atomic.Value
			var tokenRequested atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var response any
				switch r.URL.Path {
				case "/discovery":
					response = metadata{Issuer: baseURL, Authorize: baseURL + "/authorize", Token: baseURL + "/casdoor/token",
						UserInfo: baseURL + "/userinfo", Introspect: baseURL + "/casdoor/introspect", JWKS: baseURL + "/jwks"}
				case "/casdoor/introspect":
					response = TokenInfo{Active: true, Issuer: baseURL, Subject: "external-user", ClientID: "trusted-app",
						Audience: stringAudience{"safe-api"}, Scope: "read", Type: "Bearer", Expiry: time.Now().Add(time.Minute).Unix()}
				case "/userinfo":
					response = map[string]string{"sub": "external-user", "name": "Test User"}
				case "/oauth2/token":
					if err := r.ParseForm(); err != nil {
						t.Errorf("parse Hydra token request: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if scope := r.PostForm.Get("scope"); scope != tc.wantHydra {
						t.Errorf("Hydra received scope %q, want %q", scope, tc.wantHydra)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					claims, err := exchange.verify(r.PostForm.Get("assertion"))
					if err != nil {
						t.Errorf("invalid signed assertion: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					issuedSubject.Store(claims.Subject)
					tokenRequested.Store(true)
					response = TokenResponse{AccessToken: "issued-token", TokenType: "bearer", ExpiresIn: 60, Scope: tc.wantHydra}
				case "/admin/oauth2/introspect":
					response = map[string]any{"active": true, "sub": issuedSubject.Load(), "client_id": "exchange-client",
						"aud": []string{"bkn-safe"}, "ext": auth.ExtClaims(&model.User{}, "127.0.0.1", "unknown")}
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			t.Cleanup(srv.Close)
			baseURL = srv.URL
			provider := &model.SSOProvider{ID: "casdoor", Protocol: "oidc", Adapter: "casdoor", Enabled: true,
				Issuer: baseURL, DiscoveryURL: baseURL + "/discovery", ClientID: "safe", ClientSecretEnv: "TEST_CASDOOR_SECRET",
				ExchangeEnabled: true, AllowInsecureHTTP: true, ExchangeAudience: "safe-api", AllowedClientIDs: "trusted-app",
				ExternalRequiredScope: tc.external, HydraIssueScope: tc.hydra, ExchangeScope: tc.legacy}
			if err := db.Create(provider).Error; err != nil {
				t.Fatal(err)
			}
			exchange = &Exchange{service: New(db, srv.Client()), hydra: auth.NewHydraAdmin(baseURL), client: srv.Client(),
				tokenURL: baseURL + "/oauth2/token", audience: "https://openbkn.example/oauth2/token",
				issuer: "https://openbkn.example/sso-exchange", keyID: "test-key", key: key,
				clientID: "exchange-client", secret: "test-client-secret"}
			result, err := exchange.Swap(context.Background(), "casdoor", "casdoor-read-token", "127.0.0.1")
			if err != nil {
				t.Fatalf("Swap() failed: %v", err)
			}
			if !tokenRequested.Load() || result.AccessToken != "issued-token" || result.Scope != tc.wantHydra {
				t.Fatalf("unexpected token result: requested=%v scope=%q", tokenRequested.Load(), result.Scope)
			}
		})
	}
}
