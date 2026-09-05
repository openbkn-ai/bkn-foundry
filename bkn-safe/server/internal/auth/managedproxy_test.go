// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func protectedProxyFixture(t *testing.T) (*gorm.DB, *model.User) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	account, _, err := managedproxy.New(db).Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-protected",
	})
	if err != nil {
		t.Fatalf("create proxy: %v", err)
	}
	var user model.User
	if err := db.First(&user, "id = ?", account.ProxyAccountID).Error; err != nil {
		t.Fatalf("load proxy: %v", err)
	}
	return db, &user
}

func TestManagedProxyCannotLoginEvenIfPasswordIsInjected(t *testing.T) {
	db, user := protectedProxyFixture(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := db.Model(&model.User{}).Where("id = ?", user.ID).Update("password_hash", string(hash)).Error; err != nil {
		t.Fatalf("inject password: %v", err)
	}
	_, err = NewUserStore(db).Verify(t.Context(), user.Account, "secret")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Verify() error = %v, want opaque invalid credentials", err)
	}
	if !errors.Is(err, ErrManagedLoginDisabled) {
		t.Fatalf("Verify() error = %v, want managed-login sentinel", err)
	}
}

func TestManagedProxyLoginDoesNotFallThroughAuthenticatorChain(t *testing.T) {
	db, user := protectedProxyFixture(t)
	called := false
	chain := NewChain(NewUserStore(db), authenticatorFunc(func(context.Context, string, string) (*model.User, error) {
		called = true
		return user, nil
	}))
	_, err := chain.Verify(t.Context(), user.Account, "anything")
	if !errors.Is(err, ErrManagedLoginDisabled) || called {
		t.Fatalf("Chain.Verify() = (%v, LDAP called=%v), want terminal managed-login denial", err, called)
	}
}

type authenticatorFunc func(context.Context, string, string) (*model.User, error)

func (f authenticatorFunc) Verify(ctx context.Context, account, password string) (*model.User, error) {
	return f(ctx, account, password)
}

func TestManagedProxyCannotReceiveOrRotateCredentials(t *testing.T) {
	db, user := protectedProxyFixture(t)
	keys := NewAPIKeyStore(db)
	if _, _, err := keys.Issue(t.Context(), user.ID, "forbidden", nil); !errors.Is(err, managedproxy.ErrManagedAccount) {
		t.Fatalf("Issue() error = %v, want ErrManagedAccount", err)
	}
	if _, _, err := keys.Regenerate(t.Context(), user.ID, "missing"); !errors.Is(err, managedproxy.ErrManagedAccount) {
		t.Fatalf("Regenerate() error = %v, want ErrManagedAccount", err)
	}
	if err := NewUserStore(db).ResetPassword(t.Context(), user.ID, "secret"); !errors.Is(err, managedproxy.ErrManagedAccount) {
		t.Fatalf("ResetPassword() error = %v, want ErrManagedAccount", err)
	}
}

func TestManagedProxyCannotUseGenericUserMutationPaths(t *testing.T) {
	db, user := protectedProxyFixture(t)
	users := NewUserStore(db)
	if err := users.UpdateUser(t.Context(), user.ID, map[string]any{"enabled": false}); !errors.Is(err, managedproxy.ErrManagedAccount) {
		t.Fatalf("UpdateUser() error = %v, want ErrManagedAccount", err)
	}
	if err := users.DeleteUser(t.Context(), user.ID); !errors.Is(err, managedproxy.ErrManagedAccount) {
		t.Fatalf("DeleteUser() error = %v, want ErrManagedAccount", err)
	}
}
