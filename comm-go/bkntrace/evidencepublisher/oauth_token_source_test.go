package evidencepublisher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOAuthTokenSourceUsesClientCredentialsPostAndCachesToken(t *testing.T) {
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request: %v", err)
		}
		if request.Method != http.MethodPost || request.Form.Get("grant_type") != "client_credentials" ||
			request.Form.Get("client_id") != "publisher-a" || request.Form.Get("client_secret") != "secret-a" {
			t.Fatalf("unexpected OAuth request: method=%s form=%v", request.Method, request.Form)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "" {
			t.Fatalf("client_secret_post request carried Authorization header %q", authorization)
		}
		body, err := json.Marshal(map[string]any{"access_token": "short-lived", "token_type": "Bearer", "expires_in": 120})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}

	source, err := NewOAuthTokenSource(OAuthTokenConfig{
		TokenURL: "https://bkn-safe.example/oauth2/token", ClientID: "publisher-a", ClientSecret: "secret-a", HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewOAuthTokenSource() error = %v", err)
	}
	for range 2 {
		token, tokenErr := source.Token(context.Background())
		if tokenErr != nil || token != "short-lived" {
			t.Fatalf("Token() = %q, %v", token, tokenErr)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("token requests = %d, want 1 cached request", got)
	}
}

func TestOAuthTokenSourceRejectsMissingRuntimeCredentials(t *testing.T) {
	for name, config := range map[string]OAuthTokenConfig{
		"token URL":     {ClientID: "publisher-a", ClientSecret: "secret-a"},
		"client ID":     {TokenURL: "https://safe.example/token", ClientSecret: "secret-a"},
		"client secret": {TokenURL: "https://safe.example/token", ClientID: "publisher-a"},
		"malformed URL": {TokenURL: "://", ClientID: "publisher-a", ClientSecret: "secret-a"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewOAuthTokenSource(config); err == nil {
				t.Fatal("NewOAuthTokenSource() accepted incomplete runtime configuration")
			}
		})
	}
}

func TestOAuthTokenSourceDoesNotTreatTokenURLAsCredentials(t *testing.T) {
	config := OAuthTokenConfig{TokenURL: (&url.URL{Scheme: "https", Host: "safe.example", Path: "/oauth2/token"}).String(), ClientID: "publisher-a", ClientSecret: "secret-a"}
	if _, err := NewOAuthTokenSource(config); err != nil {
		t.Fatalf("NewOAuthTokenSource() error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
