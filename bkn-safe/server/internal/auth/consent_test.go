// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestIDTokenClaimsFollowGrantedScope(t *testing.T) {
	updated := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	u := &model.User{
		ID: "u-1", Account: "zhangsan", Name: "Zhang San", Email: "zs@example.com",
		Telephone: "13800000000", UpdatedAt: updated,
	}
	profile := map[string]any{"name": "Zhang San", "preferred_username": "zhangsan", "updated_at": updated.Unix()}
	cases := []struct {
		name  string
		scope []string
		want  map[string]any
	}{
		{"openid only", []string{"openid", "offline"}, map[string]any{}},
		{"profile", []string{"openid", "profile"}, profile},
		{"email", []string{"openid", "email"}, map[string]any{"email": "zs@example.com"}},
		{"phone", []string{"openid", "phone"}, map[string]any{"phone_number": "13800000000"}},
		{"all", []string{"openid", "profile", "email", "phone"}, map[string]any{
			"name": "Zhang San", "preferred_username": "zhangsan", "updated_at": updated.Unix(),
			"email": "zs@example.com", "phone_number": "13800000000",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IDTokenClaims(u, tc.scope); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("IDTokenClaims(%v) = %v, want %v", tc.scope, got, tc.want)
			}
		})
	}
}

func TestIDTokenClaimsLeaveOutEmptyValues(t *testing.T) {
	got := IDTokenClaims(&model.User{ID: "u-1", Account: "lisi"}, []string{"openid", "profile", "email", "phone"})
	want := map[string]any{"preferred_username": "lisi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IDTokenClaims = %v, want %v", got, want)
	}
}

// consentBody captures the accept-consent request bkn-safe sends to hydra.
type consentBody struct {
	GrantScope []string `json:"grant_scope"`
	Session    struct {
		AccessToken map[string]any `json:"access_token"`
		IDToken     map[string]any `json:"id_token"`
	} `json:"session"`
}

func fakeConsentHydra(t *testing.T, scope []string) (*HydraAdmin, *consentBody) {
	t.Helper()
	got := &consentBody{}
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/oauth2/auth/requests/consent", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"challenge":       "c-1",
			"subject":         "u-1",
			"requested_scope": scope,
			"client":          map[string]any{"client_id": "openbkn-studio"},
		})
	})
	mux.HandleFunc("/admin/oauth2/auth/requests/consent/accept", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Errorf("decode accept body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"redirect_to": "https://example.com/done"})
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewHydraAdmin(srv.URL), got
}

type stubUsers struct{ u *model.User }

func (s stubUsers) ByID(context.Context, string) (*model.User, error) { return s.u, nil }
func (s stubUsers) SetPassword(context.Context, string, string) error { return nil }

// TestConsentPutsProfileInIDTokenAndKeepsExt checks that consent fills the ID
// token for the granted scopes and leaves the frozen access-token ext as is.
func TestConsentPutsProfileInIDTokenAndKeepsExt(t *testing.T) {
	updated := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	u := &model.User{
		ID: "u-1", Account: "zhangsan", Name: "Zhang San", Email: "zs@example.com",
		Telephone: "13800000000", UpdatedAt: updated, Enabled: true,
	}
	h, got := fakeConsentHydra(t, []string{"openid", "offline", "profile", "email", "phone"})
	p := NewProvider(nil, h, stubUsers{u})

	if _, err := p.Consent(context.Background(), "c-1", "10.0.0.9", "", false); err != nil {
		t.Fatalf("consent: %v", err)
	}
	// JSON numbers decode as float64.
	wantID := map[string]any{
		"name": "Zhang San", "preferred_username": "zhangsan", "updated_at": float64(updated.Unix()),
		"email": "zs@example.com", "phone_number": "13800000000",
	}
	if !reflect.DeepEqual(got.Session.IDToken, wantID) {
		t.Errorf("id_token = %v, want %v", got.Session.IDToken, wantID)
	}
	wantExt := map[string]any{}
	for k, v := range ExtClaims(u, "10.0.0.9", "") {
		wantExt[k] = v
	}
	if !reflect.DeepEqual(got.Session.AccessToken, wantExt) {
		t.Errorf("access_token ext = %v, want %v", got.Session.AccessToken, wantExt)
	}
	assertFrozenExtKeys(t, got.Session.AccessToken)
}

// assertFrozenExtKeys pins the access-token ext to the five frozen contract
// fields, so no profile claim can leak into what services introspect.
func assertFrozenExtKeys(t *testing.T, ext map[string]any) {
	t.Helper()
	want := map[string]bool{"visitor_type": true, "login_ip": true, "udid": true, "account_type": true, "client_type": true}
	if len(ext) != len(want) {
		t.Errorf("access_token ext has keys %v, want exactly the five frozen fields", ext)
	}
	for k := range ext {
		if !want[k] {
			t.Errorf("access_token ext carries unexpected key %q", k)
		}
	}
}

// TestConsentWithoutProfileScopeSendsNoIDTokenClaims keeps today's behaviour
// for clients that ask only for openid/offline.
func TestConsentWithoutProfileScopeSendsNoIDTokenClaims(t *testing.T) {
	u := &model.User{ID: "u-1", Account: "zhangsan", Name: "Zhang San", Email: "zs@example.com", Enabled: true}
	h, got := fakeConsentHydra(t, []string{"openid", "offline"})
	p := NewProvider(nil, h, stubUsers{u})

	if _, err := p.Consent(context.Background(), "c-1", "10.0.0.9", "", false); err != nil {
		t.Fatalf("consent: %v", err)
	}
	if got.Session.IDToken != nil {
		t.Errorf("id_token = %v, want none", got.Session.IDToken)
	}
	assertFrozenExtKeys(t, got.Session.AccessToken)
}
