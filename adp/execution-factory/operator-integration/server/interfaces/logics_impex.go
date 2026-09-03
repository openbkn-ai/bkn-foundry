package interfaces

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

//go:generate mockgen -source=logics_impex.go -destination=../mocks/logics_impex.go -package=mocks

// ImportType import type.
type ImportType string

const (
	ImportTypeUpsert ImportType = "upsert" // update or create.
	ImportTypeCreate ImportType = "create" // Create only.
)

// IComponentImpexConfig component import and export configuration interface.
type IComponentImpexConfig interface {
	ExportConfig(ctx context.Context, exportReq *ExportConfigReq) (config *ComponentImpexConfigModel, err error)
	ImportConfig(ctx context.Context, importReq *ImportConfigReq) (err error)
}

// ExportConfigReq export configuration request.
type ExportConfigReq struct {
	UserID string        `header:"user_id" validate:"required"`                      // User ID.
	Type   ComponentType `uri:"type" validate:"required,oneof=operator toolbox mcp"` // Component type.
	ID     string        `uri:"id" validate:"required"`                              // Component ID.
}

// ImportConfigReq import configuration request.
type ImportConfigReq struct {
	UserID string          `header:"user_id" validate:"required"`                        // User ID.
	Type   ComponentType   `uri:"type" validate:"required,oneof=operator toolbox mcp"`   // Component type.
	Mode   ImportType      `form:"mode" default:"create" validate:"oneof=create upsert"` // Configure import type.
	Data   json.RawMessage `form:"data" validate:"required"`
}

// ComponentImpexConfigModel component import and export configuration model.
type ComponentImpexConfigModel struct {
	Operator *OperatorImpexConfig `json:"operator,omitempty"`
	Toolbox  *ToolBoxImpexConfig  `json:"toolbox,omitempty"`
	MCP      *MCPImpexConfig      `json:"mcp,omitempty"`
}

// OperatorImpexConfig operator import and export configuration.
type OperatorImpexConfig struct {
	Configs          []*OperatorImpexItem `json:"configs"`           // Operator configuration.
	CompositeConfigs []any                `json:"composite_configs"` // Combination operator depends on configuration.
}

// ToolBoxImpexConfig toolbox import and export configuration.
type ToolBoxImpexConfig struct {
	Configs []*ToolBoxImpexItem `json:"configs"`
}

// MCPImpexConfig MCP import and export configuration.
type MCPImpexConfig struct {
	Configs []*MCPServersImpexItem `json:"configs"`
}

// Impex import and export interface.
type Impex[T any] interface {
	Import(context.Context, *sql.Tx, *ImportReq[T]) error
	Export(context.Context, *ExportReq) (T, error)
}

// ExportReq export request.
type ExportReq struct {
	UserID string   `header:"user_id" validate:"required"`
	IDs    []string `json:"ids" validate:"required,min=1"` // Check length.
}

// ImportReq import request.
type ImportReq[T any] struct {
	UserID string     `header:"user_id" validate:"required"`
	Mode   ImportType `json:"mode" validate:"required,oneof=upsert create"`
	Data   T          `json:"data" validate:"required"`
}

type ImportResp struct {
	// Number of successes.
	SuccessCount int `json:"success_count"`
	// Number of failures.
	FailedCount int `json:"failed_count"`
	// Failure details.
	FailedDetails []*ImportFailedDetail `json:"failed_details,omitempty"`
}

type ImportFailedDetail struct {
	Type ComponentType `json:"type"`
	// Failure object information.
	ID   string `json:"id"`
	Name string `json:"name"`
	// error message.
	Error error `json:"error,omitempty"`
}

// // ParseData parses JSON data in the form into generic types.
// func (r *ImportReq[T]) ParseData(data T) error {
// 	// var data T
// 	if r.Data == nil {
// 		return fmt.Errorf("import data is nil")
// 	}
// 	if err := json.Unmarshal([]byte(r.Data), data); err != nil {
// 		err = fmt.Errorf("data unmarshal err: %w", err)
// 		return err
// 	}
// 	return nil
// }

type ToolBoxImpexData struct {
	ToolBoxes []*ToolBoxImpexItem `json:"tool_boxes"`
}

// ToolBoxImpexItem toolbox import and export data model.
type ToolBoxImpexItem struct {
	BoxID        string           `json:"box_id" validate:"required"`                                                 // Toolbox ID.
	BoxName      string           `json:"box_name" validate:"required"`                                               // Toolbox name.
	BoxDesc      string           `json:"box_desc"`                                                                   // Toolbox description.
	BoxSvcURL    string           `json:"box_svc_url" validate:"required,url"`                                        // Toolbox service address.
	Status       BizStatus        `json:"status" validate:"oneof=unpublish published offline"`                        // toolbox status.
	CategoryType string           `json:"category_type" validate:"required"`                                          // Classification.
	CategoryName string           `json:"category_name"`                                                              // Category name.
	IsInternal   bool             `json:"is_internal"`                                                                // Is it an internal toolbox?.
	Source       string           `json:"source" default:"custom" validate:"oneof=custom internal"`                   // Toolbox source.
	Tools        []*ToolImpexItem `json:"tools"`                                                                      // Tool list under toolbox.
	CreateTime   int64            `json:"create_time"`                                                                // creation time.
	UpdateTime   int64            `json:"update_time"`                                                                // Update time.
	CreateUser   string           `json:"create_user"`                                                                // Create user.
	UpdateUser   string           `json:"update_user"`                                                                // Update user.
	MetadataType MetadataType     `json:"metadata_type" default:"openapi" validate:"required,oneof=openapi function"` // metadata type.
}

// ToolImpexItem tool import and export data model.
type ToolImpexItem struct {
	ToolInfo        `json:",inline"`
	SourceID        string           `json:"source_id"`
	SourceType      model.SourceType `json:"source_type"`
	FunctionContent `json:",inline"` // Function content definition when metadata_type=="function".
}
type MCPImpexData struct {
	MCPServers      []*MCPServersImpexItem `json:"mcp_servers"`
	DeployToolBoxes []*ToolBoxImpexItem    `json:"deploy_tool_boxes,omitempty"` // Dependent tools.
}

// MCPServersImpexItem MCP Server export data.
type MCPServersImpexItem struct {
	MCPCoreConfigInfo `json:",inline"`
	MCPID             string          `json:"mcp_id" validate:"required"`                                           // MCP Server ID
	Version           int             `json:"version,omitempty"`                                                    // MCP Server version.
	CreationType      MCPCreationType `json:"creation_type" validate:"required"`                                    // Create type.
	Name              string          `json:"name" validate:"required"`                                             // MCP Server name.
	Description       string          `json:"description" validate:"required"`                                      // Description information.
	Status            BizStatus       `json:"status" validate:"required,oneof=unpublish editing published offline"` // Status.
	Source            string          `json:"source" validate:"required"`                                           // Source.
	IsInternal        bool            `json:"is_internal"`                                                          // Whether it is built-in.
	Category          string          `json:"category,omitempty" default:"other_category"`                          // Classification.
	CreateUser        string          `json:"create_user,omitempty"`                                                // Create user.
	CreateTime        int64           `json:"create_time,omitempty"`                                                // creation time.
	UpdateUser        string          `json:"update_user,omitempty"`                                                // Update user.
	UpdateTime        int64           `json:"update_time,omitempty"`                                                // Update time.
	MCPTools          []*MCPToolItem  `json:"mcp_tools,omitempty"`                                                  // mcp tool configuration.
}

// MCPToolItem MCP tool configuration.
type MCPToolItem struct {
	MCPToolID   string `json:"mcp_tool_id" validate:"required"` // mcp tool id
	MCPID       string `json:"mcp_id" validate:"required"`      // mcp id
	MCPVersion  int    `json:"mcp_version"`
	BoxID       string `json:"box_id" validate:"required"` // box id
	BoxName     string `json:"box_name" validate:"required"`
	ToolID      string `json:"tool_id" validate:"required"` // tool id
	Name        string `json:"name" validate:"required"`
	Description string `json:"description"`
	UseRule     string `json:"use_rule"`
}

type OperatorImpexData struct {
	Operators []*OperatorImpexItem `json:"operators"`
}

// OperatorImpexItem operator import and export data model.
type OperatorImpexItem struct {
	OperatorID             string                  `json:"operator_id" validate:"uuid"`                                           // Operator ID.
	OperatorName           string                  `json:"operator_name" validate:"required"`                                     // Operator name.
	Version                string                  `json:"version" validate:"uuid"`                                               // Operator version.
	Status                 BizStatus               `json:"status" validate:"omitempty,oneof=unpublish published offline editing"` // Status.
	MetadataType           MetadataType            `json:"metadata_type" default:"openapi" validate:"oneof=openapi function"`     // Operator metadata type (mandatory parameter)
	Metadata               *MetadataInfo           `json:"metadata" validate:"required"`                                          // Operator metadata.
	ExtendInfo             map[string]interface{}  `json:"extend_info,omitempty"`                                                 // Extended information.
	OperatorInfo           *OperatorInfo           `json:"operator_info"`                                                         // Operator information.
	OperatorExecuteControl *OperatorExecuteControl `json:"operator_execute_control"`                                              // Operator execution control.
	CreateUser             string                  `json:"create_user"`                                                           // Create user.
	CreateTime             int64                   `json:"create_time"`                                                           // creation time.
	UpdateUser             string                  `json:"update_user"`                                                           // Update user.
	UpdateTime             int64                   `json:"update_time"`                                                           // Update time.
	IsInternal             bool                    `json:"is_internal"`                                                           // Is it an internal operator?.
}
