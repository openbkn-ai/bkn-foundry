package interfaces

import (
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultPageSize default page size.
	DefaultPageSize = 10
	// DefaultPage default page number.
	DefaultPage = 1
	// MaxPageSize maximum page size.
	MaxPageSize = 1000

	// OSSGatewayPrefix OSS gateway skill asset prefix.
	OSSGatewayPrefix = "execution-factory"
)

var (
	// Current service configuration.
	AOIServerURL = "http://agent-operator-integration:9000"
	// AOPInternalV1Prefix AOP internal V1 prefix.
	AOPInternalV1Prefix = "/api/agent-operator-integration/internal-v1"
)

// SetAOIFuncExecPath gets the function execution path.
func SetAOIFuncExecPath(version string) string {
	return strings.ReplaceAll(GetAOIFuncExecPath(), ":version", version)
} // GetAOIFuncExecPath gets the function execution path.
func GetAOIFuncExecPath() string {
	return fmt.Sprintf("%s/function/exec/:version", AOPInternalV1Prefix)
}

// CommonPageResult Common paging return results.
type CommonPageResult struct {
	TotalCount int  `json:"total"`       // Total number of records.
	Page       int  `json:"page"`        // Current page number.
	PageSize   int  `json:"page_size"`   // page size.
	TotalPage  int  `json:"total_pages"` // Total pages.
	HasNext    bool `json:"has_next"`    // Is there a next page?.
	HasPrev    bool `json:"has_prev"`    // Is there a previous page?.
}

// BatchNamesReq Batch name request by ID (operator/tool_box/skill unified contract)
type BatchNamesReq struct {
	IDs []string `json:"ids"` // A list of IDs to be named. An empty list returns empty entries.
}

// NameEntry single ID->name entry.
type NameEntry struct {
	ID   string `json:"id"`   // Entity ID.
	Name string `json:"name"` // Entity name.
}

// BatchNamesResp Batch name response by ID.
// Fault tolerance: IDs that do not exist are ignored and no error is reported.
type BatchNamesResp struct {
	Entries []*NameEntry `json:"entries"`
}

// PtrBizIdentifiable business ID identifiable interface pointer.
type PtrBizIdentifiable[T any] interface {
	*T
	GetBizID() string // Get business ID.
}

// QueryResponse general query response structure.
type QueryResponse[T any] struct {
	CommonPageResult `json:",inline"`
	Data             []*T `json:"data"` // Data list.
}

type ResultStatus string

const (
	ResultStatusFailed  ResultStatus = "failed"
	ResultStatusSuccess ResultStatus = "success"
)

// MetadataType metadata type.
type MetadataType string

const (
	// MetadataTypeAPI API source data type.
	MetadataTypeAPI MetadataType = "openapi"
	// MetadataTypeFunc function source data type.
	MetadataTypeFunc MetadataType = "function"
)

// ExecutionMode execution mode.
type ExecutionMode string

func (e ExecutionMode) String() string {
	return string(e)
}

const (
	ExecutionModeSync   ExecutionMode = "sync"   // Synchronous execution.
	ExecutionModeAsync  ExecutionMode = "async"  // Asynchronous execution.
	ExecutionModeStream ExecutionMode = "stream" // Streaming execution.
)

// StreamingMode defines the streaming type.
type StreamingMode string

const (
	StreamingModeSSE  StreamingMode = "sse"
	StreamingModeHTTP StreamingMode = "http"
)

// HTTPRequest API request.
type HTTPRequest struct {
	ClientID          string        `json:"client_id"` // Client ID.
	Timeout           time.Duration `json:"timeout" validate:"gte=0"`
	ExecutionMode     ExecutionMode `json:"execution_mode" validate:"required,oneof=sync async stream"`
	HTTPRouter        `json:",inline"`
	HTTPRequestParams `json:",inline"`
}

// HTTPRouter HTTP routing.
type HTTPRouter struct {
	URL    string `json:"url" validate:"required"`
	Method string `json:"method" validate:"required"`
}

// APIRouter API routing.
// @description: API routing.
type APIRouter struct {
	ServerURL  string `json:"server_url" validate:"required"` // Server URL.
	HTTPRouter `json:",inline"`
}

// HTTPRequestParams HTTP request parameters.
type HTTPRequestParams struct {
	Headers     map[string]any    `json:"header"`
	Body        interface{}       `json:"body"`
	QueryParams map[string]any    `json:"query"`
	PathParams  map[string]string `json:"path"`
}

// FuncRequestParams function request parameters.
type FuncRequestParams struct {
	InputParams map[string]any `json:"inputs,omitempty" form:"inputs"` // Input parameter list.
	Code        string         `json:"code"`                           // function code.
}

// HTTPResponse API response.
type HTTPResponse struct {
	StatusCode int            `json:"status_code"` // status code.
	Headers    map[string]any `json:"headers"`     // response header.
	Body       interface{}    `json:"body"`        // response body.
	Error      string         `json:"error"`       // error message.
	Duration   int64          `json:"duration_ms"` // response time.
}

// BizStatus status.
type BizStatus string

func (b BizStatus) String() string {
	return string(b)
}

const (
	BizStatusUnpublish BizStatus = "unpublish" // Unpublished.
	BizStatusPublished BizStatus = "published" // Published.
	BizStatusOffline   BizStatus = "offline"   // Removed.
	BizStatusEditing   BizStatus = "editing"   // Published and editing.
)

// OutboxMessageReq message event request.
type OutboxMessageReq struct {
	EventID   string                 `json:"event_id"`
	EventType OutboxMessageEventType `json:"event_type" validate:"required"`
	Topic     string                 `json:"topic" validate:"required"`
	Payload   string                 `json:"payload" validate:"required"`
}

// OutboxMessageEventType message event type.
type OutboxMessageEventType string

// String Returns a string.
func (eventType OutboxMessageEventType) String() string {
	return string(eventType)
}

const (
	OutboxMessageEventTypeAuditLog OutboxMessageEventType = "audit_log" // Audit log.
)
