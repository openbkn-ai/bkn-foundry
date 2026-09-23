// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package oauthorigin

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

type fakeClient struct {
	uris   auth.OAuthClientURIs
	getErr error
	setErr error
	sets   int
}

func (f *fakeClient) GetOAuthClientURIs(context.Context, string) (auth.OAuthClientURIs, error) {
	if f.getErr != nil {
		return auth.OAuthClientURIs{}, f.getErr
	}
	return auth.OAuthClientURIs{
		RedirectURIs:           append([]string(nil), f.uris.RedirectURIs...),
		PostLogoutRedirectURIs: append([]string(nil), f.uris.PostLogoutRedirectURIs...),
	}, nil
}

func (f *fakeClient) SetOAuthClientURIs(_ context.Context, _ string, uris auth.OAuthClientURIs) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.sets++
	f.uris = uris
	return nil
}

func testService(t *testing.T, client *fakeClient, baseline ...string) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.OAuthAccessOrigin{}, &model.OAuthClientSyncState{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	service, err := New(db, client, baseline)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service, db
}

func TestNormalizeOrigin(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "https default port", raw: " HTTPS://Example.COM:443/ ", want: "https://example.com", ok: true},
		{name: "http custom port", raw: "http://Example.COM:8080", want: "http://example.com:8080", ok: true},
		{name: "ipv6", raw: "http://[2001:db8::1]:80/", want: "http://[2001:db8::1]", ok: true},
		{name: "path", raw: "https://example.com/studio", ok: false},
		{name: "query", raw: "https://example.com/?x=1", ok: false},
		{name: "fragment", raw: "https://example.com/#x", ok: false},
		{name: "userinfo", raw: "https://user@example.com", ok: false},
		{name: "wildcard", raw: "https://*.example.com", ok: false},
		{name: "scheme", raw: "ftp://example.com", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeOrigin(test.raw)
			if test.ok && (err != nil || got != test.want) {
				t.Fatalf("NormalizeOrigin(%q) = %q, %v; want %q", test.raw, got, err, test.want)
			}
			if !test.ok && !errors.Is(err, ErrInvalidOrigin) {
				t.Fatalf("NormalizeOrigin(%q) error = %v, want ErrInvalidOrigin", test.raw, err)
			}
		})
	}
}

func TestReconcileImportsLegacyAndPreservesUnknownURIs(t *testing.T) {
	client := &fakeClient{uris: auth.OAuthClientURIs{
		RedirectURIs: []string{
			"https://primary.example/studio/callback",
			"http://10.0.0.8:30080/studio/callback",
			"https://plugin.example/opaque-callback",
		},
		PostLogoutRedirectURIs: []string{
			"http://10.0.0.8:30080/studio",
			"https://plugin.example/logout",
		},
	}}
	service, db := testService(t, client, "https://primary.example/studio/callback")

	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	wantRedirects := []string{
		"http://10.0.0.8:30080/studio/callback",
		"https://plugin.example/opaque-callback",
		"https://primary.example/studio/callback",
	}
	wantLogouts := []string{
		"http://10.0.0.8:30080/studio",
		"https://plugin.example/logout",
		"https://primary.example/studio",
	}
	if !slices.Equal(client.uris.RedirectURIs, wantRedirects) {
		t.Fatalf("redirect URIs = %v, want %v", client.uris.RedirectURIs, wantRedirects)
	}
	if !slices.Equal(client.uris.PostLogoutRedirectURIs, wantLogouts) {
		t.Fatalf("logout URIs = %v, want %v", client.uris.PostLogoutRedirectURIs, wantLogouts)
	}

	var imported model.OAuthAccessOrigin
	if err := db.First(&imported, "origin = ?", "http://10.0.0.8:30080").Error; err != nil {
		t.Fatalf("legacy origin not imported: %v", err)
	}
	if imported.CreatedBy != "legacy-import" || imported.SyncState != syncStateSynced {
		t.Fatalf("imported row = %+v", imported)
	}
}

func TestDuplicateAddRetriesWhenHydraRecovers(t *testing.T) {
	client := &fakeClient{getErr: errors.New("hydra unavailable")}
	service, _ := testService(t, client, "https://primary.example/studio/callback")

	entry, err := service.Add(context.Background(), "http://10.0.0.8:30080/", "admin-1")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if entry.SyncState != syncStateError || entry.LastSyncError == "" {
		t.Fatalf("entry after outage = %+v", entry)
	}

	client.getErr = nil
	client.uris = auth.OAuthClientURIs{}
	_, err = service.Add(context.Background(), "http://10.0.0.8:30080", "admin-1")
	var duplicate *DuplicateError
	if !errors.As(err, &duplicate) || duplicate.ExistingID != entry.ID {
		t.Fatalf("duplicate add error = %#v, want existing ID %q", err, entry.ID)
	}
	entries, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 || entries[1].SyncState != syncStateSynced {
		t.Fatalf("entries after retry = %+v", entries)
	}
	if !slices.Contains(client.uris.RedirectURIs, "http://10.0.0.8:30080/studio/callback") {
		t.Fatalf("runtime callback missing from Hydra: %v", client.uris.RedirectURIs)
	}
}

func TestNewAcceptsLegacyBaselineCallbackPath(t *testing.T) {
	const legacyCallback = "http://localhost:5173/callback"
	client := &fakeClient{uris: auth.OAuthClientURIs{
		RedirectURIs:           []string{"https://primary.example/studio/callback", legacyCallback},
		PostLogoutRedirectURIs: []string{"https://primary.example/studio", "http://localhost:5173"},
	}}
	service, _ := testService(t, client,
		"https://primary.example/studio/callback",
		legacyCallback,
	)

	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !slices.Contains(client.uris.RedirectURIs, legacyCallback) {
		t.Fatalf("legacy baseline callback was removed: %v", client.uris.RedirectURIs)
	}
	entries, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].Origin != "https://primary.example" {
		t.Fatalf("managed entries = %+v", entries)
	}
}

func TestDeleteRemovesRuntimeButRejectsBaseline(t *testing.T) {
	client := &fakeClient{uris: auth.OAuthClientURIs{}}
	service, _ := testService(t, client, "https://primary.example/studio/callback")
	entry, err := service.Add(context.Background(), "https://external.example", "admin-1")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if synced, err := service.Delete(context.Background(), entry.ID); err != nil || !synced {
		t.Fatalf("delete = %v, %v", synced, err)
	}
	entries, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].Source != "system" {
		t.Fatalf("entries after delete = %+v", entries)
	}
	if _, err := service.Delete(context.Background(), entries[0].ID); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("delete baseline error = %v, want ErrReadOnly", err)
	}
}

func TestDuplicateReturnsExistingID(t *testing.T) {
	client := &fakeClient{uris: auth.OAuthClientURIs{}}
	service, _ := testService(t, client, "https://primary.example/studio/callback")
	first, err := service.Add(context.Background(), "https://external.example", "admin-1")
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	_, err = service.Add(context.Background(), "https://EXTERNAL.example:443/", "admin-1")
	var duplicate *DuplicateError
	if !errors.As(err, &duplicate) || duplicate.ExistingID != first.ID {
		t.Fatalf("duplicate error = %#v, want existing ID %q", err, first.ID)
	}
}
