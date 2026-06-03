package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	ldapv3 "github.com/go-ldap/ldap/v3"
	"gorm.io/gorm"

	"bkn-safe/config"
	"bkn-safe/internal/model"
)

// LDAPAuthenticator federates authentication to an external LDAP/AD directory
// (the "light" external-integration option). On a successful bind it provisions
// a local user record (source=ldap, no local password) so roles and directory
// data attach locally. This is the only external coupling — heavy IAM is
// deferred.
type LDAPAuthenticator struct {
	cfg  config.LDAPConfig
	db   *gorm.DB
	dial func(url string) (ldapConn, error) // injectable for tests
}

// ldapConn is the minimal LDAP surface used here (lets tests fake it).
type ldapConn interface {
	Bind(username, password string) error
	Search(*ldapv3.SearchRequest) (*ldapv3.SearchResult, error)
	Close() error
}

// NewLDAPAuthenticator builds an LDAP-backed authenticator.
func NewLDAPAuthenticator(cfg config.LDAPConfig, db *gorm.DB) *LDAPAuthenticator {
	return &LDAPAuthenticator{
		cfg: cfg,
		db:  db,
		dial: func(url string) (ldapConn, error) {
			c, err := ldapv3.DialURL(url)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	}
}

// Verify binds the service account, finds the user, binds as the user to check
// the password, then provisions/returns the local user record.
func (a *LDAPAuthenticator) Verify(ctx context.Context, account, password string) (*model.User, error) {
	if !a.cfg.Enabled() {
		return nil, ErrInvalidCredentials
	}
	conn, err := a.dial(a.cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("ldap dial: %w", err)
	}
	defer conn.Close()

	// 1. service-account bind to search.
	if a.cfg.BindDN != "" {
		if err := conn.Bind(a.cfg.BindDN, a.cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("ldap service bind: %w", err)
		}
	}

	// 2. find the user DN + attributes.
	search := ldapv3.NewSearchRequest(
		a.cfg.BaseDN, ldapv3.ScopeWholeSubtree, ldapv3.NeverDerefAliases, 1, 0, false,
		fmt.Sprintf(a.cfg.UserFilter, ldapv3.EscapeFilter(account)),
		[]string{"dn", "cn", "mail"}, nil,
	)
	res, err := conn.Search(search)
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}
	if len(res.Entries) != 1 {
		return nil, ErrInvalidCredentials // not found or ambiguous
	}
	entry := res.Entries[0]

	// 3. bind as the user to verify the password.
	if err := conn.Bind(entry.DN, password); err != nil {
		return nil, ErrInvalidCredentials
	}

	// 4. provision/return the local user.
	return a.provision(ctx, account, entry.GetAttributeValue("cn"), entry.GetAttributeValue("mail"))
}

// provision ensures a local user row exists for the LDAP account and returns it.
func (a *LDAPAuthenticator) provision(ctx context.Context, account, name, email string) (*model.User, error) {
	var u model.User
	err := a.db.WithContext(ctx).First(&u, "account = ?", account).Error
	if err == nil {
		return &u, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if name == "" {
		name = account
	}
	u = model.User{
		ID:          newID(),
		Account:     account,
		Name:        name,
		Email:       email,
		Enabled:     true,
		Source:      model.SourceLDAP,
		AccountType: model.AccountTypeOther,
	}
	if err := a.db.WithContext(ctx).Create(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// Chain tries each authenticator in order, returning the first success. The
// first non-credential error (e.g. LDAP unreachable) short-circuits.
type Chain struct {
	auths []Authenticator
}

// NewChain builds an authenticator chain (e.g. local first, then LDAP).
func NewChain(auths ...Authenticator) *Chain { return &Chain{auths: auths} }

// Verify tries each authenticator; credential failures fall through to the next.
func (c *Chain) Verify(ctx context.Context, account, password string) (*model.User, error) {
	var lastErr error = ErrInvalidCredentials
	for _, a := range c.auths {
		u, err := a.Verify(ctx, account, password)
		if err == nil {
			return u, nil
		}
		// Fall through on credential failures; surface infra errors immediately.
		if errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrUserDisabled) {
			lastErr = err
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

// newID returns a random 128-bit hex id for provisioned users.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
