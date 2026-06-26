package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"bkn-safe/internal/database"
	"bkn-safe/internal/model"
)

func newAPIKeyStore(t *testing.T) (*APIKeyStore, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewAPIKeyStore(db), db
}

func seedUser(t *testing.T, db *gorm.DB, id string, enabled bool, at model.AccountType) {
	t.Helper()
	if err := db.Create(&model.User{ID: id, Account: id, Enabled: enabled, AccountType: at}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func TestIssueAndVerify(t *testing.T) {
	s, db := newAPIKeyStore(t)
	ctx := context.Background()
	seedUser(t, db, "u-1", true, model.AccountTypeOther)

	plaintext, rec, err := s.Issue(ctx, "u-1", "ci-bot", nil) // never expires
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.HasPrefix(plaintext, KeyPrefix) {
		t.Errorf("plaintext missing prefix: %q", plaintext)
	}
	if rec.SecretHash == "" || strings.Contains(plaintext, rec.SecretHash) {
		t.Errorf("secret must be hashed at rest and not equal plaintext")
	}

	v, err := s.Verify(ctx, plaintext)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.OwnerID != "u-1" || v.AccountType != model.AccountTypeOther {
		t.Errorf("verify identity = %+v, want owner u-1 / other", v)
	}

	// LastUsedAt is stamped on successful verify.
	var after model.APIKey
	if err := db.First(&after, "key_id = ?", v.KeyID).Error; err != nil {
		t.Fatal(err)
	}
	if after.LastUsedAt == nil {
		t.Error("LastUsedAt not stamped after verify")
	}
}

func TestVerifyAppAccountType(t *testing.T) {
	s, db := newAPIKeyStore(t)
	ctx := context.Background()
	seedUser(t, db, "app-1", true, model.AccountTypeApp)
	plaintext, _, _ := s.Issue(ctx, "app-1", "svc", nil)
	v, err := s.Verify(ctx, plaintext)
	if err != nil || v.AccountType != model.AccountTypeApp {
		t.Fatalf("app key verify = %+v err=%v", v, err)
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	s, db := newAPIKeyStore(t)
	ctx := context.Background()
	seedUser(t, db, "u-1", true, model.AccountTypeOther)
	plaintext, _, _ := s.Issue(ctx, "u-1", "k", nil)

	// Flip the last char of the secret half.
	tampered := plaintext[:len(plaintext)-1] + flip(plaintext[len(plaintext)-1])
	if _, err := s.Verify(ctx, tampered); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("tampered secret: want ErrAPIKeyInvalid, got %v", err)
	}
}

func TestVerifyExpired(t *testing.T) {
	s, db := newAPIKeyStore(t)
	ctx := context.Background()
	seedUser(t, db, "u-1", true, model.AccountTypeOther)
	past := time.Now().Add(-time.Hour)
	plaintext, _, _ := s.Issue(ctx, "u-1", "k", &past)
	if _, err := s.Verify(ctx, plaintext); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("expired: want ErrAPIKeyInvalid, got %v", err)
	}
}

func TestVerifyDisabledKey(t *testing.T) {
	s, db := newAPIKeyStore(t)
	ctx := context.Background()
	seedUser(t, db, "u-1", true, model.AccountTypeOther)
	plaintext, rec, _ := s.Issue(ctx, "u-1", "k", nil)
	db.Model(&model.APIKey{}).Where("id = ?", rec.ID).Update("enabled", false)
	if _, err := s.Verify(ctx, plaintext); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("disabled key: want ErrAPIKeyInvalid, got %v", err)
	}
}

func TestVerifyDisabledOwnerInvalidatesKey(t *testing.T) {
	s, db := newAPIKeyStore(t)
	ctx := context.Background()
	seedUser(t, db, "u-1", true, model.AccountTypeOther)
	plaintext, _, _ := s.Issue(ctx, "u-1", "k", nil)
	// Disabling the owner must immediately invalidate its keys.
	db.Model(&model.User{}).Where("id = ?", "u-1").Update("enabled", false)
	if _, err := s.Verify(ctx, plaintext); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("disabled owner: want ErrAPIKeyInvalid, got %v", err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	s, _ := newAPIKeyStore(t)
	ctx := context.Background()
	for _, bad := range []string{"", "notakey", "bak_", "bak_onlyid", "ory_at_xyz", KeyPrefix + "_"} {
		if _, err := s.Verify(ctx, bad); !errors.Is(err, ErrAPIKeyInvalid) {
			t.Errorf("malformed %q: want ErrAPIKeyInvalid, got %v", bad, err)
		}
	}
}

func TestDeleteOwnedGuardsOwnership(t *testing.T) {
	s, db := newAPIKeyStore(t)
	ctx := context.Background()
	seedUser(t, db, "u-1", true, model.AccountTypeOther)
	seedUser(t, db, "u-2", true, model.AccountTypeOther)
	plaintext, rec, _ := s.Issue(ctx, "u-1", "k", nil)

	// Another user cannot revoke u-1's key.
	if err := s.DeleteOwned(ctx, "u-2", rec.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("cross-owner delete: want ErrRecordNotFound, got %v", err)
	}
	// The owner can, after which the key no longer verifies.
	if err := s.DeleteOwned(ctx, "u-1", rec.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, err := s.Verify(ctx, plaintext); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("verify after revoke: want ErrAPIKeyInvalid, got %v", err)
	}
}

func TestListByOwnerExcludesOthers(t *testing.T) {
	s, db := newAPIKeyStore(t)
	ctx := context.Background()
	seedUser(t, db, "u-1", true, model.AccountTypeOther)
	seedUser(t, db, "u-2", true, model.AccountTypeOther)
	s.Issue(ctx, "u-1", "a", nil)
	s.Issue(ctx, "u-1", "b", nil)
	s.Issue(ctx, "u-2", "c", nil)

	mine, err := s.ListByOwner(ctx, "u-1")
	if err != nil || len(mine) != 2 {
		t.Fatalf("ListByOwner u-1 = %d err=%v, want 2", len(mine), err)
	}
	all, err := s.ListAll(ctx, "")
	if err != nil || len(all) != 3 {
		t.Fatalf("ListAll = %d err=%v, want 3", len(all), err)
	}
}

// flip returns a hex char different from b (to corrupt a secret deterministically).
func flip(b byte) string {
	if b == 'a' {
		return "b"
	}
	return "a"
}
