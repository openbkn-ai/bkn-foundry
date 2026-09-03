// Package interfaces define interfaces.
// @file operator.go
// @description: Define operator operation interface.
package interfaces

//go:generate mockgen -source=logics_operator.go -destination=../mocks/operator.go -package=mocks
import (
	"context"
	"database/sql"
)

// OperatorStatusItem structure of a single status update item.
type OperatorStatusItem struct {
	OperatorID string    `json:"operator_id" validate:"required,uuid"`
	Status     BizStatus `json:"status" validate:"required,oneof=unpublish published offline editing"`
}

// OperatorStatusUpdateReq status update request.
type OperatorStatusUpdateReq struct {
	UserID      string                `header:"user_id" validate:"required"`
	StatusItems []*OperatorStatusItem `json:",inline"`
}

// OperatorDeleteItem single delete request.
type OperatorDeleteItem struct {
	OperatorID string `json:"operator_id" validate:"required,uuid"`
}

// OperatorDeleteReq delete request.
type OperatorDeleteReq []OperatorDeleteItem

// OperatorUpdateReq update request.
type OperatorUpdateReq struct {
	*OperatorRegisterReq `json:",inline"`
	OperatorID           string `json:"operator_id" form:"operator_id" validate:"required,uuid"`
}

// OperatorRegisterReq registration request.
type OperatorRegisterReq struct {
	MetadataType           MetadataType            `json:"operator_metadata_type" form:"operator_metadata_type" validate:"required" oneof:"openapi function"` // Operator metadata type (mandatory parameter)
	OperatorInfo           *OperatorInfo           `json:"operator_info" form:"operator_info"`                                                                // Operator information.
	OperatorExecuteControl *OperatorExecuteControl `json:"operator_execute_control" form:"operator_execute_control"`                                          // control parameters.
	ExtendInfo             map[string]interface{}  `json:"extend_info,omitempty" form:"extend_info,omitempty"`                                                // Expand information.
	UserToken              string                  `json:"user_token" form:"user_token"`                                                                      // Internal interface parameter passing.
	DirectPublish          bool                    `json:"direct_publish,omitempty" form:"direct_publish,omitempty"`                                          // publish directly.
	FunctionInput          *FunctionInput          `json:"function_input,omitempty" form:"function_input,omitempty"`                                          // Function input parameters.
	Data                   string                  `json:"data" form:"data"`                                                                                  // Operator metadata, required when the operator metadata type is openapi.
	Description            string                  `json:"description" form:"description"`                                                                    // Operator description.
}

// OperatorRegisterResp Single operator registration result.
type OperatorRegisterResp struct {
	Status     ResultStatus `json:"status"`          // Operator registration status (failed/success)
	OperatorID string       `json:"operator_id"`     // Operator ID.
	Version    string       `json:"version"`         // Operator version.
	Error      error        `json:"error,omitempty"` // Error message (requires support for internationalization)
}

// OperatorEditReq Edit request.
type OperatorEditReq struct {
	UserID                 string                  `header:"user_id" validate:"required"` // User ID.
	Name                   string                  `json:"name" form:"name"`
	Description            string                  `json:"description" form:"description"`                                       // Operator description.
	OperatorID             string                  `json:"operator_id" form:"operator_id" validate:"required,uuid"`              // Operator ID.
	OperatorInfoEdit       *OperatorInfoEdit       `json:"operator_info" form:"operator_info"`                                   // Operator information.
	OperatorExecuteControl *OperatorExecuteControl `json:"operator_execute_control" form:"operator_execute_control"`             // executive control.
	ExtendInfo             map[string]interface{}  `json:"extend_info,omitempty" form:"extend_info,omitempty"`                   // Extended information.
	MetadataType           MetadataType            `json:"metadata_type" form:"metadata_type" validate:"oneof=openapi function"` // Metadata type (optional parameter)
	FunctionInputEdit      *FunctionInputEdit      `json:"function_input,omitempty" form:"function_input,omitempty"`             // Function input parameters.
	*OpenAPIInput          `json:",inline"`
}

// OperatorInfoEdit Operator information editing.
type OperatorInfoEdit struct {
	Type          OperatorType  `json:"operator_type" default:"basic" validate:"oneof=basic composite"` // Operator type (basic/composite)
	ExecutionMode ExecutionMode `json:"execution_mode" default:"sync"  validate:"oneof=sync async"`     // Execution mode (async/sync)
	Category      BizCategory   `json:"category" default:"other_category"`                              // Operator classification (data_process/control)
	Source        string        `json:"source" default:"unknown"`                                       // Operator source (system/unknown)
	IsDataSource  *bool         `json:"is_data_source" form:"is_data_source" default:"false"`           // Whether it is a data source operator.
}

// OperatorEditResp edit response.
type OperatorEditResp struct {
	OperatorID string    `json:"operator_id" validate:"required,uuid"`
	Version    string    `json:"version" validate:"required,uuid"`
	Status     BizStatus `json:"status" validate:"required,oneof=unpublish published offline editing"` // validate:"oneof=asc desc"
}

// OperatorDataInfo operator data.
type OperatorDataInfo struct {
	Name                   string                  `json:"name"` // Operator name.
	OperatorID             string                  `json:"operator_id" validate:"uuid"`
	Version                string                  `json:"version" validate:"uuid"`
	Status                 BizStatus               `json:"status" validate:"omitempty,oneof=unpublish published offline editing"` // Status.
	MetadataType           MetadataType            `json:"metadata_type" default:"openapi" validate:"oneof=openapi function"`     // Operator metadata type (mandatory parameter)
	Metadata               *MetadataInfo           `json:"metadata"`
	ExtendInfo             map[string]interface{}  `json:"extend_info,omitempty"`
	OperatorInfo           *OperatorInfo           `json:"operator_info"` // Operator information.
	OperatorExecuteControl *OperatorExecuteControl `json:"operator_execute_control"`
	CreateUser             string                  `json:"create_user"`
	CreateTime             int64                   `json:"create_time"`
	UpdateUser             string                  `json:"update_user"`
	UpdateTime             int64                   `json:"update_time"`
	ReleaseUser            string                  `json:"release_user,omitempty"` // Posted by.
	ReleaseTime            int64                   `json:"release_time,omitempty"` // Release time.
	Tag                    int                     `json:"tag,omitempty"`          // version number.
	IsInternal             bool                    `json:"is_internal"`            // Is it an internal operator?.
}

// OperatorType operator type.
type OperatorType string

const (
	OperatorTypeBase      OperatorType = "basic"     // Basic operators.
	OperatorTypeComposite OperatorType = "composite" // Combinatorial operator.
)

// OperatorExecuteControl operator execution control.
type OperatorExecuteControl struct {
	Timeout     int64               `json:"timeout" form:"timeout" default:"3000"` // timeout.
	RetryPolicy OperatorRetryPolicy `json:"retry_policy" form:"retry_policy"`      // Retry strategy.
}

// OperatorRetryPolicy operator retry policy.
type OperatorRetryPolicy struct {
	MaxAttempts     int64           `json:"max_attempts" form:"max_attempts" default:"3"`      // Maximum number of retries.
	InitialDelay    int64           `json:"initial_delay" form:"initial_delay" default:"1000"` // Initial delay time (milliseconds)
	BackoffFactor   int64           `json:"backoff_factor" form:"backoff_factor" default:"2"`  // exponential backoff factor.
	MaxDelay        int64           `json:"max_delay" form:"max_delay" default:"6000"`         // Maximum delay time (milliseconds)
	RetryConditions RetryConditions `json:"retry_conditions" form:"retry_conditions"`          // Retry condition.
}

// RetryConditions retry conditions.
type RetryConditions struct {
	StatusCode []int64  `json:"status_code" form:"status_code"` // status code.
	ErrorCodes []string `json:"error_codes" form:"error_codes"` // Business error code.
}

// OperatorInfo operator information.
type OperatorInfo struct {
	Type          OperatorType  `json:"operator_type" form:"operator_type" default:"basic" validate:"oneof=basic composite"` // Operator type (basic/composite)
	ExecutionMode ExecutionMode `json:"execution_mode" form:"execution_mode"  default:"sync"  validate:"oneof=sync async"`   // Execution mode (async/sync)
	Category      BizCategory   `json:"category" form:"category" default:"other_category"`                                   // Operator classification (data_process/control)
	CategoryName  string        `json:"category_name,omitempty" form:"category_name,omitempty"`                              // Operator classification name (supports internationalization)
	Source        string        `json:"source" form:"source" default:"unknown"`                                              // Operator source (system/unknown)
	IsDataSource  *bool         `json:"is_data_source" form:"is_data_source" default:"false"`                                // Whether it is a data source operator.
}

// PageQueryRequest paging query request.
type PageQueryRequest struct {
	UserID       string       `header:"user_id"`
	Page         int          `form:"page" default:"1" validate:"min=1"`
	PageSize     int          `form:"page_size" default:"10" validate:"max=100"`
	SortBy       string       `form:"sort_by" default:"update_time" validate:"oneof=update_time create_time name"`
	SortOrder    string       `form:"sort_order" default:"desc" validate:"oneof=asc desc"`
	Name         string       `form:"name"`
	Status       BizStatus    `form:"status" validate:"omitempty,oneof=unpublish published offline editing"`
	CreateUser   string       `form:"create_user"`                                              // Creator.
	Category     BizCategory  `form:"category"`                                                 // Classification.
	OperatorType OperatorType `form:"operator_type" validate:"omitempty,oneof=basic composite"` // Operator type (basic/composite)
	All          bool         `form:"all"`
	IsDataSource *bool        `form:"is_data_source"` // Whether it is a data source operator.
}

// PageQueryResponse Pagination query response.
type PageQueryResponse struct {
	CommonPageResult `json:",inline"`
	Data             []*OperatorDataInfo `json:"data"` // Data list.
}

// OperatorHistoryDetailReq query operation history details request.
type OperatorHistoryDetailReq struct {
	UserID     string `header:"user_id"` // Optional.
	OperatorID string `uri:"operator_id" validate:"required"`
	Version    string `uri:"version" validate:"required"`
	Tag        int    `form:"tag"`
}

// OperatorMarketDetailReq Operator market details query request.
type OperatorMarketDetailReq struct {
	UserID     string `header:"user_id"` // Optional.
	OperatorID string `uri:"operator_id" validate:"required"`
}

// DebugOperatorReq debugging request.
type DebugOperatorReq struct {
	UserID            string `header:"user_id" validate:"required"` // User ID, internal use.
	OperatorID        string `json:"operator_id" validate:"required,uuid"`
	Version           string `json:"version" validate:"required,uuid"`
	Timeout           int    `json:"timeout"` // Timeout time in seconds.
	HTTPRequestParams `json:",inline"`
}

// ExecuteOperatorReq executes the request.
type ExecuteOperatorReq struct {
	UserID            string `header:"user_id" validate:"required"`       // User ID, internal use.
	OperatorID        string `uri:"operator_id" validate:"required,uuid"` // Operator ID.
	Timeout           int    `json:"timeout"`                             // Timeout time in seconds.
	HTTPRequestParams `json:",inline"`
}

// OperatorHistoryListReq gets the historical version list.
type OperatorHistoryListReq struct {
	UserID     string `header:"user_id"` // Optional.
	OperatorID string `uri:"operator_id" validate:"required"`
}

// PageQueryOperatorMarketReq Operator market query request.
type PageQueryOperatorMarketReq struct {
	UserID        string        `header:"user_id"` // Optional.
	Page          int           `form:"page" default:"1" validate:"min=1"`
	PageSize      int           `form:"page_size" default:"10" validate:"max=100"`
	SortBy        string        `form:"sort_by" default:"update_time" validate:"oneof=update_time create_time name"`
	SortOrder     string        `form:"sort_order" default:"desc" validate:"oneof=asc desc"`
	All           bool          `form:"all"`
	Status        BizStatus     `form:"status" validate:"omitempty,oneof=published offline"`       // Status.
	Name          string        `form:"name"`                                                      // Operator name.
	CreateUser    string        `form:"create_user"`                                               // Creator.
	ReleaseUser   string        `form:"release_user"`                                              // Posted by.
	Category      BizCategory   `form:"category"`                                                  // Classification.
	OperatorType  OperatorType  `form:"operator_type" validate:"omitempty,oneof=basic composite"`  // Operator type (basic/composite)
	IsDataSource  *bool         `form:"is_data_source"`                                            // Whether it is a data source operator.
	ExecutionMode ExecutionMode `form:"execution_mode" validate:"omitempty,oneof=sync async"`      // Execution mode (async/sync)
	MetadataType  MetadataType  `form:"metadata_type" validate:"omitempty,oneof=openapi function"` // Metadata type (openapi/function)
}

// GetOperatorInfoByOperatorIDReq Get operator information request.
type GetOperatorInfoByOperatorIDReq struct {
	UserID     string `header:"user_id"` // Optional.
	OperatorID string `uri:"operator_id" validate:"required,uuid"`
}

// OperatorManager operator management interface.
type OperatorManager interface {
	RegisterOperatorByOpenAPI(ctx context.Context, req *OperatorRegisterReq, userID string) ([]*OperatorRegisterResp, error)
	// UpdateOperatorStatus updates operator status.
	UpdateOperatorStatus(ctx context.Context, req *OperatorStatusUpdateReq, userID string) error
	// GetOperatorInfoByOperatorID Get operator information.
	GetOperatorInfoByOperatorID(ctx context.Context, req *GetOperatorInfoByOperatorIDReq) (*OperatorDataInfo, error)
	GetOperatorQueryPage(ctx context.Context, req *PageQueryRequest) (*PageQueryResponse, error)
	// GetOperatorNamesByIDs batches names based on operator IDs (fault tolerance: non-existent IDs are ignored)
	GetOperatorNamesByIDs(ctx context.Context, ids []string) (*BatchNamesResp, error)
	// EditOperator editing operator.
	EditOperator(ctx context.Context, req *OperatorEditReq) (*OperatorEditResp, error)
	// DeleteOperator delete operator.
	DeleteOperator(ctx context.Context, req OperatorDeleteReq, userID string) error
	UpdateOperatorByOpenAPI(ctx context.Context, req *OperatorUpdateReq, userID string) (resultList []*OperatorRegisterResp, err error)
	// Debug interface.
	DebugOperator(ctx context.Context, req *DebugOperatorReq) (resp *HTTPResponse, err error)
	// Execution operator.
	ExecuteOperator(ctx context.Context, req *ExecuteOperatorReq) (resp *HTTPResponse, err error)
	// More ID, version to obtain the published version operator information.
	QueryOperatorHistoryDetail(ctx context.Context, req *OperatorHistoryDetailReq) (*OperatorDataInfo, error)
	QueryOperatorHistoryList(ctx context.Context, req *OperatorHistoryListReq) ([]*OperatorDataInfo, error)
	// QueryOperatorMarketList Operator market query.
	QueryOperatorMarketList(ctx context.Context, req *PageQueryOperatorMarketReq) (*PageQueryResponse, error)
	// QueryOperatorMarketDetail Operator market details query.
	QueryOperatorMarketDetail(ctx context.Context, req *OperatorMarketDetailReq) (*OperatorDataInfo, error)
	// Import and export.
	// Impex[*OperatorImpexData]
	Export(ctx context.Context, req *ExportReq) (data *ComponentImpexConfigModel, err error)
	Import(ctx context.Context, tx *sql.Tx, mode ImportType, data *OperatorImpexConfig, userID string) (err error)
	// Internal operating interface.
	InternalOperatorManager
}

// CheckAddAsToolResp checks whether the operator is allowed to be added as a tool response.
type CheckAddAsToolResp struct {
	OperatorID string      `json:"operator_id"`
	Name       string      `json:"name"`
	Metadata   IMetadataDB `json:"metadata"`
}

// InternalOperatorManager internal operation interface.
type InternalOperatorManager interface {
	// Check if adding as a tool is allowed.
	CheckAddAsTool(ctx context.Context, operatorID, userID string) (resp *CheckAddAsToolResp, err error)
}
