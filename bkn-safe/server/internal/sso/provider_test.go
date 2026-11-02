package sso

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestValidateExchangeAdmitsOnlyConfiguredCasdoorClient(t *testing.T) {
	provider := &model.SSOProvider{Issuer: "https://casdoor.example", DiscoveryURL: "https://casdoor.example/.well-known/openid-configuration",
		ClientID: "safe", ExchangeEnabled: true, ExchangeAudience: "safe-api other-api", ExchangeScope: "openid",
		ExternalRequiredScope: "read", HydraIssueScope: "openid", AllowedClientIDs: "trusted-app"}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/.well-known/openid-configuration":
			body = `{"issuer":"https://casdoor.example","authorization_endpoint":"https://casdoor.example/login/oauth/authorize","token_endpoint":"https://casdoor.example/api/login/oauth/access_token","userinfo_endpoint":"https://casdoor.example/api/userinfo","introspection_endpoint":"https://casdoor.example/api/login/oauth/introspect","jwks_uri":"https://casdoor.example/.well-known/jwks"}`
		case "/api/login/oauth/introspect":
			body = fmt.Sprintf(`{"active":true,"iss":"https://casdoor.example","sub":"user-1","aud":"safe-api","client_id":%q,"scope":"read","token_type":"Bearer","exp":%d}`,
				"trusted-app", time.Now().Add(time.Minute).Unix())
		default:
			t.Fatalf("unexpected request: %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	service := New(nil, client)
	if info, err := service.ValidateExchange(context.Background(), provider, "valid"); err != nil || info.Subject != "user-1" {
		t.Fatalf("valid token rejected: %#v, %v", info, err)
	}
	provider.ExchangeAudience = "other-api safe-api"
	if _, err := service.ValidateExchange(context.Background(), provider, "valid"); err != nil {
		t.Fatalf("second allowed audience rejected: %v", err)
	}
	provider.ExchangeAudience = "other-api"
	if _, err := service.ValidateExchange(context.Background(), provider, "valid"); err != ErrForbidden {
		t.Fatalf("unlisted audience accepted: %v", err)
	}
	provider.ExchangeAudience = "safe-api"
	provider.AllowedClientIDs = "another-app"
	if _, err := service.ValidateExchange(context.Background(), provider, "valid"); err != ErrForbidden {
		t.Fatalf("untrusted client accepted: %v", err)
	}
}

func TestValidateExchangeRequiresEveryExternalScope(t *testing.T) {
	for _, tc := range []struct {
		name, required, legacy, granted string
		want                            error
	}{
		{"read accepted independently of legacy openid", "read", "openid", "read", nil},
		{"missing required read", "read", "openid", "openid", ErrForbidden},
		{"scope substrings rejected", "read", "openid", "bread read:all", ErrForbidden},
		{"all required scopes present", "read profile", "openid", "profile read email", nil},
		{"one required scope missing", "read profile", "openid", "read", ErrForbidden},
		{"legacy configuration preserved", "", "openid", "openid", nil},
		{"legacy still rejects read", "", "openid", "read", ErrForbidden},
		{"empty policy rejected", "", "", "read", ErrInvalidProvider},
		{"whitespace policy rejected", " \t", "openid", "openid", ErrInvalidProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &model.SSOProvider{Issuer: "https://casdoor.example", DiscoveryURL: "https://casdoor.example/discovery",
				ExchangeEnabled: true, ExchangeAudience: "safe-api", AllowedClientIDs: "trusted-app",
				ExternalRequiredScope: tc.required, ExchangeScope: tc.legacy, HydraIssueScope: "openid"}
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				var body string
				if req.URL.Path == "/discovery" {
					body = `{"issuer":"https://casdoor.example","authorization_endpoint":"https://casdoor.example/auth","token_endpoint":"https://casdoor.example/token","userinfo_endpoint":"https://casdoor.example/userinfo","introspection_endpoint":"https://casdoor.example/introspect","jwks_uri":"https://casdoor.example/jwks"}`
				} else if req.URL.Path == "/introspect" {
					body = fmt.Sprintf(`{"active":true,"iss":"https://casdoor.example","sub":"user-1","aud":"safe-api","client_id":"trusted-app","scope":%q,"token_type":"Bearer","exp":%d}`,
						tc.granted, time.Now().Add(time.Minute).Unix())
				} else {
					t.Fatalf("unexpected request: %s", req.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			if _, err := New(nil, client).ValidateExchange(context.Background(), provider, "external-token"); err != tc.want {
				t.Fatalf("ValidateExchange() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateProviderRejectsEmptyExchangeScopes(t *testing.T) {
	t.Setenv("TEST_CASDOOR_SECRET", "test-secret")
	for _, tc := range []struct {
		name, external, hydra, legacy string
		valid                         bool
	}{
		{"split policy", "read", "openid", "", true},
		{"legacy policy", "", "", "openid", true},
		{"external missing", "", "openid", "", false},
		{"hydra missing", "read", "", "", false},
		{"external whitespace", " ", "openid", "openid", false},
		{"hydra whitespace", "read", " ", "openid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &model.SSOProvider{ID: "casdoor", Protocol: "oidc", Adapter: "casdoor",
				Issuer: "https://casdoor.example", DiscoveryURL: "https://casdoor.example/discovery",
				ClientID: "safe", ClientSecretEnv: "TEST_CASDOOR_SECRET", ExchangeEnabled: true,
				AllowedClientIDs: "trusted-app", ExchangeAudience: "safe-api",
				ExternalRequiredScope: tc.external, HydraIssueScope: tc.hydra, ExchangeScope: tc.legacy}
			if err := validateProvider(p); (err == nil) != tc.valid {
				t.Fatalf("validateProvider() = %v, valid=%v", err, tc.valid)
			}
		})
	}
}
