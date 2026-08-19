// Package interfaces define interfaces.
// @file drivenadapters.go
// @description: Inbound interface definition.
package interfaces

//go:generate mockgen -source=drivenadapters.go -destination=../mocks/drivenadapters.go -package=mocks
import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	// SystemUser system.
	SystemUser = "system"
	// UnknownUser Unknown.
	UnknownUser = "unknown"
)

// VisitorType visitor type.
type VisitorType string

// Visitor type definition.
const (
	RealName  VisitorType = "realname"  // Real-name user.
	Anonymous VisitorType = "anonymous" // anonymous user.
	Business  VisitorType = "business"  // application account.
)

// ToAccessorType converts to visitor type.
func (v VisitorType) ToAccessorType() AccessorType {
	switch v {
	case RealName:
		return AccessorTypeUser
	case Business:
		return AccessorTypeApp
	case Anonymous:
		return AccessorTypeAnonymous
	default:
		// Unknown visitor type, default anonymous user.
		return AccessorTypeAnonymous
	}
}

// AccountType Login account type.
type AccountType int32

// Login account type definition.
const (
	Other  AccountType = 0
	IDCard AccountType = 1
)

const (
	// AccessedByUser real-name user.
	AccessedByUser string = "accessed_by_users"
	// AccessedByAnyOne anonymous user.
	AccessedByAnyOne string = "accessed_by_anyone"
)

// ClientType device type.
type ClientType int32

// ClientTypeMap client type table.
var ClientTypeMap = map[ClientType]string{
	Unknown:      "unknown",
	IOS:          "ios",
	Android:      "android",
	WindowsPhone: "windows_phone",
	Windows:      "windows",
	MacOS:        "mac_os",
	Web:          "web",
	MobileWeb:    "mobile_web",
	Nas:          "nas",
	ConsoleWeb:   "console_web",
	DeployWeb:    "deploy_web",
	Linux:        "linux",
	APP:          "app",
}

// ReverseClientTypeMap client type string reverse lookup table.
var ReverseClientTypeMap = map[string]ClientType{
	"unknown":       Unknown,
	"ios":           IOS,
	"android":       Android,
	"windows_phone": WindowsPhone,
	"windows":       Windows,
	"mac_os":        MacOS,
	"web":           Web,
	"mobile_web":    MobileWeb,
	"nas":           Nas,
	"console_web":   ConsoleWeb,
	"deploy_web":    DeployWeb,
	"linux":         Linux,
	"app":           APP,
}

// AccountTypeMap account type table.
var AccountTypeMap = map[AccountType]string{
	Other:  "other_category",
	IDCard: "id_card",
}

// ReverseAccountTypeMap account type string reverse lookup table.
var ReverseAccountTypeMap = map[string]AccountType{
	"other_category": Other,
	"id_card":        IDCard,
}

func (typ ClientType) String() string {
	str, ok := ClientTypeMap[typ]
	if !ok {
		str = ClientTypeMap[Unknown]
	}
	return str
}

// Device type definition.
const (
	Unknown ClientType = iota
	IOS
	Android
	WindowsPhone
	Windows
	MacOS
	Web
	MobileWeb
	Nas
	ConsoleWeb
	DeployWeb
	Linux
	APP
)

// TokenInfo authorization verification information.
type TokenInfo struct {
	Active     bool        // Token status.
	VisitorID  string      // Visitor ID.
	Scope      string      // Scope of authority.
	ClientID   string      // Client ID.
	VisitorTyp VisitorType // Visitor type.
	// The following fields only exist when visitorType=realname, that is, a real-name user.
	LoginIP     string      // Login IP.
	Udid        string      // Device code.
	AccountTyp  AccountType // Account type.
	ClientTyp   ClientType  // Device type.
	PhoneNumber string      // Anonymous user's phone number.
	VisitorName string      // Anonymous external links, visitor’s nickname.
	MAC         string      // MAC address.
	UserAgent   string      // Agent information.
}

// Hydra authorization service interface.
type Hydra interface {
	Introspect(c *gin.Context) (tokenInfo *TokenInfo, err error)
	GenerateVisitor(c *gin.Context) (info *TokenInfo, err error)
}

// AppKeyPrefix identifies the AppKey (API Key) credential issued by the user self-service.
// The public authentication middleware is divided according to this prefix: the ones with this prefix are verified by bkn-safe, and the other bearer tokens are verified by hydra introspection.
const AppKeyPrefix = "bak_"

// AppKeyVerifier parses AppKey into the holder's TokenInfo, and bkn-safe completes the verification.
// The return value is isomorphic to Hydra.Introspect, so the middleware can treat AppKey and OAuth tokens equivalently.
// The downstream AccountAuthContext is exactly the same as the authorization decision.
type AppKeyVerifier interface {
	Verify(ctx context.Context, key string) (tokenInfo *TokenInfo, err error)
}

const (
	// DisplayName User display name.
	DisplayName = "name"
)

// UserInfo User information.
type UserInfo struct {
	UserID      string   `json:"id"`    // User ID.
	DisplayName string   `json:"name"`  // User display name.
	Roles       []string `json:"roles"` // role.
	Account     string   `json:"account"`
}

// AppInfo application account information.
type AppInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ErrorResponse Error response.
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Detail  struct {
		IDs []string `json:"ids"`
	} `json:"detail"`
}

// UserManagement user management interface.
type UserManagement interface {
	GetAppInfo(ctx context.Context, appID string) (appInfo *AppInfo, err error)
	GetUserInfo(ctx context.Context, userID string, fields ...string) (info *UserInfo, err error)
	GetUsersInfo(ctx context.Context, userIDs []string, fields []string) (infos []*UserInfo, err error)
	GetUsersName(ctx context.Context, userIDs []string) (userMap map[string]string, err error)
}

type MCPClient interface {
	// GetInitInfo gets initialization information.
	GetInitInfo(ctx context.Context) *mcp.InitializeResult
	// ListTools Get the list of tools.
	ListTools(ctx context.Context, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error)
	// CallTool call tool.
	CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
	// Close closes the client connection.
	Close() error
}

type MCPToolConfig struct {
	ToolID      string          `json:"tool_id"`      // Tool ID.
	Name        string          `json:"name"`         // Tool name.
	Description string          `json:"description"`  // Tool description.
	InputSchema json.RawMessage `json:"input_schema"` // input mode.
}

type MCPInstanceCreateRequest struct {
	MCPID        string           `json:"mcp_id"`
	Version      int              `json:"version"`
	Name         string           `json:"name"`
	Instructions string           `json:"instructions"`
	ToolConfigs  []*MCPToolConfig `json:"tools"`
}

type MCPInstanceCreateResponse struct {
	MCPID     string `json:"mcp_id"`
	Version   int    `json:"version"`
	StreamURL string `json:"stream_url"`
	SSEURL    string `json:"sse_url"`
}

type MCPInstanceUpdateRequest struct {
	MCPServerName string           `json:"name"`
	Instructions  string           `json:"instructions"`
	ToolConfigs   []*MCPToolConfig `json:"tools"`
}

type MCPInstanceUpdateResponse struct {
	MCPID      string `json:"mcp_id"`
	MCPVersion int    `json:"version"`
	StreamURL  string `json:"stream_url"`
	SSEURL     string `json:"sse_url"`
}

// AccessorType access type.
type AccessorType string

const (
	AccessorTypeUser       AccessorType = "user"       // Real-name user.
	AccessorTypeDepartment AccessorType = "department" // Department.
	AccessorTypeGroup      AccessorType = "group"      // organization.
	AccessorTypeRole       AccessorType = "role"       // role.
	AccessorTypeApp        AccessorType = "app"        // application account.
	AccessorTypeAnonymous  AccessorType = "anonymous"  // Anonymous access.
)

// ToVisitorType Convert AccessorType to VisitorType.
func (a AccessorType) ToVisitorType() VisitorType {
	switch a {
	case AccessorTypeUser:
		return RealName
	case AccessorTypeApp:
		return Business
	case AccessorTypeAnonymous:
		return Anonymous
	case AccessorTypeDepartment, AccessorTypeGroup, AccessorTypeRole:
		return ""
	default:
		return ""
	}
}

const (
	// AccessorRootDepartmentID root department ID.
	AccessorRootDepartmentID string = "00000000-0000-0000-0000-000000000000"
)

// AuthMethod authorization method.
type AuthMethod = string

// Supported authorization methods.
const (
	AuthMethodGet    AuthMethod = "GET"
	AuthMethodDelete AuthMethod = "DELETE"
)

// AuthAccessor visitor information.
type AuthAccessor struct {
	ID   string       `json:"id"`   // Unique identification ID.
	Type AccessorType `json:"type"` // access type.
	Name string       `json:"name"` // Visitor name.
}

// AuthResource resource information.
type AuthResource struct {
	ID   string `json:"id"`   // Unique identification ID.
	Type string `json:"type"` // Resource type.
	Name string `json:"name"` // Resource name.
}

// AuthOperationCheckRequest operation check request.
type AuthOperationCheckRequest struct {
	Accessor  *AuthAccessor       `json:"accessor"`  // Visitor information.
	Resource  *AuthResource       `json:"resource"`  // Resource information.
	Operation []AuthOperationType `json:"operation"` // Check the operation.
	Method    string              `json:"method"`    // method.
}

// AuthOperationCheckResponse operation check response.
type AuthOperationCheckResponse struct {
	Result bool `json:"result"` // Check results.
}

// ResourceListRequest resource listing request.
type ResourceListRequest struct {
	Accessor  *AuthAccessor       `json:"accessor"`  // Visitor information.
	Resource  *AuthResource       `json:"resource"`  // Resource information.
	Operation []AuthOperationType `json:"operation"` // Check the operation.
	Method    string              `json:"method"`    // method.
}

// AuthResourceFilterRequest resource filtering request.
type AuthResourceFilterRequest struct {
	Accessor   *AuthAccessor       `json:"accessor"`  // Visitor information.
	Resources  []*AuthResource     `json:"resources"` // Resource list.
	Operations []AuthOperationType `json:"operation"` // List of actions to check.
	Method     string              `json:"method"`    // method.
}

type AuthOperation struct {
	ID   string `json:"id"`   // Unique identification ID.
	Name string `json:"name"` // Operation name.
}

type PolicyOperation struct {
	Allow []*AuthOperation `json:"allow"` // allowed operations.
	Deny  []*AuthOperation `json:"deny"`  // Denied action.
}

// AuthCreatePolicyRequest New policy request.
type AuthCreatePolicyRequest struct {
	Accessor  *AuthAccessor    `json:"accessor"`             // Visitor information.
	Resource  *AuthResource    `json:"resource"`             // Resource information.
	Operation *PolicyOperation `json:"operation"`            // Strategy operations.
	Condition string           `json:"condition,omitempty"`  // Conditions.
	ExpiresAt string           `json:"expires_at,omitempty"` // Expiration time (second level), RFC3339 format, UNIX TIME time epoch (1970-01-01T08:00:00+08:00) means permanently valid.
}

// AuthDeletePolicyRequest delete policy request.
type AuthDeletePolicyRequest struct {
	Method    string          `json:"method"`    // method.
	Resources []*AuthResource `json:"resources"` // Resource list.
}

// AuthResourceResult resource result.
type AuthResourceResult struct {
	ID string `json:"id"` // Unique identification ID.
}

// Authorization authorization service interface.
type Authorization interface {
	// single decision.
	OperationCheck(ctx context.Context, req *AuthOperationCheckRequest) (*AuthOperationCheckResponse, error)
	// Resource filtering.
	ResourceFilter(ctx context.Context, req *AuthResourceFilterRequest) ([]*AuthResourceResult, error)
	// Resource enumeration.
	ResourceList(ctx context.Context, req *ResourceListRequest) ([]*AuthResourceResult, error)
	// New strategy.
	CreatePolicy(ctx context.Context, req []*AuthCreatePolicyRequest) error
	// Policy deletion.
	DeletePolicy(ctx context.Context, req *AuthDeletePolicyRequest) error
}

// AuditLogModel audit log model.
type AuditLogModel struct {
	Operation   string               `json:"operation" validate:"required"`          // Operation type.
	Description string               `json:"description" validate:"required"`        // String description, maximum length 65,535.
	OpTime      int64                `json:"op_time" validate:"required"`            // Operation time (required for reporting through mq) is accurate to nanoseconds.
	Operator    AuditLogOperatorInfo `json:"operator" validate:"required"`           // Operator information.
	Object      AuditLogObjectInfo   `json:"object,omitempty"`                       // Operation object information.
	LogFrom     LogFrom              `json:"log_from" validate:"required"`           // Log source.
	Detail      interface{}          `json:"detail,omitempty"`                       // Details.
	ExMsg       string               `json:"ex_msg,omitempty"`                       // Additional information, maximum length 65,535.
	Level       LoggerLevel          `json:"level" validate:"required"`              // Log level, default INFO.
	OutBizID    string               `json:"out_biz_id" validate:"required,max=128"` // External unique business ID, used for anti-shake, format is not limited, up to 128.
	Type        AuditLogType         `json:"type" validate:"required"`               // Log type, maximum length 128.
}

// LogFrom log source.
type LogFrom struct {
	Package string      `json:"package" validate:"required"` // Big package name.
	Service ServiceInfo `json:"service" validate:"required"` // Service information.
}

// ServiceInfo service information.
type ServiceInfo struct {
	Name string `json:"name" validate:"required"` // Service name.
}

// LoggerLevel log level.
type LoggerLevel string

const (
	// LoggerLevelInfo information.
	LoggerLevelInfo LoggerLevel = "INFO"
	// LoggerLevelWarn warning.
	LoggerLevelWarn LoggerLevel = "WARN"
)

// AuditLogObjectInfo operation object information.
type AuditLogObjectInfo struct {
	Type string `json:"type" validate:"required"` // Operate type.
	Name string `json:"name"`                     // Operation object name, maximum length 128.
	ID   string `json:"id"`                       // Operation object ID, maximum length 40.
}

// AuditLogOperatoAgent operator agent information.
type AuditLogOperatoAgent struct {
	Type string `json:"type" validate:"required"` // Operator client type.
	IP   string `json:"ip" validate:"required"`   // Operator device IP.
	MAC  string `json:"mcp" validate:"required"`  // Operator device mac address.
}

// AuditLogOperatorInfo operator information.
type AuditLogOperatorInfo struct {
	ID    string               `json:"id" validate:"required,max=40"`    // Operator ID, maximum length 40.
	Name  string               `json:"name" validate:"required,max=128"` // Operator name, subject to the incoming data, the maximum length is 128, type is internal_service and must be passed.
	Type  AuditLogOperatorType `json:"type" validate:"required"`         // Operator type.
	Agent AuditLogOperatoAgent `json:"agent" validate:"required"`        // Operator agent information.
}

// AuditLogOperatorType operator type.
type AuditLogOperatorType string

const (
	// AuthenticatedUser real-name user.
	AuthenticatedUser AuditLogOperatorType = "authenticated_user"
	// AnonymousUser Anonymous user.
	AnonymousUser AuditLogOperatorType = "anonymous_user"
	// AppUser application account.
	AppUser AuditLogOperatorType = "app"
	// InternalService internal service.
	InternalService AuditLogOperatorType = "internal_service"
)

// AuditLogOperationType Audit log operation type.
type AuditLogOperationType string

const (
	// AuditLogOperationTypeCreate New.
	AuditLogOperationTypeCreate AuditLogOperationType = "create"
	// AuditLogOperationTypeDelete Delete.
	AuditLogOperationTypeDelete AuditLogOperationType = "delete"
	// AuditLogOperationTypeModify Edit.
	AuditLogOperationTypeModify AuditLogOperationType = "modify"
	// AuditLogOperationTypePublish Publish.
	AuditLogOperationTypePublish AuditLogOperationType = "publish"
	// AuditLogOperationTypeUnpublish removed.
	AuditLogOperationTypeUnpublish AuditLogOperationType = "unpublish"
	// AuditLogOperationTypeExecute execution.
	AuditLogOperationTypeExecute AuditLogOperationType = "execute"
)

// AuditLogType log type.
type AuditLogType string

const (
	// AuditLogOperation operation log.
	AuditLogOperation AuditLogType = "operation" // Operation log.
)

// BusinessDomainResource business domain resource information.
type BusinessDomainResource struct {
	BDID string `json:"bd_id"` // Business domain ID.
	ID   string `json:"id"`    // Resource ID.
	Type string `json:"type"`  // Resource type.
}

// BusinessDomainResourceListRequest Business domain resource list query request.
type BusinessDomainResourceListRequest struct {
	BDID   string `json:"bd_id"`  // Business domain ID.
	ID     string `json:"id"`     // Resource ID.
	Type   string `json:"type"`   // Resource type.
	Limit  int    `json:"limit"`  // Data volume, default: 20, -1 means no paging, full query.
	Offset int    `json:"offset"` // Data offset, default 0.
}

// BusinessDomainResourceListResponse Business domain resource list query response.
type BusinessDomainResourceListResponse struct {
	Limit  int                       `json:"limit"`  // Data volume.
	Offset int                       `json:"offset"` // data offset.
	Total  int                       `json:"total"`  // Total data.
	Items  []*BusinessDomainResource `json:"items"`  // Data content.
}

// BusinessDomainResourceAssociateRequest Business domain resource association request.
type BusinessDomainResourceAssociateRequest struct {
	BDID string `json:"bd_id"` // Business domain ID.
	ID   string `json:"id"`    // Resource ID.
	Type string `json:"type"`  // Resource type.
}

// BusinessDomainResourceDisassociateRequest Business domain resource disassociation request.
type BusinessDomainResourceDisassociateRequest struct {
	BDID string `json:"bd_id"` // Business domain ID.
	ID   string `json:"id"`    // Resource ID.
	Type string `json:"type"`  // Resource type.
}

// BusinessDomainManagement business domain management service interface.
type BusinessDomainManagement interface {
	// Resource association.
	AssociateResource(ctx context.Context, req *BusinessDomainResourceAssociateRequest) error
	// Resource disassociation.
	DisassociateResource(ctx context.Context, req *BusinessDomainResourceDisassociateRequest) error
	// Resource list query.
	ResourceList(ctx context.Context, req *BusinessDomainResourceListRequest) (*BusinessDomainResourceListResponse, error)
}

// ExecuteCodeReq execute code request.
type ExecuteCodeReq struct {
	Code                  string            `json:"code" validate:"required"`                                    // Execute code.
	Event                 map[string]any    `json:"event,omitempty"`                                             // event.
	Language              string            `json:"language" default:"python"`                                   // execution language.
	Timeout               int               `json:"timeout,omitempty"`                                           // Timeout time in seconds.
	WorkingDirectory      string            `json:"working_directory,omitempty"`                                 // Working directory, relative to the workspace root directory.
	EnvVars               map[string]any    `json:"env_vars,omitempty"`                                          // Session business context environment variables.
	Dependencies          []*DependencyInfo `json:"dependencies,omitempty"`                                      // Depend on resources.
	PythonPackageIndexURL string            `json:"python_package_index_url" default:"https://pypi.org/simple/"` // Installation source URL.
}

// ExecuteCodeResp execute code response.
type ExecuteCodeResp struct {
	ID            string `json:"id"`             // Execution ID.
	SessionID     string `json:"session_id"`     // Session ID.
	Code          string `json:"code"`           // Execute code.
	Language      string `json:"language"`       // execution language.
	Timeout       int    `json:"timeout"`        // Timeout time in seconds.
	ExitCode      int    `json:"exit_code"`      // exit code.
	ErrorMessage  string `json:"error_message"`  // error message.
	ExecutionTime int64  `json:"execution_time"` // Execution time in milliseconds.
	Artifacts     any    `json:"artifacts"`      // Document Artifact Response.
	RetryCount    int    `json:"retry_count"`    // Number of retries.
	Stdout        string `json:"stdout"`         // standard output.
	Stderr        string `json:"stderr"`         // standard error output.
	Metrics       any    `json:"metrics"`        // Execution metrics.
	CreatedAt     string `json:"created_at"`     // Creation time in milliseconds.
	StartedAt     string `json:"started_at"`     // Start time in milliseconds.
	CompletedAt   string `json:"completed_at"`   // Completion time in milliseconds.
	ReturnValue   any    `json:"return_value"`   // execution result value.
}

// QueryPythonPackagesReq Query Python third-party package requests.
type QueryPythonPackagesReq struct {
	PythonVersion string `json:"python_version"`                              // Python version.
	PackageName   string `json:"package_name"`                                // Third-party package name.
	PypiURL       string `json:"pypi_url" default:"https://pypi.org/simple/"` // PyPI URL
}

// QueryPythonPackagesResp Query Python third-party package response.
type QueryPythonPackagesResp struct {
	PackageName string   `json:"package_name"` // Third-party package name.
	Versions    []string `json:"versions"`     // Version list.
}

// SandBoxConfigReq sandbox environment configuration request.
type SandBoxConfigReq struct {
	Timeout int            `json:"timeout"` // Timeout time in seconds.
	Body    any            `json:"body"`    // Request body.
	Headers map[string]any `json:"headers"` // Request header.
	Code    string         `json:"code"`    // Execute code.
}

// CreateSessionReq Create session request.
type CreateSessionReq struct {
	ID                    string         `json:"id"`                                 // Session ID.
	TemplateID            string         `json:"template_id"`                        // Template ID.
	Timeout               int            `json:"timeout"`                            // Timeout time in seconds.
	CPU                   string         `json:"cpu,omitempty"`                      // Number of CPU cores.
	Memory                string         `json:"memory,omitempty"`                   // Memory limit, unit MB.
	Disk                  string         `json:"disk,omitempty"`                     // Disk mount point.
	EnvVars               map[string]any `json:"env_vars,omitempty"`                 // environment variables.
	Event                 map[string]any `json:"event,omitempty"`                    // event data.
	InstallTimeout        int            `json:"install_timeout,omitempty"`          // Dependency installation timeout (seconds), default 300.
	FailOnDependencyError bool           `json:"fail_on_dependency_error,omitempty"` // Dependency installation failure will directly fail the session. Default is true.
	AllowVersionConflicts bool           `json:"allow_version_conflicts,omitempty"`  // Whether to allow version conflicts, default false.
	// issue #253 Added a new field, whether the session has installed dependent libraries.
	PythonPackageIndexURL string            `json:"python_package_index_url,omitempty"` // Python third-party package index URL.
	Dependencies          []*DependencyInfo `json:"dependencies,omitempty"`             // Depend on resources.
}

type SessionStatus string

const (
	SessionStatusCreating   SessionStatus = "creating"   // Creating.
	SessionStatusFailed     SessionStatus = "failed"     // failed.
	SessionStatusRunning    SessionStatus = "running"    // Running.
	SessionStatusTerminated SessionStatus = "terminated" // terminated.
)

// SessionDetail session details.
type SessionDetail struct {
	ID             string         `json:"id"`               // Session ID.
	TemplateID     string         `json:"template_id"`      // Template ID.
	Status         SessionStatus  `json:"status"`           // session state.
	ResourceLimit  map[string]any `json:"resource_limit"`   // Session resource configuration.
	WorkspacePath  string         `json:"workspace_path"`   // workspace path.
	RuntimeType    string         `json:"runtime_type"`     // Runtime type.
	RuntimeNode    string         `json:"runtime_node"`     // runtime node.
	PodName        string         `json:"pod_name"`         // Container name.
	EnvVars        map[string]any `json:"env_vars"`         // environment variables.
	Timeout        int            `json:"timeout"`          // Timeout time in seconds.
	CreateAt       string         `json:"created_at"`       // creation time.
	UpdateAt       string         `json:"updated_at"`       // Update time.
	CompletedAt    string         `json:"completed_at"`     // completion time.
	LastActivityAt string         `json:"last_activity_at"` // Last activity time.
	// issue #253 Added a new field, whether the session has installed dependent libraries.
	LanguageRuntime              string            `json:"language_runtime"`                          // Execution language runtime.
	PythonPackageIndexURL        string            `json:"python_package_index_url,omitempty"`        // Python third-party package index URL.
	RequestedDependencies        []*DependencyInfo `json:"requested_dependencies,omitempty"`          // Requested dependent resources.
	InstalledDependencies        []*DependencyInfo `json:"installed_dependencies,omitempty"`          // Installed dependencies.
	DependencyInstallStatus      string            `json:"dependency_install_status,omitempty"`       // Depends on installation status.
	DependencyInstallError       string            `json:"dependency_install_error,omitempty"`        // Dependency installation error message.
	DependencyInstallStartedAt   string            `json:"dependency_install_started_at,omitempty"`   // Depends on installation start time.
	DependencyInstallCompletedAt string            `json:"dependency_install_completed_at,omitempty"` // Depends on installation completion time.
}

// Dependency library information.
type DependencyInfo struct {
	Name            string `json:"name"`                       // Third-party package name.
	Version         string `json:"version"`                    // version number.
	InstallLocation string `json:"install_location,omitempty"` // Installation location.
	InstallTime     string `json:"install_time,omitempty"`     // Installation time.
	IsFromTemplate  *bool  `json:"is_from_template,omitempty"` // Whether to install from a template.
}

// ListSessionsReq lists session requests.
type ListSessionsReq struct {
	Limit  int           `json:"limit"`  // paging size.
	Offset int           `json:"offset"` // paging offset.
	Status SessionStatus `json:"status"` // session state.
}

// ListSessionsResp lists session responses.
type ListSessionsResp struct {
	Sessions []*SessionDetail `json:"items"`    // Conversation list.
	Total    int              `json:"total"`    // Total number of sessions.
	Limit    int              `json:"limit"`    // paging size.
	Offset   int              `json:"offset"`   // paging offset.
	HasMore  bool             `json:"has_more"` // Is there more data?.
}

// InstallDependenciesReq installation dependency request.
type InstallDependenciesReq struct {
	Dependencies []*DependencyInfo `json:"dependencies"` // Depend on resources.
	// For example pypi https://pypi.org/simple.
	PythonPackageIndexURL string `json:"python_package_index_url,omitempty"` // Python third-party package index URL.
}

// UploadSkillArchiveReq Upload Skill compressed package request.
type UploadSkillArchiveReq struct {
	WorkDir  string `json:"work_dir"`  // session working directory.
	FileName string `json:"file_name"` // Compressed package file name.
	Content  []byte `json:"content"`   // Compressed package contents.
}

// UploadSkillArchiveResp Upload Skill compressed package response.
type UploadSkillArchiveResp struct {
	SessionID          string `json:"session_id"`
	Mode               string `json:"mode,omitempty"`
	WorkDir            string `json:"work_dir"`
	FileName           string `json:"file_name"`
	UploadedPath       string `json:"uploaded_path"`
	Size               int64  `json:"size"`
	ExtractedFileCount int    `json:"extracted_file_count,omitempty"`
	SkippedFileCount   int    `json:"skipped_file_count,omitempty"`
	Mocked             bool   `json:"mocked"`
}

// ExecuteShellReq executes the shell request.
type ExecuteShellReq struct {
	WorkDir string `json:"work_dir"` // session working directory.
	Command string `json:"command"`  // shell command.
	Timeout int    `json:"timeout"`  // Timeout time in seconds.
}

// ExecuteShellResp execute shell response.
type ExecuteShellResp struct {
	SessionID     string `json:"session_id"`
	WorkDir       string `json:"work_dir"`
	Command       string `json:"command"`
	ExitCode      int    `json:"exit_code"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	ExecutionTime int64  `json:"execution_time"`
	Mocked        bool   `json:"mocked"`
}

// SandBoxControlPlane sandbox control service interface.
type SandBoxControlPlane interface {
	// Get template details.
	GetTemplateDetail(ctx context.Context, tempID string) (any, error)
	// Create session.
	CreateSession(ctx context.Context, req *CreateSessionReq) (any, error)
	// query session.
	QuerySession(ctx context.Context, sessionID string) (exists bool, detail *SessionDetail, err error)
	// Delete session.
	DeleteSession(ctx context.Context, sessionID string) (err error)
	// List sessions.
	ListSessions(ctx context.Context, req *ListSessionsReq) (resp *ListSessionsResp, err error)
	// Execute function (synchronous)
	ExecuteCodeSync(ctx context.Context, sessionID string, req *ExecuteCodeReq) (*ExecuteCodeResp, error)
	// Incrementally install Python dependencies.
	InstallPythonDependencies(ctx context.Context, sessionID string, req *InstallDependenciesReq) (detail *SessionDetail, err error)
	// Upload Skill compressed package.
	UploadSkillArchive(ctx context.Context, sessionID string, req *UploadSkillArchiveReq) (*UploadSkillArchiveResp, error)
	// Execute shell command.
	ExecuteShell(ctx context.Context, sessionID string, req *ExecuteShellReq) (*ExecuteShellResp, error)
}

// ChatCompletionReq chat completion request.
type ChatCompletionReq struct {
	Model            string                  `json:"model"`             // Model name. When an empty string ("") is passed and no model_id is passed, it means calling the default model (if the admin does not configure a global default model, the call will report an error)
	Messages         []ChatCompletionMessage `json:"messages"`          // Message list.
	Stream           bool                    `json:"stream"`            // Whether to stream returns, default false.
	TopK             int                     `json:"top_k"`             // Sampling pool size, select only from the top k tokens with the highest probability, k is an integer. Limit the range of candidates during generation. k=1 is equivalent to a greedy search (completely deterministic); k=50 allows more diversity but may reduce relevance. Value range 1~∞.
	TopP             float64                 `json:"top_p"`             // Kernel sampling, with a value ranging from 0 to 1, balances the diversity and quality of generated results. The smaller the value, the more concentrated the output (for example, 0.9 only retains the parts with the highest probability); the larger the value, the more random the output.
	FrequencyPenalty float64                 `json:"frequency_penalty"` // Frequency penalty, which reduces the probability of repeated tokens, usually ranges from -2.0~2.0. Suppress duplicate content. Positive values (such as 0.5) penalize repetition of words; negative values encourage repetition (less used)
	PresencePenalty  float64                 `json:"presence_penalty"`  // There is a penalty that reduces the probability of tokens that have appeared, usually in the range of -2.0~2.0. Encourage the generation of new topics or vocabulary. For example, when set to 0.2, the model will avoid reusing generated words.
	Temperature      float64                 `json:"temperature"`       // Control randomness (high = creative, low = rigorous), 0.1 generates conservative results, 1.0 is more flexible, 0~1, some saas models do not support the 0 value.
	MaxTokens        int                     `json:"max_tokens"`        // Maximum generation length, the value range cannot exceed the maximum context length of the model.
	ModelID          string                  `json:"model_id"`          // Model ID. When an empty string ("") is passed and no model is passed, it means calling the default model (if the admin does not configure a global default model, the call will report an error)
}

// ChatCompletionResp chat completion response.
type ChatCompletionResp struct {
	ID      string                 `json:"id"`      // Response ID.
	Object  string                 `json:"object"`  // Object type, fixed value "chat.completion".
	Created int64                  `json:"created"` // Create timestamp.
	Model   string                 `json:"model"`   // Model name.
	Choices []ChatCompletionChoice `json:"choices"` // Generate result list.
	Usage   ChatCompletionUsage    `json:"usage"`   // Consumption statistics.
}

type ChatCompletionChoice struct {
	Index        int                   `json:"index"`             // Result index.
	Message      ChatCompletionMessage `json:"message,omitempty"` // Message content, empty when returned by streaming.
	Delta        ChatCompletionMessage `json:"delta,omitempty"`   // Incremental message content, empty when non-streaming return.
	FinishReason string                `json:"finish_reason"`     // Completion reason.
	Flag         int                   `json:"flag"`              // Flag bit.
}

// ChatCompletionMessage message structure.
type ChatCompletionMessage struct {
	Role    string `json:"role,omitempty"`    // role.
	Content string `json:"content,omitempty"` // content.
}

// ChatCompletionUsage consumption statistics structure.
type ChatCompletionUsage struct {
	PromptTokens        int                       `json:"prompt_tokens"`         // Prompt word token number.
	CompletionTokens    int                       `json:"completion_tokens"`     // Complete token count.
	TotalTokens         int                       `json:"total_tokens"`          // Total number of tokens.
	PromptTokensDetails ChatCompletionTokenDetail `json:"prompt_tokens_details"` // Prompt word token number details.
}

// ChatCompletionTokenDetail prompt word token number detail structure.
type ChatCompletionTokenDetail struct {
	CachedTokens   int `json:"cached_tokens"`   // Number of cached tokens.
	UncachedTokens int `json:"uncached_tokens"` // Number of uncached tokens.
}

// MFModelAPIClient model management API interface.
type MFModelAPIClient interface {
	// call model.
	ChatCompletion(ctx context.Context, req *ChatCompletionReq) (resp *ChatCompletionResp, err error)
	// Call model streaming return.
	StreamChatCompletion(ctx context.Context, req *ChatCompletionReq) (chan string, chan error, error)
	// Get embedding vector.
	Embeddings(ctx context.Context, req *EmbeddingReq) (resp *EmbeddingResp, err error)
}

// GetPromptResp Gets the prompt word response.
type GetPromptResp struct {
	PromptID   string `json:"prompt_id"`   // Prompt word ID.
	PromptName string `json:"prompt_name"` // Prompt word name.
	ModelID    string `json:"model_id"`    // Model ID.
	ModelName  string `json:"model_name"`  // Model name.
	Messages   string `json:"messages"`    // Prompt word content.
}

const (
	SmallModelTypeEmbedding = "embedding"
)

type EmbeddingModel struct {
	ModelID      string `json:"model_id"`
	ModelName    string `json:"model_name"`
	ModelType    string `json:"model_type"`
	EmbeddingDim int    `json:"embedding_dim"`
	BatchSize    int    `json:"batch_size"`
	MaxTokens    int    `json:"max_tokens"`
}

type EmbeddingReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type EmbeddingData struct {
	Object    string    `json:"object"`
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type EmbeddingResp struct {
	Data []EmbeddingData `json:"data"`
}

// MFModelManager model management interface.
type MFModelManager interface {
	// Get prompt words.
	GetPromptByPromptID(ctx context.Context, promptID string) (resp *GetPromptResp, err error)
	// Get embedding model information.
	GetEmbeddingModel(ctx context.Context, modelName string, modelType string) (resp *EmbeddingModel, err error)
	// GetDefaultEmbeddingModel gets the system default small model under a certain model_type; returns (nil, nil) when the default is not configured.
	GetDefaultEmbeddingModel(ctx context.Context, modelType string) (resp *EmbeddingModel, err error)
}

type VegaPropertyFeature struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	FeatureType string         `json:"feature_type"`
	Description string         `json:"description"`
	RefProperty string         `json:"ref_property"`
	IsDefault   bool           `json:"is_default"`
	IsNative    bool           `json:"is_native"`
	Config      map[string]any `json:"config,omitempty"`
}

type VegaProperty struct {
	Name         string                `json:"name"`
	Type         string                `json:"type"`
	DisplayName  string                `json:"display_name"`
	OriginalName string                `json:"original_name"`
	Description  string                `json:"description"`
	Features     []VegaPropertyFeature `json:"features,omitempty"`
}

type VegaCatalogRequest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	// Internal system internal catalog: registered in the permission service by internal_catalog type, visible only to super administrators.
	Internal bool `json:"internal"`
	// Enabled Directory enabled status. If the logical directory is false, the reading and writing of the dataset under it will be blocked.
	// vega is rejected with Catalog.IsDisabled(409), so the built-in catalog must be enabled.
	Enabled bool `json:"enabled"`
	// ConnectorType Connector type: The logical directory is always empty. This field is immutable when updated by PUT.
	// The current value must be backfilled unchanged, otherwise vega returns 400.
	ConnectorType string `json:"connector_type,omitempty"`
}

type VegaCatalog struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Tags          []string `json:"tags"`
	Description   string   `json:"description"`
	Type          string   `json:"type"`
	Enabled       bool     `json:"enabled"`
	ConnectorType string   `json:"connector_type"`
}

// VegaResourceIndexConfig resource-level index configuration. DefaultEmbeddingModel is a vega parsing vector.
// The bottom position of the field model, and will not enter OpenSearch mapping - the model snapshot must fall here and cannot.
// into the feature config of the vector attribute (that will be copied unchanged into knn_vector mapping and used by OpenSearch.
// Rejected with unknown parameter, the index cannot be built).
type VegaResourceIndexConfig struct {
	DefaultEmbeddingModel string `json:"default_embedding_model,omitempty"`
}

type VegaResourceRequest struct {
	ID               string                   `json:"id"`
	CatalogID        string                   `json:"catalog_id"`
	Name             string                   `json:"name"`
	Tags             []string                 `json:"tags"`
	Description      string                   `json:"description"`
	Category         string                   `json:"category"`
	Status           string                   `json:"status"`
	SourceIdentifier string                   `json:"source_identifier"`
	SchemaDefinition []VegaProperty           `json:"schema_definition"`
	IndexConfig      *VegaResourceIndexConfig `json:"index_config,omitempty"`
}

type VegaResource struct {
	ID               string                   `json:"id"`
	CatalogID        string                   `json:"catalog_id"`
	Name             string                   `json:"name"`
	Tags             []string                 `json:"tags"`
	Description      string                   `json:"description"`
	Category         string                   `json:"category"`
	Status           string                   `json:"status"`
	SourceIdentifier string                   `json:"source_identifier"`
	SchemaDefinition []VegaProperty           `json:"schema_definition,omitempty"`
	IndexConfig      *VegaResourceIndexConfig `json:"index_config,omitempty"`
}

type VegaBackendClient interface {
	GetCatalogByID(ctx context.Context, id string) (*VegaCatalog, error)
	CreateCatalog(ctx context.Context, req *VegaCatalogRequest) (*VegaCatalog, error)
	// UpdateCatalog updates the display information of the catalog (name/label/description). enabled and connector_type.
	// It cannot be changed by this interface, and the caller must backfill it as it is.
	UpdateCatalog(ctx context.Context, req *VegaCatalogRequest) error
	// EnableCatalog enables the catalog (vega's enabled can only access this endpoint, PUT will be changed to 409).
	EnableCatalog(ctx context.Context, id string) error
	GetResourceByID(ctx context.Context, id string) (*VegaResource, error)
	CreateResource(ctx context.Context, req *VegaResourceRequest) (*VegaResource, error)
	WriteDatasetDocuments(ctx context.Context, datasetID string, documents []map[string]any) error
	UpdateDatasetDocuments(ctx context.Context, datasetID string, documents []map[string]any) error
	DeleteDatasetDocumentByID(ctx context.Context, datasetID string, docID string) error
}

// OssObject OSS object structure.
type OssObject struct {
	StorageID  string
	StorageKey string
}

// OSSGatewayBackendClient OSS gateway backend client interface.
type OSSGatewayBackendClient interface {
	UploadFile(ctx context.Context, object *OssObject, content []byte) error
	GetDownloadURL(ctx context.Context, object *OssObject) (string, error)
	DownloadFile(ctx context.Context, object *OssObject) (data []byte, err error)
	DeleteFile(ctx context.Context, object *OssObject) error
	CurrentStorageID(ctx context.Context) (string, error)
	Close() error
	IsReady() bool
}
