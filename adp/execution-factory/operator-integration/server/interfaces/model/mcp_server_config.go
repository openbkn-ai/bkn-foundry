package model

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

//go:generate mockgen -source=mcp_server_config.go -destination=../../mocks/model_mcp_server_config.go -package=mocks

// MCPServerConfigDB The structure corresponding to the MCP Server configuration table.
type MCPServerConfigDB struct {
	ID           int64  `json:"f_id" db:"f_id"`                       // id
	MCPID        string `json:"f_mcp_id" db:"f_mcp_id"`               // mcp_id
	CreateUser   string `json:"f_create_user" db:"f_create_user"`     // Creator.
	CreateTime   int64  `json:"f_create_time" db:"f_create_time"`     // creation time.
	UpdateUser   string `json:"f_update_user" db:"f_update_user"`     // Editor.
	UpdateTime   int64  `json:"f_update_time" db:"f_update_time"`     // Edit time.
	CreationType string `json:"f_creation_type" db:"f_creation_type"` // Create type.
	Version      int    `json:"f_version" db:"f_version"`             // version number.
	Name         string `json:"f_name" db:"f_name"`                   // MCP Server name, globally unique.
	Description  string `json:"f_description" db:"f_description"`     // Description information.
	Mode         string `json:"f_mode" db:"f_mode"`                   // Communication mode (sse, streamable, stdio_npx, stdio_uvx)
	URL          string `json:"f_url" db:"f_url"`                     // Communication address, service URL in SSE/Streamable mode.
	Headers      string `json:"f_headers" db:"f_headers"`             // http request header, JSON string.
	Command      string `json:"f_command" db:"f_command"`             // Commands in stdio mode.
	Env          string `json:"f_env" db:"f_env"`                     // environment variables.
	Args         string `json:"f_args" db:"f_args"`                   // Command parameters.
	Status       string `json:"f_status" db:"f_status"`               // Status.
	Category     string `json:"f_category" db:"f_category"`           // Classification.
	Source       string `json:"f_source" db:"f_source"`               // Service source.
	IsInternal   bool   `json:"f_is_internal" db:"f_is_internal"`     // Whether it is built-in.
}

// GetBizID Get business ID.
func (m *MCPServerConfigDB) GetBizID() string {
	return m.MCPID
}

// DBMCPServerConfig MCP Server configuration table database operations.
type DBMCPServerConfig interface {
	// Insert Insert MCP Server configuration.
	Insert(ctx context.Context, tx *sql.Tx, config *MCPServerConfigDB) (ID string, err error)
	// UpdateByID updates MCP Server configuration.
	UpdateByID(ctx context.Context, tx *sql.Tx, config *MCPServerConfigDB) error
	// UpdateStatus updates MCP Server configuration status.
	UpdateStatus(ctx context.Context, tx *sql.Tx, ID string, status string, updateUser string, version int) error
	// DeleteByID deletes MCP Server configuration.
	DeleteByID(ctx context.Context, tx *sql.Tx, ID string) error
	// BatchDelete Batch delete MCP Server configuration.
	BatchDelete(ctx context.Context, tx *sql.Tx, IDs []string) error
	// SelectListPage paging query mcp server configuration list.
	SelectListPage(ctx context.Context, tx *sql.Tx, filter map[string]interface{},
		sort *ormhelper.SortParams, cursor *ormhelper.CursorParams) (configList []*MCPServerConfigDB, err error)
	// SelectByID Query MCP Server configuration.
	SelectByID(ctx context.Context, tx *sql.Tx, ID string) (config *MCPServerConfigDB, err error)
	// SelectByName Query MCP Server configuration.
	SelectByName(ctx context.Context, tx *sql.Tx, name string, status []string) (config *MCPServerConfigDB, err error)
	// CountByWhereClause counts quantities based on conditions.
	CountByWhereClause(ctx context.Context, tx *sql.Tx, filter map[string]interface{}) (count int64, err error)
	// SelectByMCPIDs Query MCP Server configuration list.
	SelectByMCPIDs(ctx context.Context, mcpIDs []string) (configList []*MCPServerConfigDB, err error)
	// SelectListByNamesAndStatus gets lists in batches based on names and statuses.
	SelectListByNamesAndStatus(ctx context.Context, names []string, status ...string) (configList []*MCPServerConfigDB, err error)
}
