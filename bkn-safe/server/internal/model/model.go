// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

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
	// AccountTypeApp / AccountTypeContactor: ISF application accounts and contacts are stored
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

// ManagedProxyAccount marks an app identity whose lifecycle is owned by one
// platform resource rather than by a human administrator. The identity itself
// remains a User row so the existing Casbin subject and account-status checks
// keep working; this companion row is the protection boundary that keeps the
// account out of normal login, credential, directory-membership and generic
// grant-management paths.
//
// The three managed-resource columns are unique together: retrying KN creation
// resolves the same proxy instead of creating another enabled app identity.
type ManagedProxyAccount struct {
	ProxyAccountID      string `gorm:"primaryKey;size:64"`
	ManagedBy           string `gorm:"size:32;uniqueIndex:uidx_managed_proxy_resource,priority:1"`
	ManagedResourceType string `gorm:"size:64;uniqueIndex:uidx_managed_proxy_resource,priority:2"`
	ManagedResourceID   string `gorm:"size:128;uniqueIndex:uidx_managed_proxy_resource,priority:3"`
	LifecycleStatus     string `gorm:"size:16;index"`
	Version             uint64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ProxyGrantSource is the durable provenance ledger for one direct permission
// required by a managed BKN proxy. A binding may require several permissions,
// and several bindings may require the same permission, so the source identity
// and permission tuple form the idempotency key. Revoked rows are retained for
// audit and may be reactivated by a later full synchronization.
type ProxyGrantSource struct {
	ID              string     `json:"id" gorm:"primaryKey;size:64"`
	ProxyAccountID  string     `json:"proxy_account_id" gorm:"size:64;uniqueIndex:uidx_proxy_grant_source,priority:1;index:idx_proxy_grant_tuple,priority:1"`
	ResourceType    string     `json:"resource_type" gorm:"size:64;uniqueIndex:uidx_proxy_grant_source,priority:2;index:idx_proxy_grant_tuple,priority:2"`
	ResourceID      string     `json:"resource_id" gorm:"size:128;uniqueIndex:uidx_proxy_grant_source,priority:3;index:idx_proxy_grant_tuple,priority:3"`
	Operation       string     `json:"operation" gorm:"size:64;uniqueIndex:uidx_proxy_grant_source,priority:4;index:idx_proxy_grant_tuple,priority:4"`
	SourceType      string     `json:"source_type" gorm:"size:32;uniqueIndex:uidx_proxy_grant_source,priority:5;index"`
	SourceID        string     `json:"source_id" gorm:"size:128;uniqueIndex:uidx_proxy_grant_source,priority:6;index"`
	KNID            string     `json:"kn_id" gorm:"size:128;index"`
	BindingType     string     `json:"binding_type" gorm:"size:64"`
	BindingID       string     `json:"binding_id" gorm:"size:128"`
	GrantedBy       string     `json:"granted_by" gorm:"size:64;index"`
	LifecycleStatus string     `json:"lifecycle_status" gorm:"size:16;index"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty" gorm:"index"`
}

// TableName keeps the schema name frozen to the singular name used by the
// cross-service design contract.
func (ProxyGrantSource) TableName() string { return "proxy_grant_source" }

// ProxyGrantPolicy records whether the source service owns the concrete
// Casbin row for a permission tuple. If the row already existed when the first
// source arrived, PolicyOwned is false and removing the last source preserves
// that manual/legacy Allow.
type ProxyGrantPolicy struct {
	ProxyAccountID string `gorm:"primaryKey;size:64"`
	ResourceType   string `gorm:"primaryKey;size:64"`
	ResourceID     string `gorm:"primaryKey;size:128"`
	Operation      string `gorm:"primaryKey;size:64"`
	PolicyOwned    bool   `gorm:"not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (ProxyGrantPolicy) TableName() string { return "proxy_grant_policy" }

// ProxyGrantAuditLog records both successful and denied proxy-grant decisions.
// It is deliberately separate from the browser/admin audit log: this internal
// surface receives a trusted grantor identity in the request rather than an
// OAuth token resolved by HTTP middleware.
type ProxyGrantAuditLog struct {
	ID             string    `json:"id" gorm:"primaryKey;size:64"`
	Action         string    `json:"action" gorm:"size:32;index"`
	Decision       string    `json:"decision" gorm:"size:16;index"`
	Reason         string    `json:"reason" gorm:"size:255"`
	GrantorID      string    `json:"grantor_id" gorm:"size:64;index"`
	ProxyAccountID string    `json:"proxy_account_id" gorm:"size:64;index"`
	ResourceType   string    `json:"resource_type" gorm:"size:64"`
	ResourceID     string    `json:"resource_id" gorm:"size:128"`
	Operation      string    `json:"operation" gorm:"size:64"`
	SourceType     string    `json:"source_type" gorm:"size:32"`
	SourceID       string    `json:"source_id" gorm:"size:128"`
	CreatedAt      time.Time `json:"created_at" gorm:"index"`
}

func (ProxyGrantAuditLog) TableName() string { return "proxy_grant_audit_log" }

// Role source values. system|business roles are SEEDED built-ins (their UUIDs
// are hardcoded in DA/flow-automation, such as application, data, and AI administrators) and are
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
	ManagerID string `gorm:"size:64;index"` // optional responsible user
	Code      string `gorm:"size:64;index"` // optional unique business code (enforced in service)
	Email     string `gorm:"size:255"`
	Remark    string `gorm:"size:1024"`
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
	// ParentTypeID declares that instances of this type sit UNDER an instance of
	// another type ("resource" under "catalog"). It is the type-level half of the
	// hierarchy; the instance-level half is ResourceParent. Empty = no parent,
	// which is every type except the explicit hierarchies seeded in catalog.json.
	ParentTypeID string `gorm:"size:64;index"`
}

// Operation is an action defined on a resource type (e.g. agent/use).
type Operation struct {
	ResourceTypeID string `gorm:"primaryKey;size:64"`
	ID             string `gorm:"primaryKey;size:64"` // e.g. "use"
	Name           string `gorm:"size:128"`
	Description    string `gorm:"size:1024"`
	// ParentOperationID is the operation to look for ON THE PARENT when this one
	// is not granted on the instance itself. It is an explicit MAPPING, never the
	// same name by convention: "modify" on a data table means "edit that table",
	// while "modify" on its catalog means "rename the catalog" — inheriting by
	// name would turn the right to rename a catalog into the right to rewrite
	// every table in it. Empty = the operation does not inherit at all (#800).
	ParentOperationID string `gorm:"size:64"`
	// ImpliedOperationIDs are operations on the SAME type that come with this one
	// and cannot sensibly be held without it: resource_manage on a catalog also
	// grants view_detail, because managing the tables inside a catalog is
	// unreachable without the right to open it — every management route loads the
	// target first, and that load is a view_detail judgement (#1121).
	//
	// Comma-separated operation ids, resolved when the grant is written rather
	// than when it is enforced, so the policy table stores what the accessor
	// actually holds and a reader need not replay a rule to know it.
	ImpliedOperationIDs string `gorm:"size:512"`
}

// ResourceParent records that ONE concrete resource instance sits under one
// concrete parent instance — the fact bkn-safe has never had, and the reason a
// grant on a catalog could not previously reach the tables inside it: policies
// are keyed by "type:id" and nothing said which catalog a given table belongs to.
//
// bkn-safe does not discover this itself; the owning module (vega for
// catalog/resource) pushes it through PUT /authz/resource-parents. A missing row
// is not an error — it degrades to the pre-#800 judgement, where only grants on
// the instance itself count.
type ResourceParent struct {
	ResourceTypeID string `gorm:"primaryKey;size:64"`
	ResourceID     string `gorm:"primaryKey;size:128"`
	ParentTypeID   string `gorm:"size:64;index:idx_resource_parent_parent,priority:1"`
	ParentID       string `gorm:"size:128;index:idx_resource_parent_parent,priority:2"`
	UpdatedAt      time.Time
}

// AuditLog records a user or admin management mutation: who (ActorID, the verified
// token subject), what (Method + Resource + Action + TargetID + Detail), and the
// outcome (Status). One row is written for each mutating request on an audited
// /admin or /me surface; ordinary reads are not audited. Action carries a stable
// business verb while Method retains the transport fact.
type AuditLog struct {
	ID                string `json:"id" gorm:"primaryKey;size:64"`
	ActorID           string `json:"actor_id" gorm:"size:64;index"` // token subject that performed the action
	ActorNameSnapshot string `json:"actor_name_snapshot" gorm:"size:255"`
	ActorType         string `json:"actor_type" gorm:"size:32"`
	AuthMethod        string `json:"auth_method" gorm:"size:32"`
	CredentialID      string `json:"credential_id" gorm:"size:128"`
	RequestID         string `json:"request_id" gorm:"size:128;index"`
	SourceChannel     string `json:"source_channel" gorm:"size:32"`
	Method            string `json:"method" gorm:"size:8"`            // POST | PUT | DELETE
	Resource          string `json:"resource" gorm:"size:64;index"`   // top-level admin noun, e.g. "users"
	Action            string `json:"action" gorm:"size:128;index"`    // dotted route, e.g. "departments.members"
	TargetID          string `json:"target_id" gorm:"size:128;index"` // :id path param, "" when the route has none
	TargetName        string `json:"target_name" gorm:"size:255"`     // display-name snapshot for deleted/renamed targets
	// Detail is a redacted, truncated JSON snapshot of the request body (password
	// fields masked), so a reader can tell WHAT changed — which users a
	// department gained, a created node's name, etc. "" when the body is
	// empty/non-JSON.
	Detail    string    `json:"detail" gorm:"size:2048"`
	Status    int       `json:"status"` // HTTP status code of the response
	ClientIP  string    `json:"client_ip" gorm:"size:64"`
	CreatedAt time.Time `json:"created_at" gorm:"index"`
}

// AccessLog records an authentication fact. It is deliberately separate from
// AuditLog: login and logout explain who entered or left the platform, while
// AuditLog records administration mutations made after authentication.
// Passwords, tokens, cookies and request bodies are never stored here.
type AccessLog struct {
	ID                string    `json:"id" gorm:"primaryKey;size:64"`
	ActorID           string    `json:"actor_id" gorm:"size:64;index"`
	ActorNameSnapshot string    `json:"actor_name_snapshot" gorm:"size:255"`
	AuthMethod        string    `json:"auth_method" gorm:"size:32"`
	SourceChannel     string    `json:"source_channel" gorm:"size:32"`
	Action            string    `json:"action" gorm:"size:32;index"`  // login | logout
	Outcome           string    `json:"outcome" gorm:"size:32;index"` // success | failure
	FailureCode       string    `json:"failure_code" gorm:"size:64"`
	RequestID         string    `json:"request_id" gorm:"size:128;index"`
	ClientIP          string    `json:"client_ip" gorm:"size:64"`
	CreatedAt         time.Time `json:"created_at" gorm:"index"`
}

// APIKey is a user-issued long-lived credential (AppKey). It authenticates AS its
// owner: verification resolves the owner's id + account_type, so downstream authz
// is identical to the owner using an OAuth token (no second permission system).
//
// The plaintext key has the shape "bak_<KeyID>_<secret>" and is shown ONCE at
// issue time; only SecretHash (sha256 hex of the secret half) is stored. KeyID is
// the public, indexed lookup half. Revoke deletes the row; Enabled is a defensive
// soft-disable flag also checked on every verify. ExpiresAt nil = never expires.
type APIKey struct {
	ID          string `gorm:"primaryKey;size:64"`  // internal row id
	KeyID       string `gorm:"uniqueIndex;size:64"` // public lookup half, embedded in the key
	OwnerUserID string `gorm:"size:64;index"`       // User.ID this key acts as
	Name        string `gorm:"size:128"`            // user-facing label
	Masked      string `gorm:"size:64"`             // one-time display hint, e.g. "bak_2882****SWua"; safe to list
	SecretHash  string `gorm:"size:128"`            // sha256 hex of the secret half
	// ExpiresAt nil = never expires (explicit opt-in by the issuer). LastUsedAt is
	// updated on each successful verify so stale/leaked keys can be spotted/reaped.
	ExpiresAt  *time.Time `gorm:"index"`
	LastUsedAt *time.Time
	Enabled    bool
	CreatedAt  time.Time
}

// License is the cluster's single license record (bkn-safe is the cluster-wide
// license holder — see docs/foundry/bkn-safe/design/issue-224-license-hub.md in
// bkn-docs). One row with a fixed ID; the activation state lives inside Text
// (the signed .lic embeds hw_fingerprint after activation), so surviving a
// restart needs nothing beyond this row.
type License struct {
	ID string `gorm:"primaryKey;size:16"` // fixed "current"
	// Text is the raw signed .lic. Its signature — not this row — is what
	// modules and bkn-safe itself trust; the DB is only a mailbox.
	Text string `gorm:"type:text"`
	// HighWater is the largest unix timestamp the background re-verify loop has
	// seen, persisted to detect large clock rollbacks on offline deployments.
	HighWater int64
	// Version is an optimistic lock: concurrent renewals (multi-replica) must
	// not overwrite each other's freshly reissued license with a stale one.
	Version   int64
	UpdatedAt time.Time
	CreatedAt time.Time
}

// AllModels is the migration set (Casbin's table is managed by its adapter).
func AllModels() []any {
	return []any{
		&User{}, &Role{}, &Department{}, &UserDepartment{},
		&Group{}, &GroupMember{}, &ResourceType{}, &Operation{},
		&AuditLog{}, &AccessLog{}, &APIKey{}, &License{}, &ResourceParent{},
		&ManagedProxyAccount{}, &ProxyGrantSource{}, &ProxyGrantPolicy{},
		&ProxyGrantAuditLog{},
	}
}
