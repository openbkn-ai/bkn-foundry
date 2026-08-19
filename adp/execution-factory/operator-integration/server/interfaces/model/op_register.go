// Package model defines database operation interface.
// @file op_registry.go
// @description: Define t_op_registry table operation interface.
package model

//go:generate mockgen -source=op_register.go -destination=../../mocks/model_op_register.go -package=mocks
import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

// OperatorRegisterDB operator registration database.
type OperatorRegisterDB struct {
	ID              int64  `json:"f_id" db:"f_id"`
	OperatorID      string `json:"f_op_id" db:"f_op_id"`
	Name            string `json:"f_name" db:"f_name"` // Operator name.
	MetadataVersion string `json:"f_metadata_version" db:"f_metadata_version"`
	MetadataType    string `json:"f_metadata_type" db:"f_metadata_type"`
	Status          string `json:"f_status" db:"f_status"`
	OperatorType    string `json:"f_operator_type" db:"f_operator_type"`
	ExecutionMode   string `json:"f_execution_mode" db:"f_execution_mode"`
	Category        string `json:"f_category" db:"f_category"`
	Source          string `json:"f_source" db:"f_source"`
	ExecuteControl  string `json:"f_execute_control" db:"f_execute_control"`
	ExtendInfo      string `json:"f_extend_info" db:"f_extend_info"`
	CreateUser      string `json:"f_create_user" db:"f_create_user"`
	CreateTime      int64  `json:"f_create_time" db:"f_create_time"`
	UpdateUser      string `json:"f_update_user" db:"f_update_user"`
	UpdateTime      int64  `json:"f_update_time" db:"f_update_time"`
	IsInternal      bool   `json:"f_is_internal" db:"f_is_internal"`
	IsDataSource    bool   `json:"f_is_data_source" db:"f_is_data_source"` // Whether it is a data source operator.
}

// GetBizID Get business ID.
func (or *OperatorRegisterDB) GetBizID() string {
	return or.OperatorID
}

// IOperatorRegisterDB operator management database.
type IOperatorRegisterDB interface {
	// InsertOperator insert operator.
	// @directUpdate: Whether to update directly.
	InsertOperator(ctx context.Context, tx *sql.Tx, operator *OperatorRegisterDB) (opID string, err error)
	// SelectByNameAndStatus Gets an operator based on its name.
	SelectByNameAndStatus(ctx context.Context, tx *sql.Tx, name, status string) (has bool, operator *OperatorRegisterDB, err error)
	// SelectByOperatorIDAndVersion Gets an operator based on operator ID and version.
	SelectByOperatorIDAndVersion(ctx context.Context, operatorID, version string) (has bool, operator *OperatorRegisterDB, err error)
	// SelectByOperatorID based on operator ID.
	SelectByOperatorID(ctx context.Context, tx *sql.Tx, operatorID string) (has bool, operator *OperatorRegisterDB, err error)
	// CountByWhereClause counts the number of operators.
	CountByWhereClause(ctx context.Context, conditions map[string]interface{}) (count int64, err error)
	// SelectListPage paging query operator list.
	SelectListPage(ctx context.Context, conditions map[string]interface{}, sort *ormhelper.SortParams, cursor *ormhelper.CursorParams) (operatorList []*OperatorRegisterDB, err error)
	// UpdateOperatorStatus updates operator status.
	UpdateOperatorStatus(ctx context.Context, tx *sql.Tx, operator *OperatorRegisterDB, userID string) error
	// UpdateByOperatorID updates the operator based on the operator ID and version.
	UpdateByOperatorID(ctx context.Context, tx *sql.Tx, operator *OperatorRegisterDB) error
	// UpdateNameByOperatorID updates the operator name based on the operator ID.
	UpdateNameByOperatorID(ctx context.Context, tx *sql.Tx, operatorID, name string, updateUser string) error
	// DeleteByOperatorID based on operator ID.
	DeleteByOperatorID(ctx context.Context, tx *sql.Tx, operatorID string) error
	SelectByOperatorIDs(ctx context.Context, operatorIDs []string) (operatorList []*OperatorRegisterDB, err error)
	// SelectListByNamesAndStatus Gets an operator based on its name and status.
	SelectListByNamesAndStatus(ctx context.Context, names []string, status string) (operatorList []*OperatorRegisterDB, err error)
	// // CountByWhereClauseAndIDs counts the number of operators.
	// CountByWhereClauseAndIDs(ctx context.Context, conditions map[string]interface{}, operatorIDs []string) (count int64, err error)
	// // SelectListPageByIDs query operator list based on IN paging.
	// SelectListPageByIDs(ctx context.Context, pageSize, offset int, conditions map[string]interface{}, operatorIDs []string, orderBy, sortOrder string) (operatorList []*OperatorRegisterDB, err error)
}
