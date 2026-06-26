package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"bkn-safe/internal/model"
)

// KeyPrefix marks an AppKey credential. The MCP/REST gateway branches on this
// prefix to route verification here (vs hydra introspection). Plaintext shape:
// "bak_<keyid>_<secret>" — both halves are base62 (no '_'), so the separator is
// unambiguous and the key is copy/URL-safe.
const KeyPrefix = "bak_"

// Key half lengths in base62 chars. keyIDLen → ~71 bits (collision-safe lookup
// id); secretLen → ~160 bits (well past brute-force). Total plaintext ≈ 44 chars
// (vs the old 101-char hex), comparable to GitHub/Stripe tokens.
const (
	keyIDLen  = 12
	secretLen = 27
)

// base62Alphabet is the credential charset: digits + letters, no '_'/'-', so it
// never collides with the key separator and is safe in URLs/headers.
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// ErrAPIKeyInvalid is the opaque verification failure: unknown/disabled/expired
// key, bad secret, or a key whose owner is gone/disabled. Deliberately does not
// distinguish causes to callers.
var ErrAPIKeyInvalid = errors.New("invalid api key")

// APIKeyStore issues, lists, revokes and verifies AppKeys, backed by GORM.
type APIKeyStore struct {
	db *gorm.DB
}

// NewAPIKeyStore builds an AppKey store.
func NewAPIKeyStore(db *gorm.DB) *APIKeyStore { return &APIKeyStore{db: db} }

// VerifiedKey is the resolved identity behind a valid AppKey: the owner it acts
// as. AccountType mirrors the owner User row so downstream authz behaves exactly
// as if the owner presented an OAuth token.
type VerifiedKey struct {
	KeyID       string
	OwnerID     string
	AccountType model.AccountType
}

// Issue mints a new AppKey for ownerID and returns the ONE-TIME plaintext key
// (never recoverable afterwards) plus the stored record (no secret). expiresAt
// nil = never expires. The caller has already authenticated the owner, so the key
// can never grant more than the owner holds.
func (s *APIKeyStore) Issue(ctx context.Context, ownerID, name string, expiresAt *time.Time) (string, *model.APIKey, error) {
	keyID := randBase62(keyIDLen)   // public lookup half
	secret := randBase62(secretLen) // the secret half

	rec := &model.APIKey{
		ID:          NewID(),
		KeyID:       keyID,
		OwnerUserID: ownerID,
		Name:        name,
		SecretHash:  hashSecret(secret),
		ExpiresAt:   expiresAt,
		Enabled:     true,
	}
	if err := s.db.WithContext(ctx).Create(rec).Error; err != nil {
		return "", nil, err
	}
	plaintext := KeyPrefix + keyID + "_" + secret
	return plaintext, rec, nil
}

// Verify validates a plaintext AppKey and resolves the owner identity. It checks,
// in order: prefix/shape, key exists, key enabled, not expired, secret matches
// (constant-time), owner user exists and is enabled. On success it best-effort
// stamps LastUsedAt. Any failure returns ErrAPIKeyInvalid (opaque).
func (s *APIKeyStore) Verify(ctx context.Context, plaintext string) (*VerifiedKey, error) {
	keyID, secret, ok := splitKey(plaintext)
	if !ok {
		return nil, ErrAPIKeyInvalid
	}

	var rec model.APIKey
	err := s.db.WithContext(ctx).First(&rec, "key_id = ?", keyID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAPIKeyInvalid
	}
	if err != nil {
		return nil, err
	}
	if !rec.Enabled {
		return nil, ErrAPIKeyInvalid
	}
	if rec.ExpiresAt != nil && time.Now().After(*rec.ExpiresAt) {
		return nil, ErrAPIKeyInvalid
	}
	// Constant-time secret comparison over the hex digests.
	if subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(rec.SecretHash)) != 1 {
		return nil, ErrAPIKeyInvalid
	}

	// The key acts AS its owner — a disabled/deleted owner invalidates the key.
	var owner model.User
	err = s.db.WithContext(ctx).First(&owner, "id = ?", rec.OwnerUserID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAPIKeyInvalid
	}
	if err != nil {
		return nil, err
	}
	if !owner.Enabled {
		return nil, ErrAPIKeyInvalid
	}

	// Best-effort last-used stamp; never fail verification on a write error.
	now := time.Now()
	_ = s.db.WithContext(ctx).Model(&model.APIKey{}).
		Where("key_id = ?", keyID).Update("last_used_at", now).Error

	return &VerifiedKey{KeyID: keyID, OwnerID: owner.ID, AccountType: owner.AccountType}, nil
}

// ListByOwner returns ownerID's keys (newest first), never including any secret.
func (s *APIKeyStore) ListByOwner(ctx context.Context, ownerID string) ([]model.APIKey, error) {
	var keys []model.APIKey
	err := s.db.WithContext(ctx).
		Where("owner_user_id = ?", ownerID).
		Order("created_at DESC").Find(&keys).Error
	return keys, err
}

// ListAll returns every key (admin view), optionally filtered by owner. Never
// includes any secret.
func (s *APIKeyStore) ListAll(ctx context.Context, ownerFilter string) ([]model.APIKey, error) {
	q := s.db.WithContext(ctx).Model(&model.APIKey{})
	if ownerFilter != "" {
		q = q.Where("owner_user_id = ?", ownerFilter)
	}
	var keys []model.APIKey
	err := q.Order("created_at DESC").Find(&keys).Error
	return keys, err
}

// DeleteOwned revokes a key only if it belongs to ownerID (self-service guard).
// Returns gorm.ErrRecordNotFound when no such key is owned by the caller.
func (s *APIKeyStore) DeleteOwned(ctx context.Context, ownerID, id string) error {
	res := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, ownerID).Delete(&model.APIKey{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Delete revokes any key by id (admin). Returns gorm.ErrRecordNotFound when the
// key does not exist.
func (s *APIKeyStore) Delete(ctx context.Context, id string) error {
	res := s.db.WithContext(ctx).Where("id = ?", id).Delete(&model.APIKey{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Regenerate rotates the secret of an existing key the caller owns: same row /
// KeyID / name / expiry, brand-new secret. The OLD plaintext stops verifying
// immediately (its secret no longer matches the stored hash); the new one-time
// plaintext is returned. Re-enables a soft-disabled key and clears LastUsedAt.
// This is the "I lost it / rotate on suspected leak" path — no need to recreate
// and rename. Returns gorm.ErrRecordNotFound when the caller owns no such key.
func (s *APIKeyStore) Regenerate(ctx context.Context, ownerID, id string) (string, *model.APIKey, error) {
	var rec model.APIKey
	err := s.db.WithContext(ctx).First(&rec, "id = ? AND owner_user_id = ?", id, ownerID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil, gorm.ErrRecordNotFound
	}
	if err != nil {
		return "", nil, err
	}
	secret := randBase62(secretLen)
	rec.SecretHash = hashSecret(secret)
	rec.LastUsedAt = nil
	rec.Enabled = true
	if err := s.db.WithContext(ctx).Model(&model.APIKey{}).Where("id = ?", id).
		Updates(map[string]any{"secret_hash": rec.SecretHash, "last_used_at": nil, "enabled": true}).Error; err != nil {
		return "", nil, err
	}
	return KeyPrefix + rec.KeyID + "_" + secret, &rec, nil
}

// splitKey parses "bak_<keyid>_<secret>" into its two halves. ok=false on any
// shape mismatch (missing prefix, missing separator, empty half).
func splitKey(plaintext string) (keyID, secret string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(plaintext), KeyPrefix)
	if !found {
		return "", "", false
	}
	keyID, secret, found = strings.Cut(rest, "_")
	if !found || keyID == "" || secret == "" {
		return "", "", false
	}
	return keyID, secret, true
}

// randBase62 returns n cryptographically-random base62 chars. Rejection sampling
// (drop bytes >= 248 = 4*62) keeps the distribution uniform — no modulo bias.
func randBase62(n int) string {
	out := make([]byte, 0, n)
	for len(out) < n {
		buf := make([]byte, n)
		_, _ = rand.Read(buf)
		for _, b := range buf {
			if b < 248 {
				out = append(out, base62Alphabet[int(b)%62])
				if len(out) == n {
					break
				}
			}
		}
	}
	return string(out)
}

// hashSecret returns the sha256 hex digest of a secret (high-entropy input, so a
// plain hash — not bcrypt — is sufficient and keeps per-request verify cheap).
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
