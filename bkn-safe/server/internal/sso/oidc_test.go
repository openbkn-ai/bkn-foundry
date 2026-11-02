package sso

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVerifyIDTokenRejectsWrongNonceAndAudience(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := fmt.Sprintf(`{"keys":[{"kid":"key-1","kty":"RSA","alg":"RS256","use":"sig","n":%q,"e":"AQAB"}]}`,
		base64.RawURLEncoding.EncodeToString(key.N.Bytes()))
	service := New(nil, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://issuer.example/jwks" {
			t.Fatalf("unexpected JWKS URL: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(jwks)), Header: make(http.Header)}, nil
	})})
	provider := &model.SSOProvider{Issuer: "https://issuer.example", ClientID: "studio"}
	metadata := &metadata{JWKS: "https://issuer.example/jwks"}
	makeToken := func(audience, nonce string) string {
		t.Helper()
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"key-1"}`))
		claims, _ := json.Marshal(map[string]any{"iss": provider.Issuer, "sub": "user-1", "aud": audience,
			"nonce": nonce, "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix()})
		payload := base64.RawURLEncoding.EncodeToString(claims)
		input := header + "." + payload
		digest := sha256.Sum256([]byte(input))
		sig, signErr := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if signErr != nil {
			t.Fatal(signErr)
		}
		return input + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	if sub, err := service.verifyIDToken(context.Background(), provider, metadata, makeToken("studio", "nonce-1"), "nonce-1"); err != nil || sub != "user-1" {
		t.Fatalf("valid ID token: sub=%q err=%v", sub, err)
	}
	for _, tc := range []struct{ audience, nonce, expectedNonce string }{
		{"studio", "nonce-1", "nonce-2"},
		{"another-app", "nonce-1", "nonce-1"},
	} {
		if _, err := service.verifyIDToken(context.Background(), provider, metadata,
			makeToken(tc.audience, tc.nonce), tc.expectedNonce); err == nil {
			t.Fatalf("accepted audience=%q nonce=%q expected=%q", tc.audience, tc.nonce, tc.expectedNonce)
		}
	}
}

func TestVerifyIDTokenRejectsUnsignedToken(t *testing.T) {
	service := New(nil, nil)
	_, err := service.verifyIDToken(context.Background(), &model.SSOProvider{}, &metadata{}, "a.b.c", "nonce")
	if err == nil {
		t.Fatal("accepted invalid ID token")
	}
}
