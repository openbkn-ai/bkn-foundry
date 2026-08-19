package model

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

// MCPServerReleaseDB The structure corresponding to the MCP Server release table.
//
//go:generate mockgen -source=mcp_server_release.go -destination=../../mocks/model_mcp_server_release.go -package=mocks
type MCPServerReleaseDB struct {
	ID           int64  `json:"f_id" db:"f_id"`                       // id
	MCPID        string `json:"f_mcp_id" db:"f_mcp_id"`               // mcp_id
	CreateUser   string `json:"f_create_user" db:"f_create_user"`     // Creator.
	CreateTime   int64  `json:"f_create_time" db:"f_create_time"`     // creation time.
	UpdateUser   string `json:"f_update_user" db:"f_update_user"`     // Editor.
	UpdateTime   int64  `json:"f_update_time" db:"f_update_time"`     // Edit time.
	CreationType string `json:"f_creation_type" db:"f_creation_type"` // Create type.
	Name         string `json:"f_name" db:"f_name"`                   // MCP Server name, globally unique.
	Description  string `json:"f_description" db:"f_description"`     // Description information.
	Mode         string `json:"f_mode" db:"f_mode"`                   // Communication mode (sse, streamable, stdio_npx, stdio_uvx)
	URL          string `json:"f_url" db:"f_url"`                     // Communication address, service URL in SSE/Streamable mode.
	Headers      string `json:"f_headers" db:"f_headers"`             // http request header, JSON string.
	Command      string `json:"f_command" db:"f_command"`             // Commands in stdio mode.
	Env          string `json:"f_env" db:"f_env"`                     // environment variables.
	Args         string `json:"f_args" db:"f_args"`                   // Command parameters.
	Category     string `json:"f_category" db:"f_category"`           // Classification.
	Source       string `json:"f_source" db:"f_source"`               // Service source.
	IsInternal   bool   `json:"f_is_internal" db:"f_is_internal"`     // Whether it is built-in.

	Version     int    `json:"f_version" db:"f_version"`           // release version.
	ReleaseDesc string `json:"f_release_desc" db:"f_release_desc"` // Release description.
	ReleaseUser string `json:"f_release_user" db:"f_release_user"` // Publisher.
	ReleaseTime int64  `json:"f_release_time" db:"f_release_time"` // Release time.
}

// GetBizID Get business ID.
func (m *MCPServerReleaseDB) GetBizID() string {
	return m.MCPID
}

// DBMCPServerRelease MCP Server publishes table database operations.
type DBMCPServerRelease interface {
	// Insert inserts MCP Server release.
	Insert(ctx context.Context, tx *sql.Tx, release *MCPServerReleaseDB) (err error)
	// UpdateByMCPID updates MCP Server release based on mcp_id.
	UpdateByMCPID(ctx context.Context, tx *sql.Tx, release *MCPServerReleaseDB) error
	// SelectListPage paging query mcp server publishing list.
	SelectListPage(ctx context.Context, tx *sql.Tx, filter map[string]interface{},
		sort *ormhelper.SortParams, cursor *ormhelper.CursorParams) (releaseList []*MCPServerReleaseDB, err error)
	// SelectByMCPID queries MCP Server publishing based on mcp_id.
	SelectByMCPID(ctx context.Context, tx *sql.Tx, mcpID string) (release *MCPServerReleaseDB, err error)
	// SelectByMCPIDs queries MCP Server publishing based on mcp_id list.
	SelectByMCPIDs(ctx context.Context, tx *sql.Tx, mcpIDs []string, fields []string) (releaseList []*MCPServerReleaseDB, err error)
	// CountByWhereClause counts quantities based on conditions.
	CountByWhereClause(ctx context.Context, tx *sql.Tx, filter map[string]interface{}) (count int64, err error)
	// DeleteByMCPID Delete MCP Server publication based on mcp_id.
	DeleteByMCPID(ctx context.Context, tx *sql.Tx, mcpID string) error
}
