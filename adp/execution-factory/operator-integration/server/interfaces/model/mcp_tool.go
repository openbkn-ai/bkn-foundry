package model

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source=mcp_tool.go -destination=../../mocks/model_mcp_tool.go -package=mocks

// MCPToolDB MCP tool table.
type MCPToolDB struct {
	ID          int64  `json:"id" db:"f_id"`                   // primary key.
	MCPToolID   string `json:"mcp_tool_id" db:"f_mcp_tool_id"` // mcp_tool_id
	MCPID       string `json:"mcp_id" db:"f_mcp_id"`           // mcp_id
	MCPVersion  int    `json:"mcp_version" db:"f_mcp_version"` // mcp_version
	BoxID       string `json:"box_id" db:"f_box_id"`           // box_id
	BoxName     string `json:"box_name" db:"f_box_name"`       // box_name
	ToolID      string `json:"tool_id" db:"f_tool_id"`         // tool_id
	Name        string `json:"name" db:"f_name"`               // Tool name.
	Description string `json:"description" db:"f_description"` // Description information.
	UseRule     string `json:"use_rule" db:"f_use_rule"`       // Usage rules.
	CreateUser  string `json:"create_user" db:"f_create_user"` // Creator.
	CreateTime  int64  `json:"create_time" db:"f_create_time"` // creation time.
	UpdateUser  string `json:"update_user" db:"f_update_user"` // Editor.
	UpdateTime  int64  `json:"update_time" db:"f_update_time"` // Edit time.
}

type DBMCPTool interface {
	// Insert MCP tool configuration information in batches.
	BatchInsert(ctx context.Context, tx *sql.Tx, tools []*MCPToolDB) (err error)
	// Delete MCP tool configuration information in batches.
	DeleteByMCPIDAndVersion(ctx context.Context, tx *sql.Tx, mcpID string, mcpVersion int) (err error)
	// Query MCP tool configuration information based on MCPID and version number.
	SelectListByMCPIDAndVersion(ctx context.Context, tx *sql.Tx, mcpID string, mcpVersion int) (tools []*MCPToolDB, err error)
	// Query MCP tool configuration information based on MCPID list.
	SelectListByMCPIDS(ctx context.Context, tx *sql.Tx, mcpIDs []string) (tools []*MCPToolDB, err error)
	// Query MCP tool configuration information based on MCPToolID.
	SelectByMCPToolID(ctx context.Context, tx *sql.Tx, mcpToolID string) (tool *MCPToolDB, err error)
}
