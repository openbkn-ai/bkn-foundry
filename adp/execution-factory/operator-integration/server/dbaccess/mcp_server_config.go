package dbaccess

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/db"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/db/sqlx"
)

type mcpServerConfigDB struct {
	dbPool *sqlx.DB
	logger interfaces.Logger
	dbName string
	orm    *ormhelper.DB
}

var (
	mcOnce sync.Once
	mc     model.DBMCPServerConfig
)

const (
	// tbMCPServerConfig MCP Server configuration table name.
	tbMCPServerConfig = "t_mcp_server_config"
)

// NewMCPServerConfigDBSingleton creates an MCP Server configuration database access object singleton.
func NewMCPServerConfigDBSingleton() model.DBMCPServerConfig {
	confLoader := config.NewConfigLoader()
	dbPool := db.NewDBPool()
	dbName := confLoader.GetDBName()
	logger := confLoader.GetLogger()

	mcOnce.Do(func() {
		// Use a basic ORM instance, no logging functionality included.
		orm := ormhelper.New(dbPool, dbName)

		mc = &mcpServerConfigDB{
			dbPool: dbPool,
			logger: logger,
			dbName: dbName,
			orm:    orm,
		}
	})
	return mc
}

// Insert Insert MCP Server configuration.
func (m *mcpServerConfigDB) Insert(ctx context.Context, tx *sql.Tx, config *model.MCPServerConfigDB) (id string, err error) {
	now := time.Now().UnixNano()
	MCPID := config.MCPID
	if MCPID == "" {
		generatedID, generateErr := uuid.NewV7()
		if generateErr != nil {
			return "", generateErr
		}
		MCPID = generatedID.String()
	}

	// The default version number is 1.
	if config.Version == 0 {
		config.Version = 1
	}
	config.MCPID = MCPID
	config.CreateTime = now
	config.UpdateTime = now

	orm := m.orm
	if tx != nil {
		orm = m.orm.WithTx(tx)
	}

	// Insert data using ORM Helper.
	_, err = orm.Insert().Into(tbMCPServerConfig).Values(map[string]interface{}{
		"f_mcp_id":        config.MCPID,
		"f_name":          config.Name,
		"f_description":   config.Description,
		"f_mode":          config.Mode,
		"f_url":           config.URL,
		"f_headers":       config.Headers,
		"f_command":       config.Command,
		"f_env":           config.Env,
		"f_args":          config.Args,
		"f_status":        config.Status,
		"f_category":      config.Category,
		"f_source":        config.Source,
		"f_create_user":   config.CreateUser,
		"f_create_time":   config.CreateTime,
		"f_update_user":   config.UpdateUser,
		"f_update_time":   config.UpdateTime,
		"f_is_internal":   config.IsInternal,
		"f_creation_type": config.CreationType,
		"f_version":       config.Version,
	}).Execute(ctx)

	if err != nil {
		return "", err
	}
	return config.MCPID, nil
}

// SelectByID queries MCP Server configuration based on ID.
func (m *mcpServerConfigDB) SelectByID(ctx context.Context, tx *sql.Tx, mcpID string) (config *model.MCPServerConfigDB, err error) {
	config = &model.MCPServerConfigDB{}

	orm := m.orm
	if tx != nil {
		orm = m.orm.WithTx(tx)
	}

	err = orm.Select().From(tbMCPServerConfig).WhereEq("f_mcp_id", mcpID).First(ctx, config)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return config, nil
}

// UpdateByID updates MCP Server configuration based on ID.
func (m *mcpServerConfigDB) UpdateByID(ctx context.Context, tx *sql.Tx, config *model.MCPServerConfigDB) error {
	config.UpdateTime = time.Now().UnixNano()

	orm := m.orm
	if tx != nil {
		orm = m.orm.WithTx(tx)
	}

	_, err := orm.Update(tbMCPServerConfig).SetData(map[string]interface{}{
		"f_name":        config.Name,
		"f_description": config.Description,
		"f_mode":        config.Mode,
		"f_url":         config.URL,
		"f_headers":     config.Headers,
		"f_command":     config.Command,
		"f_env":         config.Env,
		"f_args":        config.Args,
		"f_status":      config.Status,
		"f_category":    config.Category,
		"f_source":      config.Source,
		"f_update_user": config.UpdateUser,
		"f_update_time": config.UpdateTime,
		"f_version":     config.Version,
	}).WhereEq("f_mcp_id", config.MCPID).Execute(ctx)
	return err
}

// UpdateStatus updates MCP Server configuration status.
func (m *mcpServerConfigDB) UpdateStatus(ctx context.Context, tx *sql.Tx, mcpID, status, updateUser string, version int) error {
	orm := m.orm
	if tx != nil {
		orm = m.orm.WithTx(tx)
	}
	_, err := orm.Update(tbMCPServerConfig).SetData(map[string]interface{}{
		"f_status":      status,
		"f_update_user": updateUser,
		"f_update_time": time.Now().UnixNano(),
		"f_version":     version,
	}).WhereEq("f_mcp_id", mcpID).Execute(ctx)
	return err
}

// DeleteByID Delete MCP Server configuration based on ID.
func (m *mcpServerConfigDB) DeleteByID(ctx context.Context, tx *sql.Tx, mcpID string) error {
	orm := m.orm
	if tx != nil {
		orm = m.orm.WithTx(tx)
	}

	_, err := orm.Delete().From(tbMCPServerConfig).WhereEq("f_mcp_id", mcpID).Execute(ctx)
	return err
}

// BatchDelete Batch delete MCP Server configuration.
func (m *mcpServerConfigDB) BatchDelete(ctx context.Context, tx *sql.Tx, ids []string) error {
	orm := m.orm
	if tx != nil {
		orm = m.orm.WithTx(tx)
	}
	_, err := orm.Delete().From(tbMCPServerConfig).WhereIn("f_id", utils.SliceToInterface(ids)...).Execute(ctx)
	return err
}

// SelectListPage queries the MCP Server configuration list with pagination.
func (m *mcpServerConfigDB) SelectListPage(ctx context.Context, tx *sql.Tx, filter map[string]interface{}, sort *ormhelper.SortParams, cursor *ormhelper.CursorParams) (configList []*model.MCPServerConfigDB, err error) {
	query := m.orm.Select().From(tbMCPServerConfig)
	query = m.applyFilterConditions(query, filter)
	if cursor != nil {
		query = query.Cursor(cursor)
	}
	// Handle sorting and pagination.
	query = query.Sort(sort)
	if filter["all"] == nil || filter["all"] == false {
		pageSize, ok := filter["limit"].(int)
		if ok {
			query.Limit(pageSize)
		}
		offset, ok := filter["offset"].(int)
		if ok {
			query.Offset(offset)
		}
	}
	// Execute query.
	configList = []*model.MCPServerConfigDB{}
	err = query.Get(ctx, &configList)
	return configList, err
}

// SelectByName Query MCP Server configuration based on name.
func (m *mcpServerConfigDB) SelectByName(ctx context.Context, tx *sql.Tx, name string, status []string) (config *model.MCPServerConfigDB, err error) {
	config = &model.MCPServerConfigDB{}

	orm := m.orm
	if tx != nil {
		orm = m.orm.WithTx(tx)
	}

	args := []interface{}{}
	for _, v := range status {
		args = append(args, v)
	}
	err = orm.Select().From(tbMCPServerConfig).WhereEq("f_name", name).WhereIn("f_status", args...).First(ctx, config)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return config, nil
}

// CountByWhereClause counts quantities based on conditions.
func (m *mcpServerConfigDB) CountByWhereClause(ctx context.Context, tx *sql.Tx, filter map[string]interface{}) (count int64, err error) {
	orm := m.orm
	if tx != nil {
		orm = m.orm.WithTx(tx)
	}

	query := orm.Select().From(tbMCPServerConfig)
	query = m.applyFilterConditions(query, filter)

	count, err = query.Count(ctx)
	return count, err
}

// applyFilterConditions applies filter conditions to the query.
func (m *mcpServerConfigDB) applyFilterConditions(query *ormhelper.SelectBuilder, filter map[string]interface{}) *ormhelper.SelectBuilder {
	if filter == nil {
		return query
	}
	// Supported query conditions.
	if filter["name"] != nil {
		name := filter["name"].(string)
		query = query.WhereLike("f_name", "%"+name+"%")
	}
	if filter["status"] != nil {
		query = query.WhereEq("f_status", filter["status"])
	}
	if filter["category"] != nil {
		query = query.WhereEq("f_category", filter["category"])
	}
	if filter["source"] != nil {
		query = query.WhereEq("f_source", filter["source"])
	}
	if filter["createUser"] != nil {
		query = query.WhereEq("f_create_user", filter["createUser"])
	}
	if filter["mode"] != nil {
		query = query.WhereEq("f_mode", filter["mode"])
	}
	if filter["in"] != nil {
		queryInParams, ok := filter["in"].([]string)
		if !ok || len(queryInParams) == 0 {
			return query
		}
		arrs := []interface{}{}
		for _, v := range queryInParams {
			if v != "" {
				arrs = append(arrs, v)
			}
		}
		if len(arrs) > 0 {
			query = query.WhereIn("f_mcp_id", arrs...)
		}
	}
	return query
}

// SelectByIDs queries the MCP Server configuration list based on the ID list.
func (m *mcpServerConfigDB) SelectByMCPIDs(ctx context.Context, mcpIDs []string) (configList []*model.MCPServerConfigDB, err error) {
	orm := m.orm
	configList = []*model.MCPServerConfigDB{}
	err = orm.Select().From(tbMCPServerConfig).WhereIn("f_mcp_id", utils.SliceToInterface(mcpIDs)...).Get(ctx, &configList)
	return configList, err
}

// SelectListByNamesAndStatus gets lists in batches based on names and statuses.
func (m *mcpServerConfigDB) SelectListByNamesAndStatus(ctx context.Context, names []string, status ...string) (configList []*model.MCPServerConfigDB, err error) {
	configList = []*model.MCPServerConfigDB{}
	query := m.orm.Select().From(tbMCPServerConfig).WhereIn("f_name", utils.SliceToInterface(names)...)
	if len(status) > 0 {
		query = query.WhereIn("f_status", utils.SliceToInterface(status)...)
	}
	err = query.Get(ctx, &configList)
	return configList, err
}
