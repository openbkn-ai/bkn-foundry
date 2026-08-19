package model

import (
	"context"
	"database/sql"
)

// MCPServerReleaseHistoryDB The structure corresponding to the MCP Server release history table.
//
//go:generate mockgen -source=mcp_server_release_history.go -destination=../../mocks/model_mcp_server_release_history.go -package=mocks
type MCPServerReleaseHistoryDB struct {
	ID         int64  `json:"f_id" db:"f_id"`                   // id
	CreateUser string `json:"f_create_user" db:"f_create_user"` // Creator.
	CreateTime int64  `json:"f_create_time" db:"f_create_time"` // creation time.
	UpdateUser string `json:"f_update_user" db:"f_update_user"` // Editor.
	UpdateTime int64  `json:"f_update_time" db:"f_update_time"` // Edit time.

	MCPID       string `json:"f_mcp_id" db:"f_mcp_id"`             // mcp_id
	MCPRelease  string `json:"f_mcp_release" db:"f_mcp_release"`   // mcp server releases information.
	Version     int    `json:"f_version" db:"f_version"`           // release version.
	ReleaseDesc string `json:"f_release_desc" db:"f_release_desc"` // Release description.
	// FromVersion string `json:"from_version" db:"from_version"` // Roll back the source version.
	// RollbackReason string `json:"rollback_reason" db:"rollback_reason"` // Rollback reason.
}

// DBMCPServerReleaseHistory MCP Server publishes history table database operations.
type DBMCPServerReleaseHistory interface {
	// InsertMCPServerReleaseHistory Insert MCP Server release history.
	Insert(ctx context.Context, tx *sql.Tx, history *MCPServerReleaseHistoryDB) (id string, err error)
	// SelectByMCPID Query MCP Server release history based on mcp_id.
	SelectByMCPID(ctx context.Context, tx *sql.Tx, mcpID string) (historys []*MCPServerReleaseHistoryDB, err error)
	// DeleteByID Delete MCP Server release history based on id.
	DeleteByID(ctx context.Context, tx *sql.Tx, id int64) error
	// DeleteByMCPID Delete MCP Server release history based on mcp_id.
	DeleteByMCPID(ctx context.Context, tx *sql.Tx, mcpID string) error
	// DeleteByMCPIDAndVersion Delete MCP Server release history based on mcp_id and version.
	DeleteByMCPIDAndVersion(ctx context.Context, tx *sql.Tx, mcpID string, version int) error
}
