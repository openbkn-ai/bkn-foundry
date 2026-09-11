// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzmigration

import "time"

const (
	effectAllow           = "allow"
	effectDeny            = "deny"
	actFullBusinessAccess = "full_business_access"

	policySourceCommunityBundle  = "community_bundle"
	policySourceProfessionalRule = "professional_rule"
	policySourceLegacy           = "legacy"
	policySourceSystemDerived    = "system_derived"
	policySourceRolePermission   = "role_permission"

	authoritySourceAdminAuthz    = "admin_authz"
	authoritySourceOwnerDelegate = "owner_delegate"
	authoritySourceSystem        = "system"
	authoritySourceMigration     = "migration"

	roleSourceSystem   = "system"
	roleSourceBusiness = "business"
	roleSourceCustom   = "custom"
)

// These frozen, table-local models intentionally include only columns read or
// written by the 0.1.5 one-time migration. Runtime domain models remain owned
// by bkn-safe.
type userRow struct {
	ID      string `gorm:"primaryKey;size:64"`
	Account string `gorm:"size:128"`
	Enabled bool
}

func (userRow) TableName() string { return "users" }

type roleRow struct {
	ID     string `gorm:"primaryKey;size:64"`
	Name   string `gorm:"size:128"`
	Source string `gorm:"size:16"`
}

func (roleRow) TableName() string { return "roles" }

func (r roleRow) builtIn() bool {
	return r.Source == roleSourceSystem || r.Source == roleSourceBusiness
}

type departmentRow struct {
	ID   string `gorm:"primaryKey;size:64"`
	Name string `gorm:"size:255"`
}

func (departmentRow) TableName() string { return "departments" }

type authorizationGrantRow struct {
	GrantID         string `gorm:"primaryKey;size:64"`
	ProjectionKey   string `gorm:"size:64;index"`
	AccessorID      string `gorm:"size:64"`
	Object          string `gorm:"size:255"`
	Operation       string `gorm:"size:64"`
	Effect          string `gorm:"size:16"`
	PolicySource    string `gorm:"size:32"`
	AuthoritySource string `gorm:"size:32"`
	CreatedBy       string `gorm:"size:64"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (authorizationGrantRow) TableName() string { return "authorization_grant" }
