package model

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

// ToolboxDB ToolboxDB.
//
//go:generate mockgen -source=toolbox.go -destination=../../mocks/model_toolbox.go -package=mocks
type ToolboxDB struct {
	ID          int64  `json:"id" db:"f_id"`                     // Primary key ID.
	BoxID       string `json:"box_id" db:"f_box_id"`             // Toolbox ID.
	Name        string `json:"name" db:"f_name"`                 // Toolbox name.
	Description string `json:"description" db:"f_description"`   // Toolbox description.
	Source      string `json:"source" db:"f_source"`             // Toolbox source.
	ServerURL   string `json:"server_url" db:"f_svc_url"`        // Toolbox service address.
	Category    string `json:"category" db:"f_category"`         // Classification.
	Status      string `json:"status" db:"f_status"`             // Status.
	IsInternal  bool   `json:"is_internal" db:"f_is_internal"`   // Is it built-in.
	CreateUser  string `json:"create_user" db:"f_create_user"`   // Creator.
	CreateTime  int64  `json:"create_time" db:"f_create_time"`   // creation time.
	UpdateUser  string `json:"update_user" db:"f_update_user"`   // Updater.
	UpdateTime  int64  `json:"update_time" db:"f_update_time"`   // Update time.
	ReleaseUser string `json:"release_user" db:"f_release_user"` // Posted by.
	ReleaseTime int64  `json:"release_time" db:"f_release_time"` // Release time.
	// Toolbox metadata types.
	MetadataType string `json:"metadata_type" db:"f_metadata_type"` // Toolbox metadata types.
}

// GetBizID Get business ID.
func (b *ToolboxDB) GetBizID() string {
	return b.BoxID
}

// IToolboxDB toolbox interface.
type IToolboxDB interface {
	InsertToolBox(ctx context.Context, tx *sql.Tx, toolbox *ToolboxDB) (boxID string, err error)
	UpdateToolBox(ctx context.Context, tx *sql.Tx, toolbox *ToolboxDB) error
	SelectToolBox(ctx context.Context, boxID string) (bool, *ToolboxDB, error)
	SelectToolBoxList(ctx context.Context, filter map[string]interface{}, sort *ormhelper.SortParams, cursor *ormhelper.CursorParams) ([]*ToolboxDB, error)
	DeleteToolBox(ctx context.Context, tx *sql.Tx, boxID string) error
	CountToolBox(ctx context.Context, filter map[string]interface{}) (int64, error)
	SelectToolBoxByName(ctx context.Context, name string, status []string) (bool, *ToolboxDB, error)
	UpdateToolBoxStatus(ctx context.Context, tx *sql.Tx, boxID, status string, updateUser string) (err error)
	SelectListByBoxIDs(ctx context.Context, boxIDs []string, status ...string) ([]*ToolboxDB, error)
	SelectListByBoxIDsFilter(ctx context.Context, boxIDs []string, status string, filter map[string]interface{}) ([]*ToolboxDB, error)
	SelectListByNamesAndStatus(ctx context.Context, names []string, status ...string) ([]*ToolboxDB, error)
}
