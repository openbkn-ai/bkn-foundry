package model

import (
	"context"
	"database/sql"
)

// SourceType source type.
//
//go:generate mockgen -source=tool.go -destination=../../mocks/model_tool.go -package=mocks
type SourceType string

const (
	SourceTypeOpenAPI  SourceType = "openapi"  // openapi
	SourceTypeOperator SourceType = "operator" // Operators converted into tools.
	SourceTypeFunction SourceType = "function" // function.
)

// ToolDB tools.
type ToolDB struct {
	ID          int64      `json:"id" db:"f_id"`
	ToolID      string     `json:"tool_id" db:"f_tool_id"`
	BoxID       string     `json:"box_id" db:"f_box_id"`
	Name        string     `json:"name" db:"f_name"`
	Description string     `json:"description" db:"f_description"`
	SourceID    string     `json:"source_id" db:"f_source_id"`     // Source ID.
	SourceType  SourceType `json:"source_type" db:"f_source_type"` // Source type.
	Status      string     `json:"status" db:"f_status"`
	UseRule     string     `json:"use_rule" db:"f_use_rule"`     // Usage rules.
	Parameters  string     `json:"parameters" db:"f_parameters"` // parameters.
	UseCount    int64      `json:"use_count" db:"f_use_count"`   // Number of uses.
	CreateUser  string     `json:"create_user" db:"f_create_user"`
	CreateTime  int64      `json:"create_time" db:"f_create_time"`
	UpdateUser  string     `json:"update_user" db:"f_update_user"`
	UpdateTime  int64      `json:"update_time" db:"f_update_time"`
	ExtendInfo  string     `json:"extend_info" db:"f_extend_info"` // Extended information.
	IsDeleted   bool       `json:"is_deleted" db:"f_is_deleted"`   // Whether to delete.
}

// IToolDB tool interface.
type IToolDB interface {
	InsertTools(ctx context.Context, tx *sql.Tx, tools []*ToolDB) ([]string, error)
	InsertTool(ctx context.Context, tx *sql.Tx, tool *ToolDB) (string, error)
	UpdateTool(ctx context.Context, tx *sql.Tx, tool *ToolDB) error
	SelectTool(ctx context.Context, toolID string) (bool, *ToolDB, error)
	SelectToolList(ctx context.Context, filter map[string]interface{}) ([]*ToolDB, error)
	CountToolByBoxID(ctx context.Context, boxID string, filter map[string]interface{}) (int64, error)
	SelectToolLisByBoxID(ctx context.Context, boxID string, filter map[string]interface{}) (tools []*ToolDB, err error)
	SelectBoxToolByName(ctx context.Context, boxID, name string) (bool, *ToolDB, error)
	SelectToolByBoxID(ctx context.Context, boxID string) ([]*ToolDB, error)
	// SelectToolNameListByBoxID(ctx context.Context, boxID string) ([]string, error)
	SelectToolNameListByBoxID(ctx context.Context, boxID []string) (toolNameList map[string][]string, err error)
	DeleteBoxByIDAndTools(ctx context.Context, tx *sql.Tx, boxID string, toolIDs []string) error
	SelectToolBoxByID(ctx context.Context, boxID string, toolID []string) ([]*ToolDB, error)
	SelectToolBoxByIDs(ctx context.Context, boxIDs []string) ([]*ToolDB, error)
	UpdateToolStatus(ctx context.Context, tx *sql.Tx, toolID, status, userID string) error
	// Get the toolbox ID according to query conditions and GROUP BY.
	SelectToolBoxIDsByFilter(ctx context.Context, filter map[string]interface{}) ([]string, error)
	SelectToolBoxByToolIDs(ctx context.Context, toolIDs []string) ([]*ToolDB, error)
	SelectToolBySource(ctx context.Context, sourceType SourceType, sourceID string) ([]*ToolDB, error)
}
