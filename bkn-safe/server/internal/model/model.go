// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package model holds bkn-safe's GORM domain model. This is a CLEAN redesign
// (not the ISF schema): users/credentials/departments/groups/roles/memberships
// plus the resource-type + operation catalog. Casbin's matcher projection lives
// in the adapter-owned casbin_rule table; AuthorizationGrant is the durable
// identity and provenance of each independently managed grant.
package model

import (
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/migrationcontract"
)

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
	// GrantSyncGeneration fences full grant-set replacement requests from BKN.
	// A lower generation must never restore sources removed by a newer publish.
	GrantSyncGeneration  uint64 `gorm:"not null;default:0"`
	GrantSnapshotVersion string `gorm:"size:80;not null;default:''"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

const (
	ProxyGrantSourceTypeKNBinding = "kn_proxy_binding"
	ProxyGrantSourceTypeManual    = "manual"
	ProxyGrantSourceTypeAdmin     = "admin"
	ProxyGrantSourceStatusActive  = "active"
	ProxyGrantSourceStatusRevoked = "revoked"
)

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

	// RequirementDerived distinguishes a prerequisite synthesized by operation
	// normalization from an operation explicitly requested by the caller. The
	// distinction is durable so revoking an operation never cascades into an
	// independently requested permission that happens to share its source tuple.
	RequirementDerived bool `json:"requirement_derived" gorm:"not null;default:false"`
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

// AuthorizationGrant is the authoritative Core grant record. Several rows may
// intentionally carry the same authorization tuple: grant_id identifies the
// independently managed source, while Casbin needs only one projection of that
// tuple for runtime matching. A revoke deletes the shared projection only after
// the final matching grant row disappears. ProjectionKey is a fixed-width hash
// used only to narrow exact-tuple lookups; the query still verifies every tuple
// field, so correctness never depends on hash uniqueness.
type AuthorizationGrant struct {
	GrantID         string `json:"grant_id" gorm:"primaryKey;size:64"`
	ProjectionKey   string `json:"projection_key" gorm:"size:64;index"`
	AccessorID      string `json:"accessor_id" gorm:"size:64;index:idx_authorization_grant_scope,priority:1"`
	Object          string `json:"object" gorm:"size:255"`
	Operation       string `json:"operation" gorm:"size:64"`
	Effect          string `json:"effect" gorm:"size:16;index:idx_authorization_grant_scope,priority:2"`
	PolicySource    string `json:"policy_source" gorm:"size:32;index:idx_authorization_grant_scope,priority:3"`
	AuthoritySource string `json:"authority_source" gorm:"size:32;index:idx_authorization_grant_scope,priority:4"`
	CreatedBy       string `json:"created_by" gorm:"size:64;index"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (AuthorizationGrant) TableName() string { return "authorization_grant" }

// PermissionRequest is a durable request for one least-privilege grant. The
// recipient is always the requesting user. Knowledge-network proxy grants are
// derived by bkn-backend after a binding succeeds, not approved here.
type PermissionRequest struct {
	ID          string `json:"id" gorm:"primaryKey;size:36"`
	RequestKey  string `json:"request_key" gorm:"size:128;uniqueIndex"`
	RequesterID string `json:"requester_id" gorm:"size:64;index"`
	// RequesterName is hydrated from the user directory for API presentation.
	// It is deliberately not persisted in permission_request so a rename is
	// reflected in inboxes without rewriting historical requests.
	RequesterName string `json:"requester_name" gorm:"-"`
	// ReviewerID and ReviewerName project the latest recorded decision for
	// applicant-facing request lists. They are not request table columns.
	ReviewerID   string `json:"reviewer_id" gorm:"-"`
	ReviewerName string `json:"reviewer_name" gorm:"-"`
	// ReviewedAt is projected from permission_request_decision for a reviewer's
	// history list. It is read-only and never becomes a request table column.
	ReviewedAt   *time.Time `json:"reviewed_at,omitempty" gorm:"-"`
	ResourceType string     `json:"resource_type" gorm:"size:64;index"`
	ResourceID   string     `json:"resource_id" gorm:"size:128;index"`
	// ResourceName is an immutable display snapshot supplied when the request
	// is created. Authorization and resource liveness always use ResourceType
	// and ResourceID; retaining this name keeps historical requests readable
	// after a resource is renamed or deleted.
	ResourceName string `json:"resource_name" gorm:"size:256"`
	// Operation is retained as the first requested operation for legacy clients.
	// New callers must use Operations, which is persisted in
	// permission_request_operation.
	Operation  string   `json:"operation" gorm:"size:64"`
	Operations []string `json:"operations" gorm:"-"`
	Reason     string   `json:"reason" gorm:"size:512"`
	Status     string   `json:"status" gorm:"size:32;index"`
	GrantID    string   `gorm:"size:64;uniqueIndex"`
	ApprovedBy string   `gorm:"size:64;index"`
	ApprovedAt *time.Time
	RejectedAt *time.Time
	CreatedAt  time.Time `json:"created_at" gorm:"index"`
	UpdatedAt  time.Time
}

func (PermissionRequest) TableName() string { return "permission_request" }

// PermissionRequestOperation is one requested operation in a multi-operation
// permission request. The pair is unique, so retries cannot duplicate an
// operation inside one request.
type PermissionRequestOperation struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36"`
	RequestID string    `json:"request_id" gorm:"size:64;uniqueIndex:uidx_permission_request_operation,priority:1;index"`
	Operation string    `json:"operation" gorm:"size:64;uniqueIndex:uidx_permission_request_operation,priority:2"`
	CreatedAt time.Time `json:"created_at"`
}

func (PermissionRequestOperation) TableName() string { return "permission_request_operation" }

// PermissionRequestDecision retains every review action. A reviewer may make
// at most one decision for one request.
type PermissionRequestDecision struct {
	ID           string    `json:"id" gorm:"primaryKey;size:36"`
	RequestID    string    `json:"request_id" gorm:"size:64;uniqueIndex:uidx_permission_request_reviewer,priority:1;index"`
	ReviewerID   string    `json:"reviewer_id" gorm:"size:64;uniqueIndex:uidx_permission_request_reviewer,priority:2;index"`
	ReviewerName string    `json:"reviewer_name" gorm:"-"`
	Decision     string    `json:"decision" gorm:"size:16"`
	Comment      string    `json:"comment" gorm:"size:512"`
	CreatedAt    time.Time `json:"created_at" gorm:"index"`
}

func (PermissionRequestDecision) TableName() string { return "permission_request_decision" }

// PermissionRequestReviewer materializes a user's current eligibility to
// review one permission request. Rows are retained and revoked rather than
// deleted so that a change in authorization remains auditable.
type PermissionRequestReviewer struct {
	ID                    string    `json:"id" gorm:"primaryKey;size:36"`
	RequestID             string    `json:"request_id" gorm:"size:64;uniqueIndex:uidx_permission_request_candidate,priority:1;index"`
	ReviewerID            string    `json:"reviewer_id" gorm:"size:64;uniqueIndex:uidx_permission_request_candidate,priority:2;index"`
	EligibilityStatus     string    `json:"eligibility_status" gorm:"size:16;index"`
	AuthorizationRootType string    `json:"authorization_root_type" gorm:"size:64"`
	AuthorizationRootID   string    `json:"authorization_root_id" gorm:"size:128"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

func (PermissionRequestReviewer) TableName() string { return "permission_request_reviewer" }

// AuthorizationMigrationMarker aliases the public runtime receipt contract.
// The offline writer lives under deploy; bkn-safe owns only storage and startup
// validation of that receipt.
type AuthorizationMigrationMarker = migrationcontract.Marker

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
	ID   string `gorm:"primaryKey;size:64"`
	Name string `gorm:"size:128"`
	// NameKey is the canonical form of Name (trimmed and case-folded). It is
	// nullable so adding it to a deployment with legacy duplicate role names
	// does not make the schema migration fail; all newly written roles set it.
	NameKey     *string `gorm:"size:128;uniqueIndex:idx_roles_name_key"`
	Description string  `gorm:"size:1024"`
	Source      string  `gorm:"size:16"` // system | business | custom
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
	// which is every type except the explicit hierarchies seeded in authorization-registry.json.
	ParentTypeID string `gorm:"size:64;index"`
}

// Operation is an action defined on a resource type (e.g. agent/use).
type Operation struct {
	ResourceTypeID string `gorm:"primaryKey;size:64"`
	ID             string `gorm:"primaryKey;size:64"` // e.g. "use"
	Name           string `gorm:"size:128"`
	Description    string `gorm:"size:1024"`
	// Grantable controls whether an operation may be persisted as an allow or
	// deny policy. The database default keeps every operation from an older
	// registry grantable. A pointer lets seed persist an explicit false instead
	// of GORM replacing the bool zero value with the database default.
	Grantable *bool `gorm:"not null;default:true"`
	// ParentOperationID is the operation to look for ON THE PARENT when this one
	// is not granted on the instance itself. It is an explicit MAPPING, never the
	// same name by convention: "modify" on a data table means "edit that table",
	// while "modify" on its catalog means "rename the catalog" — inheriting by
	// name would turn the right to rename a catalog into the right to rewrite
	// every table in it. Empty = the operation does not inherit at all (#800).
	ParentOperationID string `gorm:"size:64"`
	// DerivedToOperationID declares an operation produced on this type's parent
	// when this concrete child operation has an independent direct allow. It is
	// metadata for read-time authorization calculation, never a persisted grant.
	DerivedToOperationID string `gorm:"size:64"`
	// RequiredOperationIDs are direct prerequisites on the SAME resource type.
	// They are enforced when an allow set is written, so the persisted set contains
	// every prerequisite. They do not alter read-time authorization decisions.
	// The first version deliberately permits one layer only.
	//
	// The legacy database column name is retained to keep upgrades schema-only:
	// the former "implies" rule represented the same stored edge, but enforced it
	// only while writing. Seed rewrites the authoritative values on every start.
	RequiredOperationIDs string `gorm:"column:implied_operation_ids;size:512"`
}

// IsGrantable preserves the registry's backward-compatible default when an
// Operation is constructed in memory without the optional field.
func (o Operation) IsGrantable() bool {
	return o.Grantable == nil || *o.Grantable
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

type AuditChainState string

const (
	// AuditChainStatePending means the event committed with its business mutation
	// and is waiting for the asynchronous tamper-evidence chain append.
	AuditChainStatePending AuditChainState = "pending"
	// AuditChainStateChained means Seq, PrevHash, and RowHash were assigned.
	AuditChainStateChained AuditChainState = "chained"
)

// AuditLog records a user or admin management mutation: who (ActorID, the verified
// token subject), what (Method + Resource + Action + TargetID + Detail), and the
// outcome (Status). One row is normally written for each mutating request on an
// audited /admin or /me surface; a batch mutation may write one row per target,
// correlated by RequestID. Ordinary reads are not audited. Action carries a
// stable business verb while Method retains the transport fact.
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
	// Tamper-evidence chain (#334). Seq is the row's append position, PrevHash
	// the RowHash of the row at Seq-1, and RowHash SHA-256 over this row's
	// canonical form plus PrevHash — so editing, deleting or reordering any
	// chained row breaks verification from that point on. Rows written before
	// the chain existed keep a NULL Seq and empty hashes: they are counted but
	// never verified, and the chain starts at the first row written after the
	// upgrade. Seq is nullable precisely so that AutoMigrate can add the unique
	// index on a table that already holds rows.
	Seq      *uint64 `json:"seq,omitempty" gorm:"uniqueIndex"`
	PrevHash string  `json:"prev_hash,omitempty" gorm:"size:64"`
	RowHash  string  `json:"row_hash,omitempty" gorm:"size:64"`
	// ChainState is empty for pre-chain legacy rows, pending when the business
	// transaction committed the audit fact but its chain append is outstanding,
	// and chained after a successful append.
	ChainState AuditChainState `json:"chain_state,omitempty" gorm:"size:16;index"`
}

// AuthzDecision is one recorded authorization decision (#334): which accessor
// asked for which operation on which resource, what the engine answered and on
// what basis. It is a query log, not the chained audit trail: rows are written
// asynchronously, allow decisions may be sampled, and the table is purged by
// retention. Deny decisions are always recorded.
type AuthzDecision struct {
	ID           string `json:"id" gorm:"primaryKey;size:64"`
	AccessorID   string `json:"accessor_id" gorm:"size:64;index:idx_authz_decision_accessor_time,priority:1"`
	ResourceType string `json:"resource_type" gorm:"size:64;index"`
	ResourceID   string `json:"resource_id" gorm:"size:128"`
	Operation    string `json:"operation" gorm:"size:128"`
	// Scope is the evaluation scope the caller asked for (effective | local).
	Scope string `json:"scope" gorm:"size:16"`
	// Decision is allow | deny | none; Basis is the authz.DecisionBasis that
	// produced it, or "inactive_account" when the accessor was disabled.
	Decision          string `json:"decision" gorm:"size:16;index"`
	Basis             string `json:"basis" gorm:"size:32"`
	DeniedRequirement string `json:"denied_requirement" gorm:"size:64"`
	// Source names the entry point: check | operations | resource-filter |
	// admin (management gates and permission points).
	Source    string `json:"source" gorm:"size:32;index"`
	RequestID string `json:"request_id" gorm:"size:128"`
	TraceID   string `json:"trace_id" gorm:"size:64"`
	ClientIP  string `json:"client_ip" gorm:"size:64"`
	// Detail is a small JSON object with source-specific facts (batch sizes,
	// projected operations, gate name). "" when there is nothing to add.
	Detail    string    `json:"detail" gorm:"size:1024"`
	CreatedAt time.Time `json:"created_at" gorm:"index:idx_authz_decision_accessor_time,priority:2;index"`
}

func (AuthzDecision) TableName() string { return "authz_decision_log" }

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

// OAuthAccessOrigin is an administrator-managed browser origin from which BKN
// Studio may start an OAuth authorization-code flow. Deployment-owned origins
// are injected through config and are deliberately not duplicated here.
type OAuthAccessOrigin struct {
	ID            string `gorm:"primaryKey;size:64"`
	Origin        string `gorm:"uniqueIndex;size:512"`
	DesiredState  string `gorm:"size:16;index"`
	SyncState     string `gorm:"size:16;index"`
	LastSyncError string `gorm:"size:1024"`
	CreatedBy     string `gorm:"size:64;index"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// OAuthClientSyncState records one-time legacy import and the last Hydra
// reconciliation result for a managed OAuth client.
type OAuthClientSyncState struct {
	ClientID         string `gorm:"primaryKey;size:128"`
	LegacyImported   bool
	DesiredHash      string `gorm:"size:64"`
	SyncState        string `gorm:"size:16;index"`
	LastSyncError    string `gorm:"size:1024"`
	LastReconciledAt *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// AllModels is the migration set (Casbin's table is managed by its adapter).
func AllModels() []any {
	return []any{
		&User{}, &Role{}, &Department{}, &UserDepartment{},
		&Group{}, &GroupMember{}, &ResourceType{}, &Operation{},
		&AuditLog{}, &AccessLog{}, &AuthzDecision{}, &APIKey{}, &License{}, &ResourceParent{},
		&ManagedProxyAccount{}, &ProxyGrantSource{}, &ProxyGrantPolicy{},
		&ProxyGrantAuditLog{}, &AuthorizationGrant{},
		&PermissionRequest{}, &PermissionRequestOperation{}, &PermissionRequestDecision{}, &PermissionRequestReviewer{},
		&OAuthAccessOrigin{}, &OAuthClientSyncState{},
	}
}
