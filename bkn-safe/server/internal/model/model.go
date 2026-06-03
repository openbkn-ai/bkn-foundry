// Package model holds bkn-safe's GORM domain model. This is a CLEAN redesign
// (not the ISF schema): users/credentials/departments/groups/roles/memberships
// plus the resource-type + operation catalog. Casbin policies live in the
// adapter's own table (casbin_rule), not here.
package model

import "time"

// Source distinguishes locally-managed identities from federated (LDAP) ones.
type Source string

const (
	SourceLocal Source = "local"
	SourceLDAP  Source = "ldap"
)

// AccountType mirrors the introspect ext.account_type claim values.
type AccountType string

const (
	AccountTypeOther  AccountType = "other"
	AccountTypeIDCard AccountType = "id_card"
)

// User is an identity in the directory. Password lives here for local users;
// LDAP users authenticate against the external directory (PasswordHash empty).
type User struct {
	ID           string      `gorm:"primaryKey;size:64"`
	Account      string      `gorm:"uniqueIndex;size:128"` // login name
	Name         string      `gorm:"size:255"`
	Email        string      `gorm:"size:255;index"`
	Telephone    string      `gorm:"size:64"`
	Enabled      bool        `gorm:"default:true"`
	Source       Source      `gorm:"size:16;default:local"`
	AccountType  AccountType `gorm:"size:16;default:other"`
	PasswordHash string      `gorm:"size:255"` // bcrypt; empty for ldap/app
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Role — preserves the ISF role UUIDs (seeded from role.json). Source is
// system|business.
type Role struct {
	ID          string `gorm:"primaryKey;size:64"`
	Name        string `gorm:"size:128"`
	Description string `gorm:"size:1024"`
	Source      string `gorm:"size:16"` // system | business
	CreatedAt   time.Time
}

// Department is a node in the org tree. ParentID empty = root.
type Department struct {
	ID        string `gorm:"primaryKey;size:64"`
	Name      string `gorm:"size:255"`
	ParentID  string `gorm:"size:64;index"`
	Type      string `gorm:"size:32;default:department"`
	CreatedAt time.Time
}

// UserDepartment maps a user into a department (many-to-many).
type UserDepartment struct {
	UserID       string `gorm:"primaryKey;size:64"`
	DepartmentID string `gorm:"primaryKey;size:64"`
}

// Group is an internal group of members.
type Group struct {
	ID        string `gorm:"primaryKey;size:64"`
	Name      string `gorm:"size:255"`
	Notes     string `gorm:"size:1024"`
	CreatedAt time.Time
}

// GroupMember maps a member (user) into a group.
type GroupMember struct {
	GroupID    string `gorm:"primaryKey;size:64"`
	MemberID   string `gorm:"primaryKey;size:64"`
	MemberType string `gorm:"size:16;default:user"`
}

// ResourceType is a registered resource kind (e.g. "agent", "pipeline").
// Seeded centrally (not self-registered by modules).
type ResourceType struct {
	ID          string `gorm:"primaryKey;size:64"` // e.g. "agent"
	Name        string `gorm:"size:128"`
	Description string `gorm:"size:1024"`
	Hidden      bool
}

// Operation is an action defined on a resource type (e.g. agent/use).
type Operation struct {
	ResourceTypeID string `gorm:"primaryKey;size:64"`
	ID             string `gorm:"primaryKey;size:64"` // e.g. "use"
	Name           string `gorm:"size:128"`
	Description    string `gorm:"size:1024"`
}

// AllModels is the migration set (Casbin's table is managed by its adapter).
func AllModels() []any {
	return []any{
		&User{}, &Role{}, &Department{}, &UserDepartment{},
		&Group{}, &GroupMember{}, &ResourceType{}, &Operation{},
	}
}
