// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
)

type fakeRegistrar struct {
	got []auth.DynamicClient
	err error
}

func (f *fakeRegistrar) CreateDynamicClient(_ context.Context, c auth.DynamicClient) (auth.RegisteredClient, error) {
	f.got = append(f.got, c)
	if f.err != nil {
		return auth.RegisteredClient{}, f.err
	}
	return auth.RegisteredClient{ClientID: "client-1", IssuedAt: time.Unix(1_800_000_000, 0)}, nil
}

func hydraDiscovery(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://10.211.55.4",
			"authorization_endpoint": "https://10.211.55.4/oauth2/auth",
			"token_endpoint":         "https://10.211.55.4/oauth2/token",
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func registrationEngine(t *testing.T, cfg OAuthDynamicRegistration) (*gin.Engine, *fakeRegistrar) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	reg := &fakeRegistrar{}
	if cfg.Registrar == nil {
		cfg.Registrar = reg
	}
	if cfg.HydraPublicURL == "" {
		cfg.HydraPublicURL = hydraDiscovery(t).URL
	}
	r := gin.New()
	registerOAuthDynamicRegistration(r, cfg)
	return r, reg
}

func serve(r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "10.211.55.4"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return out
}

// WorkBuddy 5.8.3 registers exactly this: authorization_code only, public, a
// loopback callback on its local server.
const workBuddyRegistration = `{
  "redirect_uris": ["http://127.0.0.1:53124/mcp/oauth/callback"],
  "client_name": "WorkBuddy (bkn-vm)",
  "token_endpoint_auth_method": "none",
  "grant_types": ["authorization_code"],
  "response_types": ["code"]
}`

func TestAuthorizationServerMetadataAdvertisesRegistration(t *testing.T) {
	r, _ := registrationEngine(t, OAuthDynamicRegistration{})
	w := serve(r, http.MethodGet, "/.well-known/oauth-authorization-server", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	meta := decode(t, w)
	if meta["issuer"] != "https://10.211.55.4" || meta["token_endpoint"] != "https://10.211.55.4/oauth2/token" {
		t.Fatalf("hydra fields not passed through: %v", meta)
	}
	if meta["registration_endpoint"] != "https://10.211.55.4/api/safe/v1/oauth/register" {
		t.Fatalf("registration_endpoint = %v", meta["registration_endpoint"])
	}
}

func TestProtectedResourceMetadataNamesTheMCPEndpointAndOfflineScope(t *testing.T) {
	r, _ := registrationEngine(t, OAuthDynamicRegistration{})
	w := serve(r, http.MethodGet, "/.well-known/oauth-protected-resource/api/agent-retrieval/v1/mcp/", "",
		map[string]string{"X-Forwarded-Proto": "https"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	meta := decode(t, w)
	if meta["resource"] != "https://10.211.55.4/api/agent-retrieval/v1/mcp/" {
		t.Fatalf("resource = %v", meta["resource"])
	}
	servers, _ := meta["authorization_servers"].([]any)
	if len(servers) != 1 || servers[0] != "https://10.211.55.4" {
		t.Fatalf("authorization_servers = %v", meta["authorization_servers"])
	}
	scopes, _ := json.Marshal(meta["scopes_supported"])
	if !strings.Contains(string(scopes), `"offline"`) {
		t.Fatalf("scopes_supported lacks offline, so no refresh token would be issued: %s", scopes)
	}

	if w := serve(r, http.MethodGet, "/.well-known/oauth-protected-resource", "", nil); w.Code != http.StatusOK {
		t.Fatalf("root document: status %d", w.Code)
	}
	if w := serve(r, http.MethodGet, "/.well-known/oauth-protected-resource/api/bkn-backend/v1", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("non-MCP resource: status %d, want 404", w.Code)
	}
}

func TestRegistrationAcceptsWorkBuddyAndAlwaysAllowsRefresh(t *testing.T) {
	r, reg := registrationEngine(t, OAuthDynamicRegistration{})
	w := serve(r, http.MethodPost, oauthRegisterPath, workBuddyRegistration, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if len(reg.got) != 1 {
		t.Fatalf("registrar calls = %d", len(reg.got))
	}
	got := reg.got[0]
	if strings.Join(got.GrantTypes, ",") != "authorization_code,refresh_token" {
		t.Fatalf("grant types = %v", got.GrantTypes)
	}
	if got.Scope != dynamicClientScope || got.Name != "WorkBuddy (bkn-vm)" {
		t.Fatalf("client = %+v", got)
	}
	if got.Metadata["openbkn_kind"] != "dynamic" {
		t.Fatalf("metadata = %v", got.Metadata)
	}
	body := decode(t, w)
	if body["client_id"] != "client-1" || body["token_endpoint_auth_method"] != "none" {
		t.Fatalf("response = %v", body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
}

func TestRegistrationRejectsClientsThatSkipSignIn(t *testing.T) {
	cases := map[string]struct {
		body string
		code string
	}{
		"client_credentials": {
			`{"redirect_uris":["http://127.0.0.1:1/cb"],"grant_types":["client_credentials"]}`, "invalid_client_metadata",
		},
		"confidential client": {
			`{"redirect_uris":["http://127.0.0.1:1/cb"],"token_endpoint_auth_method":"client_secret_post"}`, "invalid_client_metadata",
		},
		"implicit": {
			`{"redirect_uris":["http://127.0.0.1:1/cb"],"response_types":["token"]}`, "invalid_client_metadata",
		},
		"remote callback": {
			`{"redirect_uris":["https://attacker.example/cb"]}`, "invalid_redirect_uri",
		},
		"loopback lookalike": {
			`{"redirect_uris":["http://127.0.0.1.attacker.example/cb"]}`, "invalid_redirect_uri",
		},
		"custom scheme": {
			`{"redirect_uris":["workbuddy://oauth/callback"]}`, "invalid_redirect_uri",
		},
		"no callback": {
			`{"client_name":"x"}`, "invalid_redirect_uri",
		},
		"not json": {
			`{`, "invalid_client_metadata",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, reg := registrationEngine(t, OAuthDynamicRegistration{})
			w := serve(r, http.MethodPost, oauthRegisterPath, tc.body, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if got := decode(t, w)["error"]; got != tc.code {
				t.Fatalf("error = %v, want %s", got, tc.code)
			}
			if len(reg.got) != 0 {
				t.Fatalf("rejected registration still created a client")
			}
		})
	}
}

func TestRegistrationNamesTheRejectedCallback(t *testing.T) {
	r, _ := registrationEngine(t, OAuthDynamicRegistration{})
	w := serve(r, http.MethodPost, oauthRegisterPath, `{"redirect_uris":["https://agent.example/cb"]}`, nil)
	if !strings.Contains(decode(t, w)["error_description"].(string), "https://agent.example/cb") {
		t.Fatalf("description does not name the callback: %s", w.Body)
	}
}

func TestRegistrationAdmitsAnOperatorListedOrigin(t *testing.T) {
	r, reg := registrationEngine(t, OAuthDynamicRegistration{AllowedRedirectOrigins: []string{"https://Agent.Example:443"}})
	w := serve(r, http.MethodPost, oauthRegisterPath, `{"redirect_uris":["https://agent.example/oauth/cb"]}`, nil)
	if w.Code != http.StatusCreated || len(reg.got) != 1 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}

func TestRegistrationIsRateLimitedPerAddress(t *testing.T) {
	r, _ := registrationEngine(t, OAuthDynamicRegistration{RatePerMinute: 2})
	for i := 0; i < 2; i++ {
		if w := serve(r, http.MethodPost, oauthRegisterPath, workBuddyRegistration, nil); w.Code != http.StatusCreated {
			t.Fatalf("registration %d: status %d", i, w.Code)
		}
	}
	if w := serve(r, http.MethodPost, oauthRegisterPath, workBuddyRegistration, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("third registration: status %d, want 429", w.Code)
	}
}

// The leftmost X-Forwarded-For entry is the caller's to write; varying it must
// not reset the per-address limit.
func TestRegistrationLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	r, reg := registrationEngine(t, OAuthDynamicRegistration{RatePerMinute: 2})
	for i, spoofed := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		w := serve(r, http.MethodPost, oauthRegisterPath, workBuddyRegistration,
			map[string]string{"X-Forwarded-For": spoofed + ", 10.211.55.2"})
		want := http.StatusCreated
		if i == 2 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("registration %d: status %d, want %d", i, w.Code, want)
		}
	}
	if got := reg.got[0].Metadata["registered_from"]; got != "10.211.55.2" {
		t.Fatalf("registered_from = %v, want the gateway-appended address", got)
	}
}

func TestRegistrationReportsAHydraFailure(t *testing.T) {
	reg := &fakeRegistrar{err: errors.New("hydra down")}
	r, _ := registrationEngine(t, OAuthDynamicRegistration{Registrar: reg})
	if w := serve(r, http.MethodPost, oauthRegisterPath, workBuddyRegistration, nil); w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
}

func TestMetadataReportsAnUnreachableHydra(t *testing.T) {
	r, _ := registrationEngine(t, OAuthDynamicRegistration{HydraPublicURL: "http://127.0.0.1:1"})
	if w := serve(r, http.MethodGet, "/.well-known/oauth-authorization-server", "", nil); w.Code != http.StatusBadGateway {
		t.Fatalf("status %d", w.Code)
	}
}

func TestDynamicRegistrationIsAbsentUnlessConfigured(t *testing.T) {
	r := New(Deps{})
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, oauthRegisterPath, strings.NewReader(workBuddyRegistration)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("register: status %d, want 404", w.Code)
	}
}
