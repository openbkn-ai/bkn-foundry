// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package auth

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/identity"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExternalIdentityRejectsLocalPasswordAndProfileWrites(t *testing.T) {
	for _, source := range []model.Source{model.SourceLDAP, model.Source("sso")} {
		t.Run(string(source), func(t *testing.T) {
			s := newStore(t)
			hash, err := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
			if err != nil {
				t.Fatal(err)
			}
			user := model.User{ID: "external", Account: "external", Source: source, Name: "Remote", Email: "remote@example.com", Enabled: true, PasswordHash: string(hash)}
			if err := s.db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := s.Verify(t.Context(), user.Account, "old-password"); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("external local login: %v", err)
			}
			for _, change := range []func() error{
				func() error { return s.ResetPassword(t.Context(), user.ID, "new-password") },
				func() error { return s.SetPassword(t.Context(), user.ID, "new-password") },
				func() error {
					return s.UpdateUser(t.Context(), user.ID, map[string]any{"name": "Changed", "enabled": false})
				},
			} {
				if err := change(); !errors.Is(err, identity.ErrExternalIdentity) {
					t.Fatalf("external write: %v", err)
				}
			}
			stored, err := s.ByID(t.Context(), user.ID)
			if err != nil || stored.Name != user.Name || stored.PasswordHash != user.PasswordHash || !stored.Enabled {
				t.Fatalf("rejected write changed user: %+v, %v", stored, err)
			}
			if err := s.UpdateUser(t.Context(), user.ID, map[string]any{"enabled": false}); err != nil {
				t.Fatalf("local disable must remain available: %v", err)
			}
		})
	}
}

type externalTestAuthenticator struct{ user *model.User }

func (a externalTestAuthenticator) Verify(context.Context, string, string) (*model.User, error) {
	return a.user, nil
}

func TestExternalIdentityIgnoresHistoricalForcedPasswordChange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"redirect_to":"https://example.test/continue"}`))
	}))
	defer server.Close()
	store := newStore(t)
	user := &model.User{ID: "ldap", Account: "ldap-user", Source: model.SourceLDAP, MustChangePassword: true, Enabled: true}
	if err := store.db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	provider := NewProvider(externalTestAuthenticator{user: user}, NewHydraAdmin(server.URL), store)
	redirect, _, err := provider.Login(t.Context(), "challenge", user.Account, "remote-password", false)
	if err != nil || redirect != "https://example.test/continue" {
		t.Fatalf("external login forced local password change: %s %v", redirect, err)
	}
	if _, _, err := provider.ChangePassword(t.Context(), "challenge", user.Account, "remote-password", "new-password", false); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("external password change: %v", err)
	}
	stored, err := store.ByID(t.Context(), user.ID)
	if err != nil || stored.PasswordHash != "" {
		t.Fatalf("external user gained local password: %+v %v", stored, err)
	}
}
