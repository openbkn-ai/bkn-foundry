package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"bkn-safe/internal/database"
	"bkn-safe/internal/model"
)

func newStore(t *testing.T) *UserStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewUserStore(db)
}

func TestVerifyCredentials(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := &model.User{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true, AccountType: model.AccountTypeOther}
	if err := s.CreateLocalUser(ctx, u, "s3cret"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// success
	got, err := s.Verify(ctx, "alice", "s3cret")
	if err != nil || got.ID != "u-1" {
		t.Fatalf("verify ok: got=%v err=%v", got, err)
	}
	// wrong password
	if _, err := s.Verify(ctx, "alice", "nope"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password: want ErrInvalidCredentials, got %v", err)
	}
	// unknown account (opaque error, no enumeration leak)
	if _, err := s.Verify(ctx, "bob", "x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("unknown account: want ErrInvalidCredentials, got %v", err)
	}
}

func TestVerifyDisabledUser(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := &model.User{ID: "u-2", Account: "carol", Enabled: false}
	if err := s.CreateLocalUser(ctx, u, "pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, "carol", "pw"); !errors.Is(err, ErrUserDisabled) {
		t.Errorf("disabled: want ErrUserDisabled, got %v", err)
	}
}

// TestExtClaims pins the introspect ext contract: all five fields present,
// visitor_type=realname, udid empty, account_type from the user.
func TestExtClaims(t *testing.T) {
	u := &model.User{ID: "u-1", AccountType: model.AccountTypeIDCard}
	ext := ExtClaims(u, "10.0.0.9", "")
	want := map[string]any{
		"visitor_type": "realname",
		"login_ip":     "10.0.0.9",
		"udid":         "",
		"account_type": "id_card",
		"client_type":  "web",
	}
	for k, v := range want {
		if ext[k] != v {
			t.Errorf("ext[%q] = %v, want %v", k, ext[k], v)
		}
	}
	if len(ext) != 5 {
		t.Errorf("ext has %d keys, want 5 (lib panics on missing user-token ext fields)", len(ext))
	}
}

// TestExtClaimsDefaults checks account_type defaults to "other" when unset.
func TestExtClaimsDefaults(t *testing.T) {
	ext := ExtClaims(&model.User{ID: "u-1"}, "1.2.3.4", "")
	if ext["account_type"] != "other" {
		t.Errorf("default account_type = %v, want other", ext["account_type"])
	}
	if ext["client_type"] != "web" {
		t.Errorf("default client_type = %v, want web", ext["client_type"])
	}
}
