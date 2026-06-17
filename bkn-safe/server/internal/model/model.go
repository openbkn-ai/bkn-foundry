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
	// AccountTypeApp / AccountTypeContactor: ISF "应用账户" and "联系人" are stored
	// as User rows distinguished by account_type (no separate tables). Directory
	// name resolution looks them up in the users table by id like any other user.
	AccountTypeApp       AccountType = "app"
	AccountTypeContactor AccountType = "contactor"
)

// User is an identity in the directory. Password lives here for local users;
// LDAP users authenticate against the external directory (PasswordHash empty).
type User struct {
	ID        string `gorm:"primaryKey;size:64"`
	Account   string `gorm:"uniqueIndex;size:128"` // login name
	Name      string `gorm:"size:255"`
	Email     string `gorm:"size:255;index"`
	Telephone string `gorm:"size:64"`
	// No GORM "default:true": a default would override an explicit Enabled=false
	// on insert (GORM treats the bool zero value as unset). Callers set Enabled.
	Enabled      bool
	Source       Source      `gorm:"size:16;default:local"`
	AccountType  AccountType `gorm:"size:16;default:other"`
	PasswordHash string      `gorm:"size:255"` // bcrypt; empty for ldap/app
	// MustChangePassword forces a password change before the login is accepted.
	// Set on the seeded built-in admin (initial password); cleared by SetPassword.
	MustChangePassword bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Role source values. system|business roles are SEEDED built-ins (their UUIDs
// are hardcoded in DA/flow-automation, e.g. 應用/數據/AI 管理員) and are
// immutable via the API — they may only be changed by editing the seed files.
// custom roles are created at runtime through the admin API and are freely
// editable/deletable.
const (
	RoleSourceSystem   = "system"
	RoleSourceBusiness = "business"
	RoleSourceCustom   = "custom"
)

// Role — preserves the ISF role UUIDs (seeded from role.json). Source is
// system|business for built-ins, custom for API-created roles.
type Role struct {
	ID          string `gorm:"primaryKey;size:64"`
	Name        string `gorm:"size:128"`
	Description string `gorm:"size:1024"`
	Source      string `gorm:"size:16"` // system | business | custom
	CreatedAt   time.Time
}

// BuiltIn reports whether the role is a seeded system/business role and thus
// immutable through the API (no rename, no permission edit, no delete).
func (r Role) BuiltIn() bool {
	return r.Source == RoleSourceSystem || r.Source == RoleSourceBusiness
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

// AuditLog records a privileged admin-API mutation: who (ActorID, the verified
// token subject), what (Method + Resource + Action + TargetID + Detail), and the
// outcome (Status). One row per non-GET request that passes RequireAdmin; reads
// are not audited. Method distinguishes create/update/delete on the same Action
// (e.g. POST vs PUT vs DELETE on "users").
type AuditLog struct {
	ID       string `json:"id" gorm:"primaryKey;size:64"`
	ActorID  string `json:"actor_id" gorm:"size:64;index"`   // token subject that performed the action
	Method   string `json:"method" gorm:"size:8"`            // POST | PUT | DELETE
	Resource string `json:"resource" gorm:"size:64;index"`   // top-level admin noun, e.g. "users"
	Action   string `json:"action" gorm:"size:128;index"`    // dotted route, e.g. "departments.members"
	TargetID string `json:"target_id" gorm:"size:128;index"` // :id path param, "" when the route has none
	// Detail is a redacted, truncated JSON snapshot of the request body (password
	// fields masked), so a reader can tell WHAT changed — which users a
	// department gained, a created node's name, etc. "" when the body is
	// empty/non-JSON.
	Detail    string    `json:"detail" gorm:"size:2048"`
	Status    int       `json:"status"` // HTTP status code of the response
	ClientIP  string    `json:"client_ip" gorm:"size:64"`
	CreatedAt time.Time `json:"created_at" gorm:"index"`
}

// AllModels is the migration set (Casbin's table is managed by its adapter).
func AllModels() []any {
	return []any{
		&User{}, &Role{}, &Department{}, &UserDepartment{},
		&Group{}, &GroupMember{}, &ResourceType{}, &Operation{},
		&AuditLog{},
	}
}
