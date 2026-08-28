package interfaces

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source=logics_toolbox.go -destination=../mocks/toolbox.go -package=mocks

// ToolStatusType tool status type.
type ToolStatusType string

func (t ToolStatusType) String() string {
	return string(t)
}

const (
	ToolStatusTypeDisabled ToolStatusType = "disabled" // Disable.
	ToolStatusTypeEnabled  ToolStatusType = "enabled"  // enable.
)

// UpsertInternalToolBoxReq built-in toolbox registration request.
type UpsertInternalToolBoxReq struct {
	BoxID    string      `json:"box_id" validate:"required"`                                // Unique ID, registered by the business party to ensure uniqueness.
	BoxName  string      `json:"box_name" validate:"required"`                              // Toolbox name.
	Desc     string      `json:"box_desc" validate:"required"`                              // Toolbox description.
	Category BizCategory `json:"box_category" form:"box_category" default:"other_category"` // Classification.
}

// CreateToolBoxReq New toolbox request.
type CreateToolBoxReq struct {
	UserID        string       `header:"user_id" validate:"required"`                                                 // User ID, internal use.
	BoxName       string       `json:"box_name" form:"box_name"`                                                      // Toolbox name.
	BoxDesc       string       `json:"box_desc" form:"box_desc"`                                                      // Toolbox description.
	BoxSvcURL     string       `json:"box_svc_url" form:"box_svc_url"`                                                // Toolbox service address.
	Category      BizCategory  `json:"box_category" form:"box_category" default:"other_category"`                     // Classification.
	MetadataType  MetadataType `json:"metadata_type" form:"metadata_type" validate:"required,oneof=openapi function"` // Metadata type (mandatory parameter)
	Source        string       `json:"source" form:"source" default:"custom"`                                         // Toolbox source (default custom)
	*OpenAPIInput `json:",inline"`
}

// CreateToolBoxResp New tool returns results.
type CreateToolBoxResp struct {
	BoxID string `json:"box_id"` // Toolbox ID.
}

// UpdateToolBoxReq update toolbox request.
type UpdateToolBoxReq struct {
	UserID    string      `header:"user_id" validate:"required"`                                                 // User ID, internal use.
	BoxID     string      `uri:"box_id" validate:"required"`                                                     // Toolbox ID.
	BoxName   string      `json:"box_name" form:"box_name" validate:"required"`                                  // Toolbox name.
	BoxDesc   string      `json:"box_desc" form:"box_desc" validate:"required"`                                  // Toolbox description.
	BoxSvcURL string      `json:"box_svc_url" form:"box_svc_url"`                                                // Toolbox service address (required when metadata_type is openapi)
	Category  BizCategory `json:"box_category" form:"box_category" default:"other_category" validate:"required"` // Classification.
	// Metadata type (optional parameter). The type will not change after the toolbox is built, and the editing request does not need to be included;
	// Verify the value only after it is provided, otherwise the "optional" comment conflicts with oneof, and if it is not passed, it will be judged as an illegal value.
	MetadataType  MetadataType `json:"metadata_type" form:"metadata_type" validate:"omitempty,oneof=openapi function"`
	*OpenAPIInput `json:",inline"`
}

// UpdateToolBoxResp Update toolbox returns results.
type UpdateToolBoxResp struct {
	BoxID     string          `json:"box_id"`     // Toolbox ID.
	EditTools []*EditToolInfo `json:"edit_tools"` // Tool list under toolbox.
}

// EditToolInfo Basic information about the edited tool.
type EditToolInfo struct {
	ToolID string         `json:"tool_id"`     // Tool ID.
	Status ToolStatusType `json:"status"`      // tool status.
	Name   string         `json:"name"`        // Tool name.
	Desc   string         `json:"description"` // Tool description.
}

// ToolBoxToolInfo toolbox information.
type ToolBoxToolInfo struct {
	MetadataType MetadataType `json:"metadata_type" validate:"required,oneof=openapi function"` // metadata type.
	BoxID        string       `json:"box_id"`                                                   // Toolbox ID.
	BoxName      string       `json:"box_name"`                                                 // Toolbox name.
	BoxDesc      string       `json:"box_desc"`                                                 // Toolbox description.
	Status       BizStatus    `json:"status" validate:"oneof=unpublish published offline"`      // toolbox status.
	BoxSvcURL    string       `json:"box_svc_url"`                                              // Toolbox service address.
	CategoryType string       `json:"category_type"`                                            // Classification.
	CategoryName string       `json:"category_name"`                                            // Category name.
	IsInternal   bool         `json:"is_internal"`                                              // Is it an internal toolbox?.
	Source       string       `json:"source" default:"custom" validate:"oneof=custom internal"` // Toolbox source.
	Tools        []*ToolInfo  `json:"tools"`                                                    // Tool list under toolbox.
	CreateTime   int64        `json:"create_time"`                                              // creation time.
	UpdateTime   int64        `json:"update_time"`                                              // Update time.
	CreateUser   string       `json:"create_user"`                                              // Create user.
	UpdateUser   string       `json:"update_user"`                                              // Update user.
	ReleaseUser  string       `json:"release_user,omitempty"`                                   // Posted by.
	ReleaseTime  int64        `json:"release_time,omitempty"`                                   // Release time.
}

// ToolInfo tool information.
type ToolInfo struct {
	ToolID           string                 `json:"tool_id"`                                                                    // Tool ID.
	Name             string                 `json:"name"`                                                                       // Tool name.
	Description      string                 `json:"description"`                                                                // Tool description.
	Status           ToolStatusType         `json:"status" default:"disabled" validate:"oneof=disabled enabled"`                // tool status.
	MetadataType     MetadataType           `json:"metadata_type" default:"openapi" validate:"required,oneof=openapi function"` // metadata type.
	Metadata         *MetadataInfo          `json:"metadata"`                                                                   // metadata.
	UseRule          string                 `json:"use_rule"`                                                                   // Usage rules.
	GlobalParameters *ParametersStruct      `json:"global_parameters"`                                                          // global parameters.
	CreateTime       int64                  `json:"create_time"`                                                                // creation time.
	UpdateTime       int64                  `json:"update_time"`                                                                // Update time.
	CreateUser       string                 `json:"create_user"`                                                                // Create user.
	UpdateUser       string                 `json:"update_user"`                                                                // Update user.
	ExtendInfo       map[string]interface{} `json:"extend_info"`                                                                // Extended information.
	// Resource type.
	ResourceObject ResourceObjectType `json:"resource_object"` // Resource type.
}

// GetToolBoxReq Get toolbox request.
type GetToolBoxReq struct {
	UserID   string `header:"user_id"`                 // User ID, internal use.
	BoxID    string `uri:"box_id" validate:"required"` // Toolbox ID.
	IsPublic bool   `header:"is_public"`               // Whether to expose the interface.
}

// DeleteBoxReq delete toolbox request.
type DeleteBoxReq struct {
	UserID string `header:"user_id" validate:"required"` // User ID, internal use.
	BoxID  string `uri:"box_id" validate:"required"`
}

// DeleteBoxResp Delete toolbox returns results.
type DeleteBoxResp struct {
	BoxID string `json:"box_id"` // Toolbox ID.
}

// QueryToolBoxListReq Get toolbox list request.
type QueryToolBoxListReq struct {
	UserID      string      `header:"user_id"`                                                     // User ID, internal use.
	IsPublic    bool        `header:"is_public"`                                                   // Whether to expose the interface.
	CreateUser  string      `form:"create_user"`                                                   // Creator.
	ReleaseUser string      `form:"release_user"`                                                  // Posted by.
	BoxCategory BizCategory `form:"category"`                                                      // Classification.
	Status      BizStatus   `form:"status" validate:"omitempty,oneof=unpublish published offline"` // toolbox status.
	BoxName     string      `form:"name"`                                                          // Toolbox name.
	// Filter by metadata type. There are only two types of toolboxes: openapi and function. Accordingly, the function tool workbench only lists function toolboxes.
	MetadataType MetadataType `form:"metadata_type" validate:"omitempty,oneof=openapi function"`
	CommonPageParams
}

// QueryMarketToolBoxListReq Query market toolbox list request.
type QueryMarketToolBoxListReq struct {
	UserID      string      `header:"user_id"`    // User ID, internal use.
	IsPublic    bool        `header:"is_public"`  // Whether to expose the interface.
	CreateUser  string      `form:"create_user"`  // Creator.
	ReleaseUser string      `form:"release_user"` // Posted by.
	BoxCategory BizCategory `form:"category"`     // Classification.
	BoxName     string      `form:"name"`         // Toolbox name.
	CommonPageParams
}

// CommonPageParams common paging parameters.
type CommonPageParams struct {
	Page      int    `form:"page" default:"1" validate:"min=1"`                                           // Page number, starting from 1.
	PageSize  int    `form:"page_size" default:"10" validate:"min=1,max=100"`                             // page size.
	All       bool   `form:"all"`                                                                         // Whether to query all toolboxes.
	SortBy    string `form:"sort_by" default:"update_time" validate:"oneof=create_time update_time name"` // Sorting field, default is creation time.
	SortOrder string `form:"sort_order" default:"desc" validate:"oneof=asc desc"`                         // Sorting order, default is descending.
}

// ToolBoxInfo toolbox information.
type ToolBoxInfo struct {
	MetadataType MetadataType `json:"metadata_type" validate:"required,oneof=openapi function"` // metadata type.
	BoxID        string       `json:"box_id"`                                                   // Toolbox ID.
	BoxName      string       `json:"box_name"`                                                 // Toolbox name.
	BoxDesc      string       `json:"box_desc"`                                                 // Toolbox description.
	BoxSvcURL    string       `json:"box_svc_url"`                                              // Toolbox service address.
	Status       BizStatus    `json:"status" validate:"oneof=unpublish published offline"`      // toolbox status.
	CategoryType string       `json:"category_type"`                                            // Classification.
	CategoryName string       `json:"category_name"`                                            // Category name.
	IsInternal   bool         `json:"is_internal"`                                              // Is it an internal toolbox?.
	Source       string       `json:"source" default:"custom" validate:"oneof=custom internal"` // Toolbox source.
	Tools        []string     `json:"tools"`                                                    // Tool list under toolbox.
	CreateTime   int64        `json:"create_time"`                                              // creation time.
	UpdateTime   int64        `json:"update_time"`                                              // Update time.
	CreateUser   string       `json:"create_user"`                                              // Create user.
	UpdateUser   string       `json:"update_user"`                                              // Update user.
	ReleaseUser  string       `json:"release_user,omitempty"`                                   // Posted by.
	ReleaseTime  int64        `json:"release_time,omitempty"`                                   // Release time.
}

// QueryToolBoxListResp Gets the toolbox list and returns the results.
type QueryToolBoxListResp struct {
	CommonPageResult `json:",inline"`
	Data             []*ToolBoxInfo `json:"data"` // Toolbox list.
}

// ParametersStruct parameter structure.
type ParametersStruct struct {
	Name        string      `json:"name" validate:"required"`                                           // Parameter name.
	Description string      `json:"description" validate:"required"`                                    // Parameter description.
	Required    bool        `json:"required"`                                                           // Is it required?.
	In          string      `json:"in" validate:"required,oneof=query path header cookie body"`         // Parameter location, for example: query, path, header, cookie, body.
	Type        string      `json:"type" validate:"required,oneof=string integer boolean array object"` // Parameter type, for example: string, integer, boolean, array, object.
	Value       interface{} `json:"value"`                                                              // Parameter value.
}

// CreateToolReq Create tool request.
type CreateToolReq struct {
	UserID           string                 `header:"user_id" validate:"required"`                                                 // User ID, internal use.
	BoxID            string                 `uri:"box_id" validate:"required"`                                                     // Toolbox ID.
	MetadataType     MetadataType           `json:"metadata_type" form:"metadata_type" validate:"required,oneof=openapi function"` // Metadata type (mandatory parameter)
	UseRule          string                 `json:"use_rule" form:"use_rule"`                                                      // Usage rules.
	GlobalParameters *ParametersStruct      `json:"global_parameters" form:"global_parameters" validate:"omitempty"`               // global parameters.
	ExtendInfo       map[string]interface{} `json:"extend_info" form:"extend_info"`                                                // Extended information.
	FunctionInput    *FunctionInput         `json:"function_input,omitempty"`
	*OpenAPIInput    `json:",inline"`
}

// CreateToolResp Create tool return result.
type CreateToolResp struct {
	BoxID        string                    `json:"box_id"`                // Toolbox ID.
	SuccessCount int64                     `json:"success_count"`         // number of successes.
	SuccessIDs   []string                  `json:"success_ids,omitempty"` // List of successful tool IDs.
	FailureCount int64                     `json:"failure_count"`         // Number of failures.
	Failures     []CreateToolFailureResult `json:"failures,omitempty"`    // List of tool IDs and error messages that failed to create.
}

// CreateToolFailureResult Create tool failure result.
type CreateToolFailureResult struct {
	ToolName string `json:"tool_name"` // Failed tool name.
	Error    error  `json:"error_msg"` // Reason for failure.
}

// UpdateToolReq update tool request.
type UpdateToolReq struct {
	UserID            string                 `header:"user_id" validate:"required"` // User ID, internal use.
	BoxID             string                 `uri:"box_id" validate:"required"`
	ToolID            string                 `uri:"tool_id" validate:"required"`
	ToolName          string                 `json:"name" form:"name" validate:"required"`
	ToolDesc          string                 `json:"description" form:"description" validate:"required"`
	UseRule           string                 `json:"use_rule" form:"use_rule"`                                                      // Usage rules.
	GlobalParameters  *ParametersStruct      `json:"global_parameters" form:"global_parameters"`                                    // global parameters.
	ExtendInfo        map[string]interface{} `json:"extend_info" form:"extend_info"`                                                // Extended information.
	MetadataType      MetadataType           `json:"metadata_type" form:"metadata_type" validate:"required,oneof=openapi function"` // Metadata type (optional parameter)
	FunctionInputEdit *FunctionInputEdit     `json:"function_input,omitempty"`
	*OpenAPIInput     `json:",inline"`
}

// UpdateToolResp Update tool returns results.
type UpdateToolResp struct {
	BoxID  string `json:"box_id"`  // Toolbox ID.
	ToolID string `json:"tool_id"` // Tool ID.
}

// GetToolReq Get tool request.
type GetToolReq struct {
	UserID string `header:"user_id"` // User ID, internal use.
	BoxID  string `uri:"box_id" validate:"required"`
	ToolID string `uri:"tool_id" validate:"required"`
}

// BatchDeleteToolReq Batch delete tool request.
type BatchDeleteToolReq struct {
	UserID  string   `header:"user_id" validate:"required"` // User ID, internal use.
	BoxID   string   `uri:"box_id" validate:"required"`
	ToolIDs []string `json:"tool_ids" validate:"required"`
}

type BatchDeleteToolResp struct {
	BoxID  string   `json:"box_id"`   // Toolbox ID.
	ToolID []string `json:"tool_ids"` // Tool ID.
}

// QueryToolListReq Get tool list request.
type QueryToolListReq struct {
	UserID      string         `header:"user_id"` // User ID, internal use.
	Page        int            `form:"page" default:"1" validate:"min=1"`
	PageSize    int            `form:"page_size" default:"10" validate:"min=1,max=100"`
	SortBy      string         `form:"sort_by" default:"create_time" validate:"oneof=create_time update_time tool_name"` // Sorting field, default is creation time.
	SortOrder   string         `form:"sort_order" default:"desc" validate:"oneof=asc desc"`                              // Sorting order, default is descending.
	ToolName    string         `form:"name"`                                                                             // Tool name.
	Status      ToolStatusType `form:"status" validate:"omitempty,oneof=disabled enabled"`                               // tool status // tool status.
	QueryUserID string         `form:"user_id"`                                                                          // Query user ID.
	All         bool           `form:"all"`                                                                              // Whether to query all tools.
	BoxID       string         `uri:"box_id" validate:"required"`                                                        // Toolbox ID // Whether to query all tools.
}

// QueryToolListResp Gets the tool list and returns the results.
type QueryToolListResp struct {
	CommonPageResult `json:",inline"`
	BoxID            string      `json:"box_id"` // Toolbox ID.
	Tools            []*ToolInfo `json:"tools"`  // Tool list under toolbox.
}

// QueryMarketToolListReq Get market tool list request.
type QueryMarketToolListReq struct {
	UserID    string         `header:"user_id"` // User ID, internal use.
	Page      int            `form:"page" default:"1" validate:"min=1"`
	PageSize  int            `form:"page_size" default:"10" validate:"min=1,max=100"`
	SortBy    string         `form:"sort_by" default:"update_time" validate:"oneof=create_time update_time tool_name"` // Sorting field, default is creation time.
	SortOrder string         `form:"sort_order" default:"desc" validate:"oneof=asc desc"`                              // Sorting order, default is descending.
	ToolName  string         `form:"tool_name" validate:"required"`                                                    // Tool name.
	Status    ToolStatusType `form:"status" validate:"omitempty,oneof=disabled enabled"`                               // tool status.
	All       bool           `form:"all"`                                                                              // Whether to query all tools.
}

// QueryMarketToolListResp Gets the market tool list and returns the results.
type QueryMarketToolListResp struct {
	CommonPageResult `json:",inline"`
	Data             []*ToolBoxToolInfo `json:"data"` // Tool details list.
}

type ToolStatus struct {
	ToolID string         `json:"tool_id" validate:"required"`
	Status ToolStatusType `json:"status" validate:"required,oneof=disabled enabled"`
}

// UpdateToolStatusReq Update tool status request.
type UpdateToolStatusReq struct {
	UserID         string        `header:"user_id" validate:"required"` // User ID, internal use.
	BoxID          string        `uri:"box_id" validate:"required"`
	ToolStatusList []*ToolStatus `json:",inline"`
}

// ExecuteToolReq Execute tool request.
type ExecuteToolReq struct {
	UserID            string `header:"user_id" validate:"required"` // User ID, internal use.
	BoxID             string `uri:"box_id" validate:"required"`
	ToolID            string `uri:"tool_id" validate:"required"`
	Timeout           int    `json:"timeout"` // Timeout time in seconds.
	HTTPRequestParams `json:",inline"`
}

// ConvertOperatorToToolReq operator converts tool request.
type ConvertOperatorToToolReq struct {
	UserID           string            `header:"user_id" validate:"required"` // User ID, internal use.
	OperatorID       string            `json:"operator_id" validate:"required"`
	BoxID            string            `json:"box_id" validate:"required"`
	UseRule          string            `json:"use_rule"`          // Usage rules.
	ExtendInfo       map[string]string `json:"extend_info"`       // Extended information.
	GlobalParameters *ParametersStruct `json:"global_parameters"` // global parameters.
}

// ConvertOperatorToToolResp operator is converted into a tool and returns the result.
type ConvertOperatorToToolResp struct {
	BoxID  string `json:"box_id"`
	ToolID string `json:"tool_id"` // Tool ID.
}

// RegisterOpenApiBundleReq OpenAPI capability package registration: register the operator first, and then convert it into a tool (establishing blood relationship)
type RegisterOpenApiBundleReq struct {
	UserID                 string                  `header:"user_id" validate:"required"`
	BoxID                  string                  `json:"box_id"`                                // Already has a toolbox ID (optional with box_name)
	BoxName                string                  `json:"box_name"`                              // New toolbox name.
	BoxDesc                string                  `json:"box_desc"`                              // Toolbox description.
	BoxSvcURL              string                  `json:"box_svc_url" validate:"required"`       // Toolbox service address.
	Category               BizCategory             `json:"box_category" default:"other_category"` // Classification.
	UseRule                string                  `json:"use_rule"`                              // Tool usage rules.
	Data                   string                  `json:"data" validate:"required"`              // OpenAPI 3.0 documentation.
	Description            string                  `json:"description"`                           // Operator description.
	DirectPublish          bool                    `json:"direct_publish,omitempty"`              // Publish the operator directly after registration.
	OperatorInfo           *OperatorInfo           `json:"operator_info"`                         // Operator information.
	OperatorExecuteControl *OperatorExecuteControl `json:"operator_execute_control"`              // Operator execution control.
	ExtendInfo             map[string]interface{}  `json:"extend_info,omitempty"`                 // Extended information.
}

// OpenApiBundleLink operator and tool association.
type OpenApiBundleLink struct {
	OperatorID string `json:"operator_id"`
	ToolID     string `json:"tool_id"`
}

// RegisterOpenApiBundleResp OpenAPI capability package registration result.
type RegisterOpenApiBundleResp struct {
	BoxID        string              `json:"box_id"`
	ToolIDs      []string            `json:"tool_ids"`
	OperatorIDs  []string            `json:"operator_ids"`
	Links        []OpenApiBundleLink `json:"links"`
	FailureCount int                 `json:"failure_count,omitempty"`
	Failures     []string            `json:"failures,omitempty"`
}

// UpdateToolBoxStatusReq Update toolbox status request.
type UpdateToolBoxStatusReq struct {
	UserID string    `header:"user_id" validate:"required"` // User ID, internal use.
	BoxID  string    `uri:"box_id" validate:"required"`
	Status BizStatus `json:"status" validate:"required,oneof=unpublish published offline"` // toolbox status.
}

// UpdateToolBoxStatusResp Update toolbox status response.
type UpdateToolBoxStatusResp struct {
	BoxID  string    `json:"box_id"`
	Status BizStatus `json:"status"`
}

// GetReleaseToolBoxInfoReq Get toolbox information request.
type GetReleaseToolBoxInfoReq struct {
	UserID string `header:"user_id"`                 // User ID, internal use.
	BoxIDs string `uri:"box_id" validate:"required"` // Toolbox ID.
	Fields string `uri:"fields" validate:"required"` // Field.
}

// GetReleaseToolBoxInfoResp Gets the toolbox information response.
type GetReleaseToolBoxInfoResp struct {
	MetadataType MetadataType `json:"metadata_type" validate:"required,oneof=openapi function"`
	BoxID        string       `json:"box_id" validate:"required"`
	BoxName      string       `json:"box_name,omitempty"`
	BoxDesc      string       `json:"box_desc,omitempty"`
	BoxSvcURL    string       `json:"box_svc_url,omitempty"`
	Status       string       `json:"status,omitempty"`
	Category     BizCategory  `json:"category_type,omitempty"`
	CategoryName string       `json:"category_name,omitempty"`
	IsInternal   *bool        `json:"is_internal,omitempty"`
	Source       string       `json:"source,omitempty"`
	Tools        []*ToolInfo  `json:"tools,omitempty"`
	CreateUser   string       `json:"create_user,omitempty"`
	UpdateUser   string       `json:"update_user,omitempty"`
	ReleaseUser  string       `json:"release_user,omitempty"`
}

// IToolService toolbox service interface.
type IToolService interface {
	// Toolbox management.
	CreateToolBox(ctx context.Context, req *CreateToolBoxReq) (resp *CreateToolBoxResp, err error)
	UpdateToolBox(ctx context.Context, req *UpdateToolBoxReq) (resp *UpdateToolBoxResp, err error)
	GetToolBox(ctx context.Context, req *GetToolBoxReq, isMarket bool) (resp *ToolBoxToolInfo, err error)
	DeleteBoxByID(ctx context.Context, req *DeleteBoxReq) (resp *DeleteBoxResp, err error)
	QueryToolBoxList(ctx context.Context, req *QueryToolBoxListReq) (resp *QueryToolBoxListResp, err error)
	// GetToolBoxNamesByIDs batch names based on toolbox IDs (fault tolerance: non-existent IDs are ignored)
	GetToolBoxNamesByIDs(ctx context.Context, ids []string) (resp *BatchNamesResp, err error)
	QueryMarketToolBoxList(ctx context.Context, req *QueryMarketToolBoxListReq) (resp *QueryToolBoxListResp, err error)
	UpdateToolBoxStatus(ctx context.Context, req *UpdateToolBoxStatusReq) (resp *UpdateToolBoxStatusResp, err error)
	// Tool management.
	CreateTool(ctx context.Context, req *CreateToolReq) (resp *CreateToolResp, err error)
	UpdateTool(ctx context.Context, req *UpdateToolReq) (resp *UpdateToolResp, err error)
	GetBoxTool(ctx context.Context, req *GetToolReq) (resp *ToolInfo, err error)
	DeleteBoxTool(ctx context.Context, req *BatchDeleteToolReq) (resp *BatchDeleteToolResp, err error)
	QueryToolList(ctx context.Context, req *QueryToolListReq) (resp *QueryToolListResp, err error)
	UpdateToolStatus(ctx context.Context, req *UpdateToolStatusReq) (resp []*ToolStatus, err error)
	// Tool debugging.
	DebugTool(ctx context.Context, req *ExecuteToolReq) (resp *HTTPResponse, err error)
	// tool execution.
	ExecuteTool(ctx context.Context, req *ExecuteToolReq) (resp *HTTPResponse, err error)
	// Tool execution (excluding permission verification and audit logs)
	ExecuteToolCore(ctx context.Context, req *ExecuteToolReq) (resp *HTTPResponse, err error)
	// Operators converted into tools.
	ConvertOperatorToTool(ctx context.Context, req *ConvertOperatorToToolReq) (resp *ConvertOperatorToToolResp, err error)
	// OpenAPI capability package: operator registration + convert tool (unified bloodline)
	RegisterOpenApiBundle(ctx context.Context, req *RegisterOpenApiBundleReq) (resp *RegisterOpenApiBundleResp, err error)
	GetReleaseToolBoxInfo(ctx context.Context, req *GetReleaseToolBoxInfoReq) (resp []*GetReleaseToolBoxInfoResp, err error)
	// market interface.
	GetMarketToolList(ctx context.Context, req *QueryMarketToolListReq) (resp *QueryMarketToolListResp, err error) // Get all the tools.

	// Import exceeds.
	// Impex[*ToolBoxImpexData]
	Import(ctx context.Context, tx *sql.Tx, mode ImportType, data *ComponentImpexConfigModel, userID string) (err error)
	Export(ctx context.Context, req *ExportReq) (data *ComponentImpexConfigModel, err error)
	// event handling.
	ToolBoxEventHandler
}

// ToolBoxEventHandler event handling interface.
type ToolBoxEventHandler interface {
	HandleOperatorDeleteEvent(ctx context.Context, message []byte) error
}
