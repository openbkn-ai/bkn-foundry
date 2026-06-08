// Package auth implements bkn-safe's authentication: a local user store
// (password verified with bcrypt) plus the hydra login/consent/device provider
// that drives the OAuth2 flow and injects the introspect ext claims.
//
// Passwords are validated against bkn-safe's OWN store — NOT eacp/anyshare.
package auth

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"bkn-safe/internal/model"
)

// DefaultInitialPassword is the platform initial password handed to newly
// created local users (and the seeded admin) when no password is specified.
// MustChangePassword is always set, so it must be changed on first login. Single
// source of truth: seed + admin user-create + the CLI all use this value.
const DefaultInitialPassword = "openbkn"

// ErrInvalidCredentials is returned when account/password verification fails.
// It is deliberately opaque (no "user not found" vs "wrong password" leak).
var ErrInvalidCredentials = errors.New("invalid account or password")

// ErrUserDisabled is returned when the account exists but is disabled.
var ErrUserDisabled = errors.New("user disabled")

// ErrMustChangePassword is returned when credentials are valid but the user must
// change their password before the login can be accepted (e.g. the seeded admin
// on first login). The caller should drive the change-password flow.
var ErrMustChangePassword = errors.New("must change password")

// Authenticator verifies credentials. The local implementation checks bcrypt
// hashes; an LDAP-backed implementation (Phase 5) federates to an external
// directory. Both return the resolved local user.
type Authenticator interface {
	Verify(ctx context.Context, account, password string) (*model.User, error)
}

// UserStore is the local (bcrypt) authenticator backed by GORM.
type UserStore struct {
	db *gorm.DB
}

// NewUserStore builds a local user store.
func NewUserStore(db *gorm.DB) *UserStore { return &UserStore{db: db} }

// Verify checks account+password against the local store. Returns
// ErrInvalidCredentials on any mismatch and ErrUserDisabled for disabled users.
func (s *UserStore) Verify(ctx context.Context, account, password string) (*model.User, error) {
	var u model.User
	err := s.db.WithContext(ctx).First(&u, "account = ?", account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if u.PasswordHash == "" {
		// No local password (e.g. an LDAP/app identity) — not a local login.
		return nil, ErrInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	if !u.Enabled {
		return nil, ErrUserDisabled
	}
	return &u, nil
}

// CreateLocalUser creates a local user with a bcrypt-hashed password. The
// password is admin-assigned, so MustChangePassword is forced on: the user must
// change it on first login (same rule as an admin reset).
func (s *UserStore) CreateLocalUser(ctx context.Context, u *model.User, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(hash)
	u.Source = model.SourceLocal
	u.MustChangePassword = true
	return s.db.WithContext(ctx).Create(u).Error
}

// UpdateUser patches mutable profile fields of a user (name/email/telephone/
// enabled/account_type — NOT account or password, which have their own paths).
// Returns gorm.ErrRecordNotFound when no row matches.
func (s *UserStore) UpdateUser(ctx context.Context, id string, fields map[string]any) error {
	res := s.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteUser removes a user and its directory memberships (department links and
// group memberships) in one transaction. Casbin role/permission cleanup is the
// caller's job (it holds the enforcer). Returns gorm.ErrRecordNotFound when the
// user doesn't exist.
func (s *UserStore) DeleteUser(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ?", id).Delete(&model.User{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("user_id = ?", id).Delete(&model.UserDepartment{}).Error; err != nil {
			return err
		}
		return tx.Where("member_id = ?", id).Delete(&model.GroupMember{}).Error
	})
}

// ByID fetches a user by ID (implements UserLookup).
func (s *UserStore) ByID(ctx context.Context, id string) (*model.User, error) {
	var u model.User
	if err := s.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// ChangePassword is the self-service password change: it re-verifies the
// current (old) password, then sets the new one (which also clears
// MustChangePassword). Returns the same opaque errors as Verify on a bad old
// password / disabled user. Drives the CLI change-password path — no hydra
// challenge, just a credential update.
func (s *UserStore) ChangePassword(ctx context.Context, account, oldPassword, newPassword string) error {
	u, err := s.Verify(ctx, account, oldPassword)
	if err != nil {
		return err
	}
	return s.SetPassword(ctx, u.ID, newPassword)
}

// ResetPassword is the ADMIN reset: it sets a new password AND forces
// MustChangePassword on, so the user must change the admin-assigned password on
// their next login. Contrast SetPassword (self-service / forced-change
// completion), which clears the flag.
func (s *UserStore) ResetPassword(ctx context.Context, userID, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Updates(map[string]any{"password_hash": string(hash), "must_change_password": true}).Error
}

// SetPassword updates a local user's password and clears MustChangePassword
// (a successful change always satisfies a forced-change requirement).
func (s *UserStore) SetPassword(ctx context.Context, userID, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Updates(map[string]any{"password_hash": string(hash), "must_change_password": false}).Error
}
