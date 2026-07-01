// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"

	"bkn-safe/internal/model"
)

// ClientType values for the introspect ext.client_type claim (lib enum).
const (
	ClientTypeWeb = "web"
)

// VisitorType values for the introspect ext.visitor_type claim. The lib maps
// "realname" -> user; ISF emits "realname" for human users (verified from live
// ISF introspect), so bkn-safe does the same.
const (
	VisitorTypeRealname  = "realname"
	VisitorTypeAnonymous = "anonymous"
)

// ExtClaims builds the introspect `ext` object hydra surfaces from
// session.access_token. For a human (user) login all five fields must be
// present or the kweaver-go-lib introspect parser panics (unchecked type
// assertions) — see the contract freeze §1.
func ExtClaims(u *model.User, loginIP, clientType string) map[string]any {
	if clientType == "" {
		clientType = ClientTypeWeb
	}
	accountType := string(u.AccountType)
	if accountType == "" {
		accountType = string(model.AccountTypeOther)
	}
	return map[string]any{
		"visitor_type": VisitorTypeRealname,
		"login_ip":     loginIP,
		"udid":         "", // ISF user tokens carry an empty udid (captured)
		"account_type": accountType,
		"client_type":  clientType,
	}
}

// Provider orchestrates the hydra login & consent flow against the local user
// store. It is the heart of bkn-safe's authentication.
type Provider struct {
	auth  Authenticator
	hydra *HydraAdmin
	users UserLookup
}

// UserLookup fetches a user by ID (subject) — needed at consent time to build
// the ext claims for the already-authenticated subject — and updates a user's
// password (the change-password flow).
type UserLookup interface {
	ByID(ctx context.Context, id string) (*model.User, error)
	SetPassword(ctx context.Context, userID, password string) error
}

// NewProvider wires the provider.
func NewProvider(auth Authenticator, hydra *HydraAdmin, users UserLookup) *Provider {
	return &Provider{auth: auth, hydra: hydra, users: users}
}

// Login verifies credentials and accepts the hydra login, returning hydra's
// redirect target. On bad credentials it returns ErrInvalidCredentials. When the
// user must change their password first it returns ErrMustChangePassword without
// accepting the login — the caller drives the change-password flow.
func (p *Provider) Login(ctx context.Context, challenge, account, password string, remember bool) (redirectTo string, err error) {
	u, err := p.auth.Verify(ctx, account, password)
	if err != nil {
		return "", err
	}
	if u.MustChangePassword {
		return "", ErrMustChangePassword
	}
	return p.hydra.AcceptLogin(ctx, challenge, u.ID, remember)
}

// ChangePassword re-verifies the current password, sets the new one (clearing
// MustChangePassword), then accepts the hydra login — completing a forced
// first-login change in one step. There is no server session, so the old
// password is re-entered and re-verified rather than trusted from the prior
// login POST. Returns hydra's redirect target.
func (p *Provider) ChangePassword(ctx context.Context, challenge, account, oldPassword, newPassword string, remember bool) (redirectTo string, err error) {
	u, err := p.auth.Verify(ctx, account, oldPassword)
	if err != nil {
		return "", err
	}
	if err := p.users.SetPassword(ctx, u.ID, newPassword); err != nil {
		return "", err
	}
	return p.hydra.AcceptLogin(ctx, challenge, u.ID, remember)
}

// ConsentInfo returns the consent request (client + requested scope) for
// rendering the consent page.
func (p *Provider) ConsentInfo(ctx context.Context, challenge string) (*ConsentRequest, error) {
	return p.hydra.GetConsent(ctx, challenge)
}

// Consent grants the requested scope and injects the ext claims for the
// already-authenticated subject. Returns hydra's redirect target.
func (p *Provider) Consent(ctx context.Context, challenge, loginIP, clientType string, remember bool) (redirectTo string, err error) {
	cr, err := p.hydra.GetConsent(ctx, challenge)
	if err != nil {
		return "", err
	}
	u, err := p.users.ByID(ctx, cr.Subject)
	if err != nil {
		return "", err
	}
	ext := ExtClaims(u, loginIP, clientType)
	return p.hydra.AcceptConsent(ctx, cr, ext, remember)
}

// RejectConsent denies the consent request, returning hydra's redirect target.
func (p *Provider) RejectConsent(ctx context.Context, challenge string) (string, error) {
	return p.hydra.RejectConsent(ctx, challenge)
}
