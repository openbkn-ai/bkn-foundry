package sso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

func TestBootstrapRetriesAndCachesUntilDatabasePolicyChanges(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer func() { _ = sqlDB.Close() }()
	if err := db.AutoMigrate(&model.SSOProvider{}); err != nil {
		t.Fatal(err)
	}
	p := model.SSOProvider{ID: "casdoor", Enabled: true, ExchangeEnabled: true, HydraIssueScope: "openid"}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			w.WriteHeader(503)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/admin/clients/exchange-client" {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/admin/trust/grants/jwt-bearer/issuers" {
			var body struct {
				JWK map[string]any `json:"jwk"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.JWK["kty"] != "RSA" || body.JWK["e"] != "AQAB" || body.JWK["n"] == nil || body.JWK["d"] != nil {
				t.Error("invalid or private JWK")
			}
		}
		w.WriteHeader(201)
	}))
	defer srv.Close()
	e := &Exchange{service: New(db, nil), hydra: auth.NewHydraAdmin(srv.URL), key: key, clientID: "exchange-client", keyID: "key", issuer: "https://safe.example/sso", autoBootstrap: true}
	if err := e.ensureHydra(context.Background()); err == nil {
		t.Fatal("failure accepted")
	}
	if err := e.ensureHydra(context.Background()); err != nil {
		t.Fatal(err)
	}
	count := requests.Load()
	if err := e.ensureHydra(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != count {
		t.Fatal("successful setup was not cached")
	}
	e.invalidateSetup()
	if err := e.ensureHydra(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() == count {
		t.Fatal("failed grant did not trigger re-registration")
	}
	count = requests.Load()

	if err := db.Model(&model.SSOProvider{}).Where("id = ?", p.ID).Update("hydra_issue_scope", "openid profile").Error; err != nil {
		t.Fatal(err)
	}
	if err := e.ensureHydra(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() == count || strings.Join(e.setupScopes, " ") != "openid profile" {
		t.Fatal("database scope changes did not trigger setup")
	}
}

func TestDefaultHookDoesNotRequireAnIdentityProvider(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ExchangeConfig{TokenURL: "http://hydra:4444/oauth2/token", Audience: "https://safe.example/oauth2/token", Issuer: "https://safe.example/sso", ClientID: "exchange-client", ClientSecret: "client-secret", HookKey: strings.Repeat("h", 48), PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})), AutoBootstrap: true}
	e, err := NewExchange(New(nil, nil), auth.NewHydraAdmin("http://unused"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := NewExchange(New(nil, nil), auth.NewHydraAdmin("http://unused"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if e.keyID == "" || e.keyID != again.keyID {
		t.Fatal("derived key ID must survive restart")
	}
	body := []byte(`{"request":{"client_id":"openbkn-studio","grant_types":["authorization_code"]}}`)
	ext, modified, err := e.Hook(context.Background(), cfg.HookKey, body)
	if err != nil || modified || ext != nil {
		t.Fatalf("ordinary client hook failed: %v", err)
	}
	if _, _, err := e.Hook(context.Background(), "wrong-key", body); err != ErrForbidden {
		t.Fatal("bad hook key accepted")
	}
}

func TestDatabaseCredentialsReloadWithoutRestart(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer func() { _ = sqlDB.Close() }()
	if err := db.AutoMigrate(&model.SSOProvider{}); err != nil {
		t.Fatal(err)
	}
	p := model.SSOProvider{ID: "casdoor", Enabled: true, Protocol: "oidc", Adapter: "casdoor", Issuer: "https://casdoor.example", DiscoveryURL: "https://casdoor.example/discovery", ClientID: "safe", ClientSecret: "first-secret"}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	s := New(db, nil)
	for _, secret := range []string{"first-secret", "changed-secret"} {
		if err := db.Model(&p).Update("client_secret", secret).Error; err != nil {
			t.Fatal(err)
		}
		got, err := s.Provider(context.Background(), p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if providerSecret(got) != secret {
			t.Fatal("credential update required restart")
		}
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) {
			t.Fatal("provider JSON leaked credentials")
		}
	}
}
