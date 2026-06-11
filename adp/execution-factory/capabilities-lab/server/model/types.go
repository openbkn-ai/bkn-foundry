package model

type Group struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ServiceURL   string `json:"service_url,omitempty"`
	Status       string `json:"status,omitempty"`
	Category     string `json:"category,omitempty"`
	ToolCount    int    `json:"tool_count,omitempty"`
	UpdateTime   int64  `json:"update_time,omitempty"`
	MetadataType string `json:"metadata_type,omitempty"`
}

type Endpoint struct {
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
}

type Orchestration struct {
	Enabled      bool   `json:"enabled"`
	OperatorID   string `json:"operator_id,omitempty"`
	OperatorName string `json:"operator_name,omitempty"`
	Audit        *Audit `json:"audit,omitempty"`
}

type Audit struct {
	CreateUser  string `json:"create_user,omitempty"`
	CreateTime  int64  `json:"create_time,omitempty"`
	UpdateUser  string `json:"update_user,omitempty"`
	UpdateTime  int64  `json:"update_time,omitempty"`
	ReleaseUser string `json:"release_user,omitempty"`
	ReleaseTime int64  `json:"release_time,omitempty"`
}

type Capability struct {
	ID            string                 `json:"id"`
	Kind          string                 `json:"kind"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description,omitempty"`
	Status        string                 `json:"status"`
	Group         *Group                 `json:"group,omitempty"`
	Endpoint      *Endpoint              `json:"endpoint,omitempty"`
	Orchestration *Orchestration         `json:"orchestration,omitempty"`
	Audit         *Audit                 `json:"audit,omitempty"`
	UpdateTime    int64                  `json:"update_time,omitempty"`
	ToolID        string                 `json:"tool_id,omitempty"`
	BoxID         string                 `json:"box_id,omitempty"`
	McpID         string                 `json:"mcp_id,omitempty"`
	SkillID       string                 `json:"skill_id,omitempty"`
	Version       string                 `json:"version,omitempty"`
	OpenAPISpec   string                 `json:"openapi_spec,omitempty"`
	URL           string                 `json:"url,omitempty"`
	Code          string                 `json:"code,omitempty"`
	Inputs        []FunctionParameterDef `json:"inputs,omitempty"`
	Outputs       []FunctionParameterDef `json:"outputs,omitempty"`
}

type CapabilityListResponse struct {
	Data     []Capability `json:"data"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

type GroupListResponse struct {
	Data     []Group `json:"data"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

type GroupInput struct {
	Mode  string `json:"mode"` // auto | existing | new
	BoxID string `json:"box_id,omitempty"`
	Name  string `json:"name,omitempty"`
}

type CreateHttpCapabilityRequest struct {
	OpenAPISpec          string     `json:"openapi_spec" binding:"required"`
	ServiceURL           string     `json:"service_url" binding:"required"`
	Name                 string     `json:"name,omitempty"`
	Description          string     `json:"description,omitempty"`
	Category             string     `json:"category,omitempty"`
	Group                GroupInput `json:"group"`
	OrchestrationEnabled bool       `json:"orchestration_enabled"`
}

type ImportOpenApiCapabilityRequest struct {
	OpenAPISpec          string     `json:"openapi_spec" binding:"required"`
	ServiceURL           string     `json:"service_url" binding:"required"`
	Description          string     `json:"description,omitempty"`
	Category             string     `json:"category,omitempty"`
	Group                GroupInput `json:"group"`
	OrchestrationEnabled bool       `json:"orchestration_enabled"`
}

type CreateHttpCapabilityResponse struct {
	Capability Capability `json:"capability"`
	Links      []Link     `json:"links,omitempty"`
}

type ImportOpenApiCapabilityResponse struct {
	BoxID        string       `json:"box_id"`
	Capabilities []Capability `json:"capabilities"`
	Links        []Link       `json:"links,omitempty"`
	FailureCount int64        `json:"failure_count,omitempty"`
	Failures     []string     `json:"failures,omitempty"`
}

type Link struct {
	OperatorID string `json:"operator_id"`
	ToolID     string `json:"tool_id"`
}

type VersionEntry struct {
	Version     string `json:"version"`
	Status      string `json:"status,omitempty"`
	ReleaseUser string `json:"release_user,omitempty"`
	ReleaseTime int64  `json:"release_time,omitempty"`
	UpdateTime  int64  `json:"update_time,omitempty"`
}

type VersionListResponse struct {
	Kind     string         `json:"kind"`
	Versions []VersionEntry `json:"versions"`
}

type RepublishVersionRequest struct {
	Version string `json:"version" binding:"required"`
	Mode    string `json:"mode"` // publish | republish (skill only)
}

type DebugCapabilityRequest struct {
	Body     map[string]interface{} `json:"body,omitempty"`
	Query    map[string]interface{} `json:"query,omitempty"`
	Path     map[string]interface{} `json:"path,omitempty"`
	Header   map[string]interface{} `json:"header,omitempty"`
	ToolName string                 `json:"tool_name,omitempty"` // MCP debug
	Timeout  int                    `json:"timeout,omitempty"`
}

type DebugCapabilityResponse struct {
	StatusCode int                    `json:"status_code,omitempty"`
	Body       interface{}            `json:"body,omitempty"`
	DurationMs int64                  `json:"duration_ms,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Content    string                 `json:"content,omitempty"`
	IsError    bool                   `json:"is_error,omitempty"`
	Headers    map[string]interface{} `json:"headers,omitempty"`
}

type PublishCapabilityRequest struct {
	Status string `json:"status"` // published | offline | unpublish
}

type EnableOrchestrationResponse struct {
	OperatorID string `json:"operator_id"`
	Audit      *Audit `json:"audit,omitempty"`
}

type OperatorRetryConditions struct {
	StatusCode []int    `json:"status_code,omitempty"`
	ErrorCodes []string `json:"error_codes,omitempty"`
}

type OperatorRetryPolicy struct {
	MaxAttempts     int64                   `json:"max_attempts,omitempty"`
	InitialDelay    int64                   `json:"initial_delay,omitempty"`
	MaxDelay        int64                   `json:"max_delay,omitempty"`
	BackoffFactor   int64                   `json:"backoff_factor,omitempty"`
	RetryConditions OperatorRetryConditions `json:"retry_conditions,omitempty"`
}

type OperatorExecuteControl struct {
	Timeout     int64               `json:"timeout,omitempty"`
	RetryPolicy OperatorRetryPolicy `json:"retry_policy,omitempty"`
}

type EnableOrchestrationRequest struct {
	OperatorExecuteControl OperatorExecuteControl `json:"operator_execute_control,omitempty"`
}

type UpdateOrchestrationConfigRequest = EnableOrchestrationRequest

type DisableOrchestrationResponse struct {
	Enabled    bool   `json:"enabled"`
	OperatorID string `json:"operator_id,omitempty"`
}

type OrchestrationDetailResponse struct {
	Enabled    bool   `json:"enabled"`
	OperatorID string `json:"operator_id,omitempty"`
	ToolID     string `json:"tool_id,omitempty"`
	BoxID      string `json:"box_id,omitempty"`
	Audit      *Audit `json:"audit,omitempty"`
}

type UpdateHttpCapabilityRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	OpenAPISpec string `json:"openapi_spec,omitempty"`
}

type UpdateCapabilityRequest struct {
	Name         string                 `json:"name,omitempty"`
	Description  string                 `json:"description,omitempty"`
	OpenAPISpec  string                 `json:"openapi_spec,omitempty"`
	URL          string                 `json:"url,omitempty"`
	Mode         string                 `json:"mode,omitempty"`
	Headers      map[string]string      `json:"headers,omitempty"`
	Category     string                 `json:"category,omitempty"`
	CreationType string                 `json:"creation_type,omitempty"`
	Source       string                 `json:"source,omitempty"`
	Code         string                 `json:"code,omitempty"`
	Inputs       []FunctionParameterDef `json:"inputs,omitempty"`
	Outputs      []FunctionParameterDef `json:"outputs,omitempty"`
}

type CategoryListResponse struct {
	Data []CategoryEntry `json:"data"`
}

type CategoryEntry struct {
	CategoryType string `json:"category_type"`
	Name         string `json:"name"`
}

type RegisterMcpCapabilityRequest struct {
	Name         string            `json:"name" binding:"required"`
	Description  string            `json:"description,omitempty"`
	Mode         string            `json:"mode"` // sse | stream
	URL          string            `json:"url" binding:"required"`
	Headers      map[string]string `json:"headers,omitempty"`
	Category     string            `json:"category,omitempty"`
	CreationType string            `json:"creation_type,omitempty"`
}

type RegisterSkillCapabilityRequest struct {
	FileType string
	Category string
	Source   string
	Filename string
	Content  []byte
	MimeType string
}
