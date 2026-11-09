// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package model

import "time"

// SSOProvider is an externally managed OAuth/OIDC provider. ClientSecret supports
// DB-only configuration; ClientSecretEnv remains available for Secret-backed installs.
// Both credential fields are excluded from JSON responses.
type SSOProvider struct {
	ID                    string `gorm:"primaryKey;size:64"`
	DisplayName           string `gorm:"size:128"`
	Protocol              string `gorm:"size:16"`
	Adapter               string `gorm:"size:32"`
	Issuer                string `gorm:"size:512"`
	DiscoveryURL          string `gorm:"size:1024"`
	ClientID              string `gorm:"size:255"`
	ClientSecret          string `gorm:"type:text" json:"-"`
	ClientSecretEnv       string `gorm:"size:128" json:"-"`
	RedirectURI           string `gorm:"size:1024"`
	Scopes                string `gorm:"size:512"`
	DepartmentClaim       string `gorm:"size:128"`
	AllowedClientIDs      string `gorm:"size:1024"`
	ExchangeAudience      string `gorm:"size:255"`
	ExternalRequiredScope string `gorm:"size:255"`
	HydraIssueScope       string `gorm:"size:255"`
	// ExchangeScope is the legacy fallback for either scope setting when unset.
	ExchangeScope     string `gorm:"size:255"`
	LoginEnabled      bool
	ExchangeEnabled   bool
	Enabled           bool
	DefaultForLogin   bool
	AllowInsecureHTTP bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// SSOIdentity maps the issuer's immutable subject to a local user.
type SSOIdentity struct {
	ID         string `gorm:"primaryKey;size:64"`
	ProviderID string `gorm:"size:64;uniqueIndex:ux_sso_identity,priority:1"`
	Issuer     string `gorm:"size:512;uniqueIndex:ux_sso_identity,priority:2"`
	Subject    string `gorm:"size:512;uniqueIndex:ux_sso_identity,priority:3"`
	UserID     string `gorm:"size:64;index"`
	CreatedAt  time.Time
}

// SSODepartment maps one external department key to a local department.
type SSODepartment struct {
	ID         string `gorm:"primaryKey;size:64"`
	ProviderID string `gorm:"size:64;uniqueIndex:ux_sso_department,priority:1"`
	ExternalID string `gorm:"size:255;uniqueIndex:ux_sso_department,priority:2"`
	DeptID     string `gorm:"size:64;index"`
}

// SSOLoginState holds a one-time authorization-code login transaction.
type SSOLoginState struct {
	StateHash    string    `gorm:"primaryKey;size:64"`
	ProviderID   string    `gorm:"size:64"`
	Challenge    string    `gorm:"size:2048"`
	CodeVerifier string    `gorm:"size:128"`
	Nonce        string    `gorm:"size:128"`
	ExpiresAt    time.Time `gorm:"index"`
	CreatedAt    time.Time
}
