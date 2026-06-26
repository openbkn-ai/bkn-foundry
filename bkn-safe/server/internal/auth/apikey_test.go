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

// TestRegenerate: rotating the secret invalidates the old plaintext, the new one
// verifies, and the row identity (id/KeyID) is preserved.
func TestRegenerate(t *testing.T) {
	s, db := newAPIKeyStore(t)
	ctx := context.Background()
	seedUser(t, db, "u-1", true, model.AccountTypeOther)
	old, rec, _ := s.Issue(ctx, "u-1", "k", nil)

	// cross-owner cannot regenerate
	if _, _, err := s.Regenerate(ctx, "u-2", rec.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("cross-owner regenerate: want ErrRecordNotFound, got %v", err)
	}

	fresh, rec2, err := s.Regenerate(ctx, "u-1", rec.ID)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if rec2.KeyID != rec.KeyID || rec2.ID != rec.ID {
		t.Errorf("regenerate changed identity: %s/%s -> %s/%s", rec.ID, rec.KeyID, rec2.ID, rec2.KeyID)
	}
	if fresh == old {
		t.Error("regenerate returned the same plaintext")
	}
	// old no longer verifies; new does
	if _, err := s.Verify(ctx, old); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("old key after regenerate: want ErrAPIKeyInvalid, got %v", err)
	}
	if v, err := s.Verify(ctx, fresh); err != nil || v.OwnerID != "u-1" {
		t.Errorf("new key verify = %+v err=%v", v, err)
	}
}

// TestIssueDuplicateNamePerOwner: a user can't reuse a name; different users can.
func TestIssueDuplicateNamePerOwner(t *testing.T) {
	s, db := newAPIKeyStore(t)
	ctx := context.Background()
	seedUser(t, db, "u-1", true, model.AccountTypeOther)
	seedUser(t, db, "u-2", true, model.AccountTypeOther)

	if _, _, err := s.Issue(ctx, "u-1", "ci", nil); err != nil {
		t.Fatalf("first issue: %v", err)
	}
	if _, _, err := s.Issue(ctx, "u-1", "ci", nil); !errors.Is(err, ErrAPIKeyNameTaken) {
		t.Errorf("dup name same owner: want ErrAPIKeyNameTaken, got %v", err)
	}
	// different owner, same name -> allowed
	if _, _, err := s.Issue(ctx, "u-2", "ci", nil); err != nil {
		t.Errorf("same name different owner should be allowed, got %v", err)
	}
	// after deleting, the name frees up
	mine, _ := s.ListByOwner(ctx, "u-1")
	if err := s.DeleteOwned(ctx, "u-1", mine[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Issue(ctx, "u-1", "ci", nil); err != nil {
		t.Errorf("reissue after delete should succeed, got %v", err)
	}
}

// TestKeyFormat: the plaintext is the compact base62 shape and not absurdly long.
func TestKeyFormat(t *testing.T) {
	s, db := newAPIKeyStore(t)
	seedUser(t, db, "u-1", true, model.AccountTypeOther)
	plaintext, _, _ := s.Issue(context.Background(), "u-1", "k", nil)
	if !strings.HasPrefix(plaintext, KeyPrefix) {
		t.Fatalf("missing prefix: %q", plaintext)
	}
	if len(plaintext) > 60 { // ~44 expected; guard against hex-length regressions
		t.Errorf("key too long (%d): %q", len(plaintext), plaintext)
	}
	body := strings.TrimPrefix(plaintext, KeyPrefix)
	kid, secret, ok := strings.Cut(body, "_")
	if !ok || len(kid) != keyIDLen || len(secret) != secretLen {
		t.Errorf("unexpected shape: kid=%q secret=%q", kid, secret)
	}
}

// flip returns a char different from b (to corrupt a secret deterministically).
func flip(b byte) string {
	if b == 'a' {
		return "b"
	}
	return "a"
}
