package model

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

// OperatorReleaseDB operator release table.
//
//go:generate mockgen -source=operator_release.go -destination=../../mocks/model_operator_release.go -package=mocks
type OperatorReleaseDB struct {
	ID              int64  `json:"id" db:"f_id"`                             // primary key.
	OpID            string `json:"op_id" db:"f_op_id"`                       // Operator ID.
	Name            string `json:"name" db:"f_name"`                         // Operator name.
	MetadataVersion string `json:"metadata_version" db:"f_metadata_version"` // metadata version.
	MetadataType    string `json:"metadata_type" db:"f_metadata_type"`       // metadata type.
	Status          string `json:"status" db:"f_status"`                     // metadata status.
	OperatorType    string `json:"operator_type" db:"f_operator_type"`       // Operator type.
	ExecutionMode   string `json:"execution_mode" db:"f_execution_mode"`     // execution mode.
	ExecuteControl  string `json:"execute_control" db:"f_execute_control"`   // executive control.
	Category        string `json:"category" db:"f_category"`                 // Operator classification.
	Source          string `json:"source" db:"f_source"`                     // Operator source.
	ExtendInfo      string `json:"extend_info" db:"f_extend_info"`           // Extended information.
	CreateTime      int64  `json:"create_time" db:"f_create_time"`           // creation time.
	UpdateTime      int64  `json:"update_time" db:"f_update_time"`           // Update time.
	ReleaseTime     int64  `json:"release_time" db:"f_release_time"`         // Release time.
	CreateUser      string `json:"create_user" db:"f_create_user"`           // Create user.
	UpdateUser      string `json:"update_user" db:"f_update_user"`           // Update user.
	ReleaseUser     string `json:"release_user" db:"f_release_user"`         // publish user.
	Tag             int    `json:"tag" db:"f_tag"`                           // version.
	IsInternal      bool   `json:"is_internal" db:"f_is_internal"`           // Whether it is an internal operator.
	IsDataSource    bool   `json:"is_data_source" db:"f_is_data_source"`     // Whether it is a data source operator.
}

// GetBizID Get business ID.
func (ore *OperatorReleaseDB) GetBizID() string {
	return ore.OpID
}

// IOperatorReleaseDB operator release table operation interface.
type IOperatorReleaseDB interface {
	// BatchInsert batch insertion operator publishes information.
	BatchInsert(ctx context.Context, tx *sql.Tx, operator []*OperatorReleaseDB) (opIDs []string, err error)
	// Insert insertion operator publishes information.
	Insert(ctx context.Context, tx *sql.Tx, operator *OperatorReleaseDB) (err error)
	// UpdateByOpID update operator release information.
	UpdateByOpID(ctx context.Context, tx *sql.Tx, operator *OperatorReleaseDB) error
	// DeleteByOpID delete operator release information.
	DeleteByOpID(ctx context.Context, tx *sql.Tx, opID string) error
	// SelectByOpID Query operator release information based on operator ID.
	SelectByOpID(ctx context.Context, opID string) (exist bool, releaseDB *OperatorReleaseDB, err error)
	// SelectByName Query operator release information based on operator name.
	SelectByName(ctx context.Context, tx *sql.Tx, name string) (exist bool, releaseDB *OperatorReleaseDB, err error)
	// CountByWhereClause queries the number of information released by the operator based on conditions.
	CountByWhereClause(ctx context.Context, conditions map[string]interface{}) (count int64, err error)
	// SelectByWhereClause publishes information based on conditional query operators.
	SelectByWhereClause(ctx context.Context, conditions map[string]interface{}, sort *ormhelper.SortParams, cursor *ormhelper.CursorParams) (releaseList []*OperatorReleaseDB, err error)
}
