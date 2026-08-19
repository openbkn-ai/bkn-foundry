// Package dbaccess
// @file op_register.go
// @description: Implement operator registration database operations.
package dbaccess

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/db"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/db/sqlx"
	"github.com/pkg/errors"
)

type operatorManagerDB struct {
	dbPool *sqlx.DB
	logger interfaces.Logger
	dbName string
	orm    *ormhelper.DB
}

var (
	once sync.Once
	om   model.IOperatorRegisterDB
)

const (
	// tbOperatorRegistry table name.
	tbOperatorRegistry = "t_op_registry"
)

// NewOperatorManagerDB creates an operator management database.
func NewOperatorManagerDB() model.IOperatorRegisterDB {
	once.Do(func() {
		confLoader := config.NewConfigLoader()
		dbPool := db.NewDBPool()
		dbName := confLoader.GetDBName()
		orm := ormhelper.New(dbPool, dbName)
		om = &operatorManagerDB{
			dbPool: dbPool,
			logger: confLoader.GetLogger(),
			dbName: dbName,
			orm:    orm,
		}
	})
	return om
}

// InsertOperator insert operator.
func (o *operatorManagerDB) InsertOperator(ctx context.Context, tx *sql.Tx, operator *model.OperatorRegisterDB) (opID string, err error) {
	if operator.OperatorID == "" {
		operator.OperatorID = uuid.NewString()
	}
	opID = operator.OperatorID
	orm := o.orm
	if tx != nil {
		orm = o.orm.WithTx(tx)
	}
	// Insert data using ormhelper.
	row, err := orm.Insert().Into(tbOperatorRegistry).Values(map[string]interface{}{
		"f_op_id":            operator.OperatorID,
		"f_name":             operator.Name,
		"f_metadata_version": operator.MetadataVersion,
		"f_metadata_type":    operator.MetadataType,
		"f_status":           operator.Status,
		"f_operator_type":    operator.OperatorType,
		"f_execution_mode":   operator.ExecutionMode,
		"f_category":         operator.Category,
		"f_execute_control":  operator.ExecuteControl,
		"f_extend_info":      operator.ExtendInfo,
		"f_create_user":      operator.CreateUser,
		"f_create_time":      operator.CreateTime,
		"f_update_user":      operator.UpdateUser,
		"f_update_time":      operator.UpdateTime,
		"f_source":           operator.Source,
		"f_is_internal":      operator.IsInternal,
		"f_is_data_source":   operator.IsDataSource,
	}).Execute(ctx)
	if err != nil {
		err = errors.Wrapf(err, "insert operator failed")
		return
	}
	ok, err := checkAffected(row)
	if err != nil {
		err = fmt.Errorf("insert operator failed, err: %v", err)
		return
	}
	if !ok {
		err = errors.New("insert operator failed, no affected rows")
	}
	return
}

// SelectByNameAndStatus Gets an operator based on its name.
func (o *operatorManagerDB) SelectByNameAndStatus(ctx context.Context, tx *sql.Tx, name, status string) (has bool, operator *model.OperatorRegisterDB, err error) {
	orm := o.orm
	if tx != nil {
		orm = o.orm.WithTx(tx)
	}
	operator = &model.OperatorRegisterDB{}
	// Use ormhelper to query data.
	err = orm.Select().From(tbOperatorRegistry).WhereEq("f_name", name).WhereEq("f_status", status).First(ctx, operator)
	has, err = checkHasQueryErr(err)
	return
}

// SelectByOperatorID Gets status based on operator ID.
func (o *operatorManagerDB) SelectByOperatorID(ctx context.Context, tx *sql.Tx, operatorID string) (has bool, operator *model.OperatorRegisterDB, err error) {
	orm := o.orm
	if tx != nil {
		orm = o.orm.WithTx(tx)
	}
	operator = &model.OperatorRegisterDB{}
	err = orm.Select().From(tbOperatorRegistry).WhereEq("f_op_id", operatorID).First(ctx, operator)
	has, err = checkHasQueryErr(err)
	return
}

// SelectByOperatorIDAndVersion Gets an operator based on operator ID and version.
func (o *operatorManagerDB) SelectByOperatorIDAndVersion(ctx context.Context, operatorID, version string) (has bool, operator *model.OperatorRegisterDB, err error) {
	orm := o.orm
	operator = &model.OperatorRegisterDB{}
	err = orm.Select().From(tbOperatorRegistry).WhereEq("f_op_id", operatorID).WhereEq("f_metadata_version", version).First(ctx, operator)
	has, err = checkHasQueryErr(err)
	return
}

// CountByWhereClause queries the number of operators based on conditions.
func (o *operatorManagerDB) CountByWhereClause(ctx context.Context, conditions map[string]interface{}) (count int64, err error) {
	orm := o.orm
	query := orm.Select().From(tbOperatorRegistry)
	query = o.buildQueryConditions(query, conditions)
	count, err = query.Count(ctx)
	return count, err
}

// SelectListPage paging query operator list.
func (o *operatorManagerDB) SelectListPage(ctx context.Context, conditions map[string]interface{},
	sort *ormhelper.SortParams, cursor *ormhelper.CursorParams) (operatorList []*model.OperatorRegisterDB, err error) {
	orm := o.orm
	query := orm.Select().From(tbOperatorRegistry)
	query = o.buildQueryConditions(query, conditions)
	query.Cursor(cursor)
	query.Sort(sort)
	// Handle pagination.
	if conditions["all"] == nil || conditions["all"] == false {
		pageSize, ok := conditions["limit"].(int)
		if ok {
			query.Limit(pageSize)
		}
		offset, ok := conditions["offset"].(int)
		if ok {
			query.Offset(offset)
		}
	}
	operatorList = []*model.OperatorRegisterDB{}
	err = query.Get(ctx, &operatorList)
	return operatorList, err
}

func (o *operatorManagerDB) buildQueryConditions(query *ormhelper.SelectBuilder, conditions map[string]interface{}) *ormhelper.SelectBuilder {
	if len(conditions) == 0 {
		return query
	}
	if conditions["create_user"] != nil {
		query = query.WhereEq("f_create_user", conditions["create_user"])
	}
	if conditions["name"] != nil {
		name := conditions["name"].(string)
		query = query.WhereLike("f_name", "%"+name+"%")
	}
	if conditions["status"] != nil {
		query = query.WhereEq("f_status", conditions["status"])
	}
	if conditions["category"] != nil {
		query = query.WhereEq("f_category", conditions["category"])
	}
	if conditions["operator_type"] != nil {
		query = query.WhereEq("f_operator_type", conditions["operator_type"])
	}
	if conditions["is_data_source"] != nil {
		query = query.WhereEq("f_is_data_source", conditions["is_data_source"])
	}
	if conditions["in"] != nil {
		operatorIDs := conditions["in"].([]string)
		if len(operatorIDs) == 0 {
			return query
		}
		var arr []interface{}
		for _, id := range operatorIDs {
			if id != "" {
				arr = append(arr, id)
			}
		}
		if len(arr) > 0 {
			query = query.WhereIn("f_op_id", arr...)
		}
	}
	return query
}

// DeleteByOperatorID Delete an operator based on its ID.
func (o *operatorManagerDB) DeleteByOperatorID(ctx context.Context, tx *sql.Tx, operatorID string) error {
	orm := o.orm
	if tx != nil {
		orm = o.orm.WithTx(tx)
	}
	_, err := orm.Delete().From(tbOperatorRegistry).
		WhereEq("f_op_id", operatorID).Execute(ctx)
	return err
}

// UpdateOperatorStatus updates operator status.
func (o *operatorManagerDB) UpdateOperatorStatus(ctx context.Context, tx *sql.Tx, operator *model.OperatorRegisterDB, userID string) error {
	orm := o.orm
	if tx != nil {
		orm = o.orm.WithTx(tx)
	}
	_, err := orm.Update(tbOperatorRegistry).SetData(map[string]interface{}{
		"f_status":      operator.Status,
		"f_update_user": userID,
		"f_update_time": time.Now().UnixNano(),
	}).WhereEq("f_op_id", operator.OperatorID).
		WhereEq("f_metadata_version", operator.MetadataVersion).Execute(ctx)
	return err
}

// UpdateNameByOperatorID updates the operator name based on the operator ID.
func (o *operatorManagerDB) UpdateNameByOperatorID(ctx context.Context, tx *sql.Tx, operatorID, name, updateUser string) error {
	orm := o.orm
	if tx != nil {
		orm = o.orm.WithTx(tx)
	}
	_, err := orm.Update(tbOperatorRegistry).SetData(map[string]interface{}{
		"f_name":        name,
		"f_update_user": updateUser,
		"f_update_time": time.Now().UnixNano(),
	}).WhereEq("f_op_id", operatorID).Execute(ctx)
	return err
}

// UpdateMetadataVersionByOperatorID Updates the operator metadata version based on the operator ID.
func (o *operatorManagerDB) UpdateMetadataVersionByOperatorID(ctx context.Context, tx *sql.Tx, operatorID, version string) error {
	orm := o.orm
	if tx != nil {
		orm = o.orm.WithTx(tx)
	}
	_, err := orm.Update(tbOperatorRegistry).SetData(map[string]interface{}{
		"f_metadata_version": version,
	}).WhereEq("f_op_id", operatorID).Execute(ctx)
	return err
}

// UpdateByOperatorID updates the operator based on the operator ID.
func (o *operatorManagerDB) UpdateByOperatorID(ctx context.Context, tx *sql.Tx, operator *model.OperatorRegisterDB) error {
	orm := o.orm
	if tx != nil {
		orm = o.orm.WithTx(tx)
	}
	_, err := orm.Update(tbOperatorRegistry).SetData(map[string]interface{}{
		"f_metadata_version": operator.MetadataVersion,
		"f_status":           operator.Status,
		"f_operator_type":    operator.OperatorType,
		"f_execution_mode":   operator.ExecutionMode,
		"f_name":             operator.Name,
		"f_category":         operator.Category,
		"f_execute_control":  operator.ExecuteControl,
		"f_extend_info":      operator.ExtendInfo,
		"f_update_user":      operator.UpdateUser,
		"f_update_time":      time.Now().UnixNano(),
		"f_source":           operator.Source,
		"f_is_internal":      operator.IsInternal,
		"f_is_data_source":   operator.IsDataSource,
	}).WhereEq("f_op_id", operator.OperatorID).Execute(ctx)
	return err
}

// SelectByOperatorIDs batch query operator information.
func (o *operatorManagerDB) SelectByOperatorIDs(ctx context.Context, operatorIDs []string) (operatorList []*model.OperatorRegisterDB, err error) {
	operatorList = []*model.OperatorRegisterDB{}
	orm := o.orm
	args := []interface{}{}
	for _, id := range operatorIDs {
		args = append(args, id)
	}
	if len(args) == 0 {
		return
	}
	err = orm.Select().From(tbOperatorRegistry).WhereIn("f_op_id", args...).Get(ctx, &operatorList)
	return
}

// SelectListByNamesAndStatus gets the operator list based on the operator name and status.
func (o *operatorManagerDB) SelectListByNamesAndStatus(ctx context.Context, names []string, status string) (operatorList []*model.OperatorRegisterDB, err error) {
	orm := o.orm
	args := []interface{}{}
	for _, name := range names {
		args = append(args, name)
	}
	operatorList = []*model.OperatorRegisterDB{}
	err = orm.Select().From(tbOperatorRegistry).WhereIn("f_name", args...).WhereEq("f_status", status).Get(ctx, &operatorList)
	return
}
