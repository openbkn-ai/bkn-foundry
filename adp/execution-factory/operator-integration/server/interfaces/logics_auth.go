package interfaces

import (
	"context"
)

// AccountAuthContext Account authentication context.
type AccountAuthContext struct {
	// AccountID account unique identifier.
	AccountID string `json:"account_id"`
	// AccountType account type.
	AccountType AccessorType `json:"account_type"`
	// Token information.
	TokenInfo *TokenInfo `json:"token_info"`
}

// AuthOperationType operation type.
//
//go:generate mockgen -source=logics_auth.go -destination=../mocks/auth.go -package=mocks
type AuthOperationType string

const (
	AuthOperationTypeCreate       AuthOperationType = "create"        // New.
	AuthOperationTypeModify       AuthOperationType = "modify"        // Edit.
	AuthOperationTypeDelete       AuthOperationType = "delete"        // Delete.
	AuthOperationTypeView         AuthOperationType = "view"          // View.
	AuthOperationTypePublish      AuthOperationType = "publish"       // publish.
	AuthOperationTypeUnpublish    AuthOperationType = "unpublish"     // Removed from shelves.
	AuthOperationTypeAuthorize    AuthOperationType = "authorize"     // Permission management.
	AuthOperationTypePublicAccess AuthOperationType = "public_access" // public access.
	AuthOperationTypeExecute      AuthOperationType = "execute"       // execute.
	AuthOperationTypeManage       AuthOperationType = "manage"        // Management (for super management determination, see AuthResourceTypeSafeAdmin)
)

var (
	// Owner permissions.
	OwnerPolicyList = []AuthOperationType{
		AuthOperationTypeCreate,
		AuthOperationTypeModify,
		AuthOperationTypeDelete,
		AuthOperationTypeView,
		AuthOperationTypePublish,
		AuthOperationTypeUnpublish,
		AuthOperationTypeAuthorize,
		AuthOperationTypePublicAccess,
		AuthOperationTypeExecute,
	}
)

// AuthResourceType resource type.
type AuthResourceType string

// Supported resource types.
const (
	AuthResourceTypeToolBox  AuthResourceType = "tool_box" // toolbox.
	AuthResourceTypeMCP      AuthResourceType = "mcp"      // MCP
	AuthResourceTypeOperator AuthResourceType = "operator" // operator.
	AuthResourceTypeSkill    AuthResourceType = "skill"    // Skill

	// AuthResourceTypeSafeAdmin is the super-management capability bit of bkn-safe, not the execution factory's own resource type.
	// bkn-safe's Enforcer.CanAdmin is Check(accessorID, "safe_admin", "console", "manage"),
	// The same triplet is reused here, so that the execution factory's determination of "overrun" and bkn-safe maintain a single source of truth.
	AuthResourceTypeSafeAdmin AuthResourceType = "safe_admin"
)

// SafeAdminConsoleResourceID is the resource ID corresponding to the safe_admin capability bit, which is consistent with the value on the bkn-safe side.
const SafeAdminConsoleResourceID = "console"

func (a AuthResourceType) String() string {
	return string(a)
}

// ResourceID resource ID type alias.
type ResourceID = string

// Special resource ID constant.
const (
	ResourceIDAll = "*" // Represents all resources.
)

// QueryOption query option function type.
type QueryOption[T any, PT PtrBizIdentifiable[T]] func() ([]PT, error)

// ResourceListFunc is a function type that obtains a list of authorized resource IDs.
type ResourceListFunc func() ([]string, error)

// IAuthorizationService Authorization Service interface.
type IAuthorizationService interface {
	// CheckCreatePermission Check new permissions.
	CheckCreatePermission(ctx context.Context, accessor *AuthAccessor, resourceType AuthResourceType) error
	// CheckViewPermission Check view permission.
	CheckViewPermission(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType) error
	// CheckModifyPermission Check editing permissions.
	CheckModifyPermission(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType) error
	// CheckDeletePermission Check delete permission.
	CheckDeletePermission(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType) error
	// CheckPublishPermission Check publishing permission.
	CheckPublishPermission(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType) error
	// CheckUnpublishPermission Check the removal permission.
	CheckUnpublishPermission(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType) error
	// CheckAuthorizePermission Check permission management permissions.
	CheckAuthorizePermission(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType) error
	// CheckPublicAccessPermission Check public access permissions.
	CheckPublicAccessPermission(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType) error
	// CheckExecutePermission Check execution permission.
	CheckExecutePermission(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType) error
	// MultiCheckOperationPermission Multi-operation permission check.
	MultiCheckOperationPermission(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType, operations ...AuthOperationType) error
	// CheckAdminPermission checks super-administrative permissions. The judgment semantics is consistent with Enforcer.CanAdmin of bkn-safe.
	// Used to protect operation and maintenance observation interfaces that return cross-tenant data and do not belong to any single resource.
	CheckAdminPermission(ctx context.Context, accessor *AuthAccessor) error

	// CreateOwnerPolicy creates owner permissions.
	CreateOwnerPolicy(ctx context.Context, accessor *AuthAccessor, authResource *AuthResource) error
	// CreateIntCompPolicyForAllUsers creates an internal component permission policy that affects all users.
	CreateIntCompPolicyForAllUsers(ctx context.Context, authResource *AuthResource) error

	// ResourceFilterIDs resource filtering.
	ResourceFilterIDs(ctx context.Context, accessor *AuthAccessor, resourceIDS []string, resourceType AuthResourceType, operations ...AuthOperationType) ([]string, error)
	// ResourceListIDs resource list.
	ResourceListIDs(ctx context.Context, accessor *AuthAccessor, resourceType AuthResourceType, operations ...AuthOperationType) ([]string, error)

	// OperationCheckAll AND relationship: all operation permissions need to be satisfied.
	OperationCheckAll(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType, operations ...AuthOperationType) (bool, error)
	// OperationCheckAny OR relationship, only needs to satisfy any one operation permission.
	OperationCheckAny(ctx context.Context, accessor *AuthAccessor, resourceID string, resourceType AuthResourceType, operations ...AuthOperationType) (bool, error)
	// CreatePolicy creates a policy.
	CreatePolicy(ctx context.Context, accessor *AuthAccessor, authResource *AuthResource, allow []AuthOperationType, deny []AuthOperationType) error
	// DeletePolicy delete policy.
	DeletePolicy(ctx context.Context, resourceIDs []string, resourceType AuthResourceType) error
	// NotifyResourceChange resource name change message notification.
	NotifyResourceChange(ctx context.Context, authResource *AuthResource) error

	// GetAccessor gets visitor information.
	GetAccessor(ctx context.Context, userID string) (*AuthAccessor, error)
}
