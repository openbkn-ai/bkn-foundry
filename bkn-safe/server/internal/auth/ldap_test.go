// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	ldapv3 "github.com/go-ldap/ldap/v3"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// fakeConn is an in-memory LDAP connection for tests. The user DN
// "uid=alice,dc=x" binds only with password "correct".
type fakeConn struct {
	userDN string
}

func (f *fakeConn) Bind(dn, password string) error {
	if dn == "cn=svc,dc=x" { // service bind
		return nil
	}
	if dn == f.userDN && password == "correct" {
		return nil
	}
	return errors.New("invalid credentials")
}

func (f *fakeConn) Search(*ldapv3.SearchRequest) (*ldapv3.SearchResult, error) {
	e := &ldapv3.Entry{DN: f.userDN, Attributes: []*ldapv3.EntryAttribute{
		{Name: "cn", Values: []string{"Alice LDAP"}},
		{Name: "mail", Values: []string{"alice@corp.example"}},
	}}
	return &ldapv3.SearchResult{Entries: []*ldapv3.Entry{e}}, nil
}

func (f *fakeConn) Close() error { return nil }

func newLDAP(t *testing.T) (*LDAPAuthenticator, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := config.LDAPConfig{
		URL: "ldap://fake", BindDN: "cn=svc,dc=x", BindPassword: "svc",
		BaseDN: "dc=x", UserFilter: "(uid=%s)",
	}
	a := NewLDAPAuthenticator(cfg, db)
	a.dial = func(string) (ldapConn, error) { return &fakeConn{userDN: "uid=alice,dc=x"}, nil }
	return a, db
}

func TestLDAPVerifyAndProvision(t *testing.T) {
	a, db := newLDAP(t)
	ctx := context.Background()

	u, err := a.Verify(ctx, "alice", "correct")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if u.Source != model.SourceLDAP || u.Name != "Alice LDAP" || u.Email != "alice@corp.example" {
		t.Errorf("provisioned user = %+v", u)
	}
	// provisioned locally, idempotent on second login (same id).
	u2, err := a.Verify(ctx, "alice", "correct")
	if err != nil {
		t.Fatal(err)
	}
	if u2.ID != u.ID {
		t.Errorf("re-login created a new user: %s != %s", u2.ID, u.ID)
	}
	var count int64
	db.Model(&model.User{}).Where("account = ?", "alice").Count(&count)
	if count != 1 {
		t.Errorf("provisioned %d users, want 1", count)
	}
}

func TestLDAPWrongPassword(t *testing.T) {
	a, _ := newLDAP(t)
	if _, err := a.Verify(context.Background(), "alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password: want ErrInvalidCredentials, got %v", err)
	}
}

// TestChainFallsThrough confirms the chain tries the next authenticator on a
// credential failure and returns the first success.
func TestChainFallsThrough(t *testing.T) {
	a, _ := newLDAP(t)
	// local store with no matching user -> always ErrInvalidCredentials.
	local := NewUserStore(a.db)
	chain := NewChain(local, a)
	u, err := chain.Verify(context.Background(), "alice", "correct")
	if err != nil {
		t.Fatalf("chain verify: %v", err)
	}
	if u.Source != model.SourceLDAP {
		t.Errorf("expected LDAP user, got source %s", u.Source)
	}
}

func TestLDAPDisabledAccountCannotLogin(t *testing.T) {
	a, db := newLDAP(t)
	u, err := a.Verify(t.Context(), "alice", "correct")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.User{}).Where("id = ?", u.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := a.Verify(t.Context(), "alice", "correct"); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("LDAP login = %v, want disabled", err)
	}
	if _, err := a.Verify(t.Context(), "alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password = %v, want invalid credentials", err)
	}
}

func TestChainDoesNotFallbackAfterDisabledLocalLogin(t *testing.T) {
	a, _ := newLDAP(t)
	local := NewUserStore(a.db)
	if err := local.CreateLocalUser(t.Context(), &model.User{ID: "disabled-local", Account: "alice", Enabled: false}, "correct"); err != nil {
		t.Fatal(err)
	}
	a.dial = func(string) (ldapConn, error) {
		t.Error("disabled local login must not fall back to LDAP")
		return &fakeConn{userDN: "uid=alice,dc=x"}, nil
	}
	if _, err := NewChain(local, a).Verify(t.Context(), "alice", "correct"); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("chain login = %v, want disabled", err)
	}
}
