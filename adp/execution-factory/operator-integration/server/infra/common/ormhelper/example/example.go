package example

import (
	"context"
	"database/sql"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

// Example: How to use ORM Helper in an existing project.

// ExampleUsage shows the basic usage of ORM Helper.
func ExampleUsage() {
	// 1. Initialize ORM Helper.
	// Assume that you already have a database connection pool dbPool and a database name dbName.
	var dbPool ormhelper.Executor // Your database connection pool implements the Executor interface.
	dbName := "your_database_name"

	orm := ormhelper.New(dbPool, dbName)

	// 2. Basic query operations.
	ctx := context.Background()

	// SELECT query example.
	var configs []MCPConfigExample
	err := orm.Select().
		From("t_mcp_server_config").
		WhereEq("f_status", "active").
		OrderByDesc("f_update_time").
		Limit(10). //nolint:mnd
		Get(ctx, &configs)
	if err != nil {
		return
	}

	// Single record query.
	var config MCPConfigExample
	err = orm.Select().
		From("t_mcp_server_config").
		WhereEq("f_id", "some-id").
		First(ctx, &config)
	if err != nil {
		return
	}

	// Statistical quantity.
	count, err := orm.Select().
		From("t_mcp_server_config").
		WhereEq("f_status", "active").
		Count(ctx)
	if err != nil {
		return
	}
	// Example of using count variable (can be used to return to the front end or log in actual projects)
	_ = count // You can use the count variable here, for example: fmt.Printf("Found %d records\n", count)

	// 3. Insert operation.
	data := map[string]interface{}{
		"f_id":          "new-config-id",
		"f_name":        "新配置",
		"f_description": "这是一个新的配置",
		"f_status":      "active",
		"f_create_time": time.Now().UnixNano(),
		"f_update_time": time.Now().UnixNano(),
	}

	result, err := orm.Insert().
		Into("t_mcp_server_config").
		Values(data).
		Execute(ctx)
	if err != nil {
		return
	}

	// Get the inserted ID (if it is an auto-incrementing primary key)
	lastID, _ := result.LastInsertId()
	_ = lastID

	// 4. Batch insert.
	columns := []string{"f_id", "f_name", "f_status", "f_create_time", "f_update_time"}
	now := time.Now().UnixNano()
	values := [][]interface{}{
		{"config-1", "配置1", "active", now, now},
		{"config-2", "配置2", "active", now, now},
		{"config-3", "配置3", "inactive", now, now},
	}

	_, err = orm.Insert().
		Into("t_mcp_server_config").
		BatchValues(columns, values).
		Execute(ctx)
	if err != nil {
		return
	}

	// 5. Update operation.
	_, err = orm.Update("t_mcp_server_config").
		Set("f_status", "inactive").
		Set("f_update_time", time.Now().UnixNano()).
		WhereEq("f_id", "some-id").
		Execute(ctx)
	if err != nil {
		return
	}

	// 6. Delete operation.
	_, err = orm.Delete().
		From("t_mcp_server_config").
		WhereEq("f_id", "some-id").
		Execute(ctx)
	if err != nil {
		return
	}
}

// ExampleTransactionUsage shows how to use transactions.
func ExampleTransactionUsage() {
	var dbPool ormhelper.Executor
	dbName := "your_database_name"
	orm := ormhelper.New(dbPool, dbName)

	ctx := context.Background()

	// Method 1: Use existing transactions (compatible with existing code)
	var tx *sql.Tx // your transaction object.
	txORM := orm.WithTx(tx)

	// Perform operations within a transaction.
	_, err := txORM.Insert().
		Into("t_mcp_server_config").
		Values(map[string]interface{}{
			"f_id":   "tx-config-1",
			"f_name": "事务配置1",
		}).
		Execute(ctx)
	if err != nil {
		// Handle errors, which may require rolling back the transaction.
		return
	}

	// Continue to perform other operations in the same transaction.
	_, err = txORM.Update("t_mcp_server_config").
		Set("f_status", "active").
		WhereEq("f_id", "tx-config-1").
		Execute(ctx)
	if err != nil {
		// Handle errors, which may require rolling back the transaction.
		return
	}
}

// ExampleComplexQuery demonstrates the construction of complex queries.
func ExampleComplexQuery() {
	var dbPool ormhelper.Executor
	dbName := "your_database_name"
	orm := ormhelper.New(dbPool, dbName)

	ctx := context.Background()

	// Complex WHERE conditions.
	var configs []MCPConfigExample
	err := orm.Select().
		From("t_mcp_server_config").
		WhereEq("f_status", "active").
		And(func(w *ormhelper.WhereBuilder) {
			w.Gt("f_create_time", time.Now().UnixMilli()).
				Lt("f_create_time", time.Now().Add(time.Minute).UnixMilli())
		}).
		Or(func(w *ormhelper.WhereBuilder) {
			w.Eq("f_category", "special").
				Like("f_name", "%test%")
		}).
		OrderByDesc("f_update_time").
		Limit(20).  //nolint:mnd
		Offset(10). //nolint:mnd
		Get(ctx, &configs)
	if err != nil {
		return
	}

	// JOIN query example.
	query, args := orm.Select("c.f_id", "c.f_name", "h.f_version").
		From("t_mcp_server_config c").
		LeftJoin("t_mcp_server_release_history h", "c.f_id = h.f_mcp_id").
		WhereEq("c.f_status", "active").
		OrderByDesc("h.f_create_time").
		Build()

	// Execute native SQL queries.
	rows, err := orm.GetExecutor().QueryContext(ctx, query, args...)
	if err != nil {
		return
	}
	if rows.Err() != nil {
		_ = rows.Close()
		return
	}
	_ = rows.Close()
	// Processing results...
}

// MCPConfigExample sample configuration structure.
type MCPConfigExample struct {
	ID          string `json:"f_id" db:"f_id"`
	Name        string `json:"f_name" db:"f_name"`
	Description string `json:"f_description" db:"f_description"`
	Status      string `json:"f_status" db:"f_status"`
	Category    string `json:"f_category" db:"f_category"`
	CreateTime  int64  `json:"f_create_time" db:"f_create_time"`
	UpdateTime  int64  `json:"f_update_time" db:"f_update_time"`
}

// ConfigDAO sample DAO implementation, showing how to organize code in actual projects.
type ConfigDAO struct {
	orm *ormhelper.DB
}

// NewConfigDAO creates DAO instance.
func NewConfigDAO(orm *ormhelper.DB) *ConfigDAO {
	return &ConfigDAO{orm: orm}
}

// GetActiveConfigs Gets the active configuration list.
func (dao *ConfigDAO) GetActiveConfigs(ctx context.Context) ([]*MCPConfigExample, error) {
	var configs []*MCPConfigExample
	err := dao.orm.Select().
		From("t_mcp_server_config").
		WhereEq("f_status", "active").
		OrderByDesc("f_update_time").
		Get(ctx, &configs)
	return configs, err
}

// GetConfigByID Gets configuration based on ID.
func (dao *ConfigDAO) GetConfigByID(ctx context.Context, id string) (*MCPConfigExample, error) {
	config := &MCPConfigExample{}
	err := dao.orm.Select().
		From("t_mcp_server_config").
		WhereEq("f_id", id).
		First(ctx, config)
	if err != nil {
		return nil, err
	}
	return config, nil
}

// CreateConfig creates a new configuration.
func (dao *ConfigDAO) CreateConfig(ctx context.Context, config *MCPConfigExample) error {
	now := time.Now().UnixNano()
	data := map[string]interface{}{
		"f_id":          config.ID,
		"f_name":        config.Name,
		"f_description": config.Description,
		"f_status":      config.Status,
		"f_category":    config.Category,
		"f_create_time": now,
		"f_update_time": now,
	}

	_, err := dao.orm.Insert().
		Into("t_mcp_server_config").
		Values(data).
		Execute(ctx)
	return err
}

// UpdateConfigStatus updates configuration status.
func (dao *ConfigDAO) UpdateConfigStatus(ctx context.Context, id, status string) error {
	_, err := dao.orm.Update("t_mcp_server_config").
		Set("f_status", status).
		Set("f_update_time", time.Now().UnixNano()).
		WhereEq("f_id", id).
		Execute(ctx)
	return err
}

// DeleteConfig delete configuration.
func (dao *ConfigDAO) DeleteConfig(ctx context.Context, id string) error {
	_, err := dao.orm.Delete().
		From("t_mcp_server_config").
		WhereEq("f_id", id).
		Execute(ctx)
	return err
}

// GetConfigsPage paging query configuration.
func (dao *ConfigDAO) GetConfigsPage(ctx context.Context, page, pageSize int, status string) ([]*MCPConfigExample, int64, error) {
	// Total number of queries.
	countBuilder := dao.orm.Select().From("t_mcp_server_config")
	if status != "" {
		countBuilder.WhereEq("f_status", status)
	}
	total, err := countBuilder.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	// Page query.
	var configs []*MCPConfigExample
	queryBuilder := dao.orm.Select().
		From("t_mcp_server_config").
		OrderByDesc("f_update_time").
		Limit(pageSize).
		Offset((page - 1) * pageSize)

	if status != "" {
		queryBuilder.WhereEq("f_status", status)
	}

	err = queryBuilder.Get(ctx, &configs)
	return configs, total, err
}

// BatchCreateConfigs batch creation configuration.
func (dao *ConfigDAO) BatchCreateConfigs(ctx context.Context, configs []*MCPConfigExample) error {
	if len(configs) == 0 {
		return nil
	}

	columns := []string{"f_id", "f_name", "f_description", "f_status", "f_category", "f_create_time", "f_update_time"}
	values := make([][]interface{}, len(configs))

	now := time.Now().UnixNano()
	for i, config := range configs {
		values[i] = []interface{}{
			config.ID,
			config.Name,
			config.Description,
			config.Status,
			config.Category,
			now,
			now,
		}
	}

	_, err := dao.orm.Insert().
		Into("t_mcp_server_config").
		BatchValues(columns, values).
		Execute(ctx)
	return err
}
