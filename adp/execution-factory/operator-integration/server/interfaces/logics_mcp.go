package interfaces

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

//go:generate mockgen -source=logics_mcp.go -destination=../mocks/logics_mcp.go -package=mocks

// MCPMode MCP operating mode.
type MCPMode string

func (b MCPMode) String() string {
	return string(b)
}

const (
	MCPModeStdioUv  MCPMode = "stdio_uv"  // Standard UV.
	MCPModeStdioNpx MCPMode = "stdio_npx" // StandardNPX.
	MCPModeSSE      MCPMode = "sse"       // SSE
	MCPModeStream   MCPMode = "stream"    // streaming.
)

// MCPCreationType MCP creation type.
type MCPCreationType string

func (b MCPCreationType) String() string {
	return string(b)
}

const (
	MCPCreationTypeCustom       MCPCreationType = "custom"        // Customize.
	MCPCreationTypeToolImported MCPCreationType = "tool_imported" // Tool import.
)

// MCPParseSSERequest MCP parses SSE request.
type MCPParseSSERequest struct {
	Mode    MCPMode           `json:"mode" validate:"required,oneof=stdio_uv stdio_npx sse stream"` // operating mode.
	URL     string            `json:"url" validate:"required,url"`                                  // Request URL.
	Headers map[string]string `json:"headers"`                                                      // Request header.
}

type MCPParseSSEResponse struct {
	Tools          []mcp.Tool            `json:"tools"`     // Tools.
	ServerInitInfo *mcp.InitializeResult `json:"init_info"` // Initialization information.
}

// MCPCoreConfigInfo MCP core information.
type MCPCoreConfigInfo struct {
	Mode    MCPMode           `json:"mode,omitempty" default:"stream" validate:"required,oneof=sse stream"` // operating mode.
	Command string            `json:"command,omitempty"`                                                    // Run command.
	Args    []string          `json:"args,omitempty"`                                                       // Operating parameters.
	URL     string            `json:"url,omitempty" validate:"omitempty,url"`                               // Service URL.
	Headers map[string]string `json:"headers,omitempty"`                                                    // Request header.
	Env     map[string]string `json:"env,omitempty"`                                                        // environment variables.
}

type MCPToolConfigInfo struct {
	BoxID           string `json:"box_id"`      // Toolbox ID.
	ToolID          string `json:"tool_id"`     // Tool ID.
	BoxName         string `json:"box_name"`    // Toolbox name.
	ToolName        string `json:"tool_name"`   // Tool name.
	ToolDescription string `json:"description"` // Tool description.
	UseRule         string `json:"use_rule"`    // Usage rules.
}

type MCPAppEndpointRequest struct {
	MCPID string `uri:"mcp_id" validate:"required"` // MCP Server ID
}

// MCPServerAddRequest MCP Server registration request.
type MCPServerAddRequest struct {
	MCPCoreConfigInfo
	BusinessDomainID string               `header:"x-business-domain" validate:"required"`                                       // Business domain ID.
	UserID           string               `header:"user_id"`                                                                     // User ID, internal use.
	IsPublic         bool                 `header:"is_public"`                                                                   // Is it a public interface?.
	CreationType     MCPCreationType      `json:"creation_type" default:"custom" validate:"required,oneof=custom tool_imported"` // Create type.
	Name             string               `json:"name" validate:"required"`                                                      // MCP Server name.
	Description      string               `json:"description"`                                                                   // Description information.
	Source           string               `json:"source" default:"custom"`                                                       // Source.
	IsInternal       bool                 `json:"is_internal" default:"false"`                                                   // Whether it is built-in.
	Category         string               `json:"category" default:"other_category"`                                             // Classification.
	ToolConfigs      []*MCPToolConfigInfo `json:"tool_configs"`                                                                  // Tool configuration.
}

// MCPServerAddResponse MCP Server registration response.
type MCPServerAddResponse struct {
	MCPID  string `json:"mcp_id"` // MCP Server ID
	Status string `json:"status"` // Status.
}

// MCPServerDeleteRequest MCP Server delete request.
type MCPServerDeleteRequest struct {
	BusinessDomainID string `header:"x-business-domain" validate:"required"` // Business domain ID.
	UserID           string `header:"user_id"`                               // User ID, internal use.
	IsPublic         bool   `header:"is_public"`                             // Is it a public interface?.
	MCPID            string `uri:"mcp_id" validate:"required"`               // MCP Server ID
}

type MCPServerConfigInfo struct {
	MCPCoreConfigInfo `json:",inline"`
	BusinessDomainID  string               `json:"business_domain_id"`                          // Business domain ID.
	MCPID             string               `json:"mcp_id"`                                      // MCP Server ID
	Version           int                  `json:"version,omitempty"`                           // MCP Server version.
	CreationType      MCPCreationType      `json:"creation_type,omitempty"`                     // Create type.
	Name              string               `json:"name,omitempty"`                              // MCP Server name.
	Description       string               `json:"description,omitempty"`                       // Description information.
	Status            string               `json:"status,omitempty"`                            // Status.
	Source            string               `json:"source,omitempty"`                            // Source.
	IsInternal        bool                 `json:"is_internal"`                                 // Whether it is built-in.
	Category          string               `json:"category,omitempty" default:"other_category"` // Classification.
	CreateUser        string               `json:"create_user,omitempty"`                       // Create user.
	CreateTime        int64                `json:"create_time,omitempty"`                       // creation time.
	UpdateUser        string               `json:"update_user,omitempty"`                       // Update user.
	UpdateTime        int64                `json:"update_time,omitempty"`                       // Update time.
	ReleaseTime       int64                `json:"release_time,omitempty"`                      // Release time.
	ReleaseUser       string               `json:"release_user,omitempty"`                      // publish user.
	ToolConfigs       []*MCPToolConfigInfo `json:"tool_configs,omitempty"`                      // Tool configuration.
}

func (m *MCPServerConfigInfo) ToMapByFields(fields []string) map[string]any {
	// First serialize to JSON, then deserialize to map.
	data, _ := json.Marshal(m)
	var fullMap map[string]interface{}
	_ = json.Unmarshal(data, &fullMap)

	// If fields are not specified, all fields are returned.
	if len(fields) == 0 {
		return fullMap
	}

	// Return as needed based on fields.
	result := make(map[string]interface{})
	for _, field := range fields {
		if value, exists := fullMap[field]; exists {
			result[field] = value
		}
	}

	return result
}

// MCPConnectionInfo MCP connection information.
type MCPConnectionInfo struct {
	SSEURL    string `json:"sse_url,omitempty"`    // SSE URL
	StreamURL string `json:"stream_url,omitempty"` // Streaming URL. If empty, streaming is not supported.
}

// MCPServerListRequest MCP Server list request.
type MCPServerListRequest struct {
	BusinessDomainID string `header:"x-business-domain" validate:"required"`                                     // Business domain ID.
	UserID           string `header:"user_id"`                                                                   // User ID, internal use.
	IsPublic         bool   `header:"is_public"`                                                                 // Is it a public interface?.
	Page             int    `form:"page" default:"1" validate:"min=1"`                                           // Page number.
	PageSize         int    `form:"page_size" default:"10" validate:"min=1,max=100"`                             // Number of items per page.
	SortBy           string `form:"sort_by" default:"update_time" validate:"oneof=update_time create_time name"` // sort field.
	SortOrder        string `form:"sort_order" default:"desc" validate:"oneof=asc desc"`                         // sort order.
	Name             string `form:"name"`                                                                        // MCP name.
	Source           string `form:"source"`                                                                      // Source.
	IsInternal       bool   `form:"is_internal"`                                                                 // Whether it is built-in.
	Category         string `form:"category"`                                                                    // Classification.
	Status           string `form:"status"`                                                                      // Status.
	CreateUser       string `form:"create_user"`                                                                 // Create user.
	All              bool   `form:"all"`                                                                         // Whether to return all information.
	Mode             string `form:"mode" validate:"omitempty,oneof=stdio_uv stdio_npx sse stream"`               // operating mode.
}

// MCPServerListResponse MCP Server list response.
type MCPServerListResponse struct {
	*ormhelper.QueryResult `json:",inline"`
	Data                   []*MCPServerConfigInfo `json:"data"` // Data list.
}

// MCPServerDetailRequest MCP Server details request.
type MCPServerDetailRequest struct {
	UserID   string `header:"user_id"`                 // User ID, internal use.
	IsPublic bool   `header:"is_public"`               // Is it a public interface?.
	ID       string `uri:"mcp_id" validate:"required"` // MCP Server ID
}

type MCPServerDetailResponse struct {
	BaseInfo       *MCPServerConfigInfo `json:"base_info"`       // MCP Server basic information.
	ConnectionInfo *MCPConnectionInfo   `json:"connection_info"` // MCP connection information.
}

// MCPServerReleaseListRequest MCP Server publish list request.
type MCPServerReleaseListRequest struct {
	MCPServerListRequest `json:",inline"`
	ReleaseUser          string `form:"release_user"` // Publisher.
}

// MCPServerReleaseListResponse MCP Server publish list response.
type MCPServerReleaseListResponse struct {
	MCPServerListResponse `json:",inline"`
}

type MCPServerReleaseDetailRequest struct {
	MCPServerDetailRequest `json:",inline"`
}

type MCPServerReleaseDetailResponse struct {
	MCPServerDetailResponse `json:",inline"`
}

var MCPFields = []string{
	"mcp_id",
	"name",
	"description",
	"source",
	"category",
	"mode",
	"is_internal",
	"create_user",
	"create_time",
	"update_user",
	"update_time",
	"release_time",
	"release_user",
}

// MCPServerReleaseBatchRequest MCP Server releases batch details request.
type MCPServerReleaseBatchRequest struct {
	UserID   string `header:"user_id"`                  // User ID, internal use.
	IsPublic bool   `header:"is_public"`                // Is it a public interface?.
	MCPIDs   string `uri:"mcp_ids" validate:"required"` // MCP Server ID list, multiple IDs separated by commas.
	Fields   string `uri:"fields" validate:"required"`  // Get MCP information field names: (can be combined in any combination, if multiple are obtained, separate them with commas)
}

// MCPServerUpdateRequest MCP Server update request.
type MCPServerUpdateRequest struct {
	UserID       string               `header:"user_id" validate:"required"`                                                           // User ID, internal use.
	IsPublic     bool                 `header:"is_public"`                                                                             // Is it public?.
	MCPID        string               `json:"mcp_id"`                                                                                  // MCP Server ID
	Name         string               `json:"name,omitempty"`                                                                          // MCP Server name.
	Description  string               `json:"description,omitempty"`                                                                   // Description information.
	CreationType MCPCreationType      `json:"creation_type" default:"custom" validate:"required,oneof=custom tool_imported"`           // Create type.
	Mode         MCPMode              `json:"mode,omitempty" default:"stream" validate:"required,oneof=stdio_uv stdio_npx sse stream"` // operating mode.
	URL          string               `json:"url,omitempty" validate:"omitempty,url"`                                                  // Service URL.
	Headers      map[string]string    `json:"headers,omitempty"`                                                                       // Request header.
	Command      string               `json:"command,omitempty"`                                                                       // Run command.
	Args         []string             `json:"args,omitempty"`                                                                          // Operating parameters.
	Env          map[string]string    `json:"env,omitempty"`                                                                           // environment variables.
	Source       string               `json:"source,omitempty" default:"custom"`                                                       // Source.
	Category     string               `json:"category,omitempty" default:"other_category"`                                             // Classification.
	ToolConfigs  []*MCPToolConfigInfo `json:"tool_configs"`                                                                            // Tool configuration.
}

// MCPServerUpdateResponse MCP Server update response.
type MCPServerUpdateResponse struct {
	MCPID  string    `json:"mcp_id"` // MCP Server ID
	Status BizStatus `json:"status"` // Status.
}

// UpdateMCPStatusRequest MCP Server status update request.
type UpdateMCPStatusRequest struct {
	UserID   string    `header:"user_id" validate:"required"`                                        // User ID, internal use.
	IsPublic bool      `header:"is_public"`                                                          // Is it public?.
	MCPID    string    `uri:"mcp_id" validate:"required"`                                            // MCP Server ID
	Status   BizStatus `json:"status" validate:"required,oneof=unpublish editing published offline"` // Status.
}

// UpdateMCPStatusResponse MCP Server status update response.
type UpdateMCPStatusResponse struct {
	MCPID  string    `json:"mcp_id"` // MCP Server ID
	Status BizStatus `json:"status"` // Status.
}

// MCPToolDebugRequest MCP tool debug request.
type MCPToolDebugRequest struct {
	UserID     string         `header:"user_id" validate:"required"` // User ID, internal use.
	IsPublic   bool           `header:"is_public"`                   // Is it public?.
	MCPID      string         `uri:"mcp_id" validate:"required"`     // MCP Server ID
	ToolName   string         `uri:"tool_name" validate:"required"`  // Tool name.
	Parameters map[string]any `json:"parameters"`                    // Tool request parameters.
}

// MCPToolDebugResponse MCP tool debug response.
type MCPToolDebugResponse struct {
	Content []mcp.Content `json:"content"`  // Tool call result content.
	IsError bool          `json:"is_error"` // Is it an error.
}

type MCPProxyToolListRequest struct {
	UserID string `header:"user_id"`                 // User ID, internal use.
	MCPID  string `uri:"mcp_id" validate:"required"` // MCP Server ID
}

type MCPProxyToolListResponse struct {
	Tools []mcp.Tool `json:"tools"` // Tools.
}

// MCPProxyCallToolRequest MCP tool call request.
type MCPProxyCallToolRequest struct {
	UserID     string         `header:"user_id" validate:"required"`  // User ID, internal use.
	MCPID      string         `uri:"mcp_id" validate:"required"`      // MCP Server ID
	ToolName   string         `json:"tool_name" validate:"required"`  // Tool name.
	Parameters map[string]any `json:"parameters" validate:"required"` // Tool request parameters.
}

// MCPProxyCallToolResponse MCP tool call response.
type MCPProxyCallToolResponse struct {
	Content []mcp.Content `json:"content"`  // Tool call result content.
	IsError bool          `json:"is_error"` // Is it an error.
}

// IMCPManageService MCP management interface.
type IMCPManageService interface {
	// ParseSSE parses SSE MCPServer.
	ParseSSE(ctx context.Context, req *MCPParseSSERequest) (*MCPParseSSEResponse, error)
	// AddMCPServer Register MCPServer.
	AddMCPServer(ctx context.Context, req *MCPServerAddRequest) (*MCPServerAddResponse, error)
	// DeleteMCPServer DeleteMCPServer.
	DeleteMCPServer(ctx context.Context, req *MCPServerDeleteRequest) error
	// List Get the MCPServer list.
	QueryPage(ctx context.Context, req *MCPServerListRequest) (*MCPServerListResponse, error)
	// Detail Get MCPServer details.
	GetDetail(ctx context.Context, req *MCPServerDetailRequest) (*MCPServerDetailResponse, error)
	// UpdateMCPServer EditMCPServer.
	UpdateMCPServer(ctx context.Context, req *MCPServerUpdateRequest) (*MCPServerUpdateResponse, error)
	// UpdateMCPStatus updates MCP Server status.
	UpdateMCPStatus(ctx context.Context, req *UpdateMCPStatusRequest) (*UpdateMCPStatusResponse, error)
	// DebugTool tool debugging.
	DebugTool(ctx context.Context, req *MCPToolDebugRequest) (*MCPToolDebugResponse, error)
}

// IMCPReleaseService MCP market interface.
type IMCPReleaseService interface {
	// getList Gets the MCP Server publishing list.
	QueryRelease(ctx context.Context, req *MCPServerReleaseListRequest) (*MCPServerReleaseListResponse, error)
	// getDetail Gets MCP Server release details.
	GetReleaseDetail(ctx context.Context, req *MCPServerReleaseDetailRequest) (*MCPServerReleaseDetailResponse, error)
	// QueryReleaseBatch obtains MCP Server release details in batches.
	QueryReleaseBatch(ctx context.Context, req *MCPServerReleaseBatchRequest) ([]map[string]any, error)
}

// IMCPExecuteService MCP proxy interface.
type IMCPExecuteService interface {
	// GetMCPTools Gets the MCP Server release list.
	GetMCPTools(ctx context.Context, req *MCPProxyToolListRequest) (*MCPProxyToolListResponse, error)
	// CallMCPTool calls MCP tool.
	CallMCPTool(ctx context.Context, req *MCPProxyCallToolRequest) (*MCPProxyCallToolResponse, error)
}

// IMCPService MCP service interface.
type IMCPService interface {
	// MCPManageService MCP management interface.
	IMCPManageService
	// MCPReleaseService MCP market interface.
	IMCPReleaseService
	// MCPExecuteService MCP proxy interface.
	IMCPExecuteService
	// IMCPImpexService MCP import and export interface.
	IMCPImpexService
	// UpgradeMCPInstance upgrade MCP Server instance.
	UpgradeMCPInstance(ctx context.Context, mcpID string) error
	// MCPInstancConfig MCP Server instance configuration interface.
	IMCPInstancConfig
	// IMCPToolExecutor MCP Tool Executor.
	IMCPToolExecutor
}

// IMCPImpexService MCP import and export interface.
type IMCPImpexService interface {
	// Import and export.
	// Impex[*MCPImpexData]
	Import(ctx context.Context, tx *sql.Tx, mode ImportType, data *ComponentImpexConfigModel, userID string) (err error)
	// Export export configuration.
	Export(ctx context.Context, req *ExportReq) (data *ComponentImpexConfigModel, err error)
}

// IMCPToolExecutor MCP Tool Executor.
type IMCPToolExecutor interface {
	ExecuteTool(ctx context.Context, mcpToolID string, params HTTPRequestParams) (*HTTPResponse, error)
}

// MCP instance related configuration interface.
type IMCPInstancConfig interface {
	// GetMCPInstanceConfig Gets the MCP Server instance configuration.
	GetMCPInstanceConfig(ctx context.Context, mcpID string, mode MCPMode) (*MCPInstancConfigInfo, error)
}

// MCPInstancConfigInfo MCP Server instance configuration information.
type MCPInstancConfigInfo struct {
	MCPID   string            // MCP Server ID
	URL     string            // MCP Server URL
	Headers map[string]string // MCP Server request header.
	Mode    MCPMode           // MCP Server operating mode.
	Version int               // MCP Server version (used in tool_imported scenario)
}
