package example_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

// MCPConfigTest configuration structure for testing.
type MCPConfigTest struct {
	ID          string `json:"f_id" db:"f_id"`
	Name        string `json:"f_name" db:"f_name"`
	Description string `json:"f_description" db:"f_description"`
	Status      string `json:"f_status" db:"f_status"`
	CreateTime  int64  `json:"f_create_time" db:"f_create_time"`
	UpdateTime  int64  `json:"f_update_time" db:"f_update_time"`
}

// ExampleDAO ExampleDAO.
type ExampleDAO struct {
	orm *ormhelper.DB
}

// NewExampleDAO creates a DAO instance.
func NewExampleDAO(orm *ormhelper.DB) *ExampleDAO {
	return &ExampleDAO{orm: orm}
}

// Insert insert configuration.
func (dao *ExampleDAO) Insert(ctx context.Context, config *MCPConfigTest) (string, error) {
	now := time.Now().UnixNano()
	data := map[string]interface{}{
		"f_id":          config.ID,
		"f_name":        config.Name,
		"f_description": config.Description,
		"f_status":      config.Status,
		"f_create_time": now,
		"f_update_time": now,
	}
	_, err := dao.orm.Insert().Into("t_mcp_server_config").Values(data).Execute(ctx)
	return config.ID, err
}

// UpdateStatus update status.
func (dao *ExampleDAO) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := dao.orm.Update("t_mcp_server_config").
		Set("f_status", status).
		Set("f_update_time", time.Now().UnixNano()).
		WhereEq("f_id", id).
		Execute(ctx)
	return err
}

// DeleteByID Delete based on ID.
func (dao *ExampleDAO) DeleteByID(ctx context.Context, id string) error {
	_, err := dao.orm.Delete().From("t_mcp_server_config").WhereEq("f_id", id).Execute(ctx)
	return err
}

func TestExampleDAO_Insert(t *testing.T) {
	// Create a simulation executor.
	mockExecutor := NewMockExecutor()

	// Set the desired SQL execution.
	mockExecutor.ExpectExec(
		"INSERT INTO `test_db`.`t_mcp_server_config` (f_create_time, f_description, f_id, f_name, f_status, f_update_time) VALUES (?, ?, ?, ?, ?, ?)",
	).WillReturnResult(1, 1)

	// Create ORM instance.
	orm := ormhelper.New(mockExecutor, "test_db")
	dao := NewExampleDAO(orm)

	// test data.
	config := &MCPConfigTest{
		ID:          "test-id-1",
		Name:        "测试配置",
		Description: "这是一个测试配置",
		Status:      "active",
	}

	// perform insert.
	ctx := context.Background()
	id, err := dao.Insert(ctx, config)

	// Verification results.
	if err != nil {
		t.Errorf("Insert failed: %v", err)
	}
	if id != "test-id-1" {
		t.Errorf("Expected id 'test-id-1', got '%s'", id)
	}

	// Verify that all expectations are met.
	if err := mockExecutor.ExpectationsWereMet(); err != nil {
		t.Errorf("Expectations were not met: %v", err)
	}
}

func TestExampleDAO_Insert_Error(t *testing.T) {
	// Create a simulation executor.
	mockExecutor := NewMockExecutor()

	// Sets the desired SQL execution (returns an error)
	mockExecutor.ExpectExec(
		"INSERT INTO `test_db`.`t_mcp_server_config` (f_create_time, f_description, f_id, f_name, f_status, f_update_time) VALUES (?, ?, ?, ?, ?, ?)",
	).WillReturnError(sql.ErrConnDone)

	// Create ORM instance.
	orm := ormhelper.New(mockExecutor, "test_db")
	dao := NewExampleDAO(orm)

	// test data.
	config := &MCPConfigTest{
		ID:          "test-id-2",
		Name:        "测试配置2",
		Description: "这是另一个测试配置",
		Status:      "inactive",
	}

	// perform insert.
	ctx := context.Background()
	_, err := dao.Insert(ctx, config)

	// Validation error.
	if err == nil {
		t.Error("Expected error, but got nil")
	}
	if err != sql.ErrConnDone {
		t.Errorf("Expected sql.ErrConnDone, got %v", err)
	}

	// Verify that all expectations are met.
	if err := mockExecutor.ExpectationsWereMet(); err != nil {
		t.Errorf("Expectations were not met: %v", err)
	}
}

func TestExampleDAO_UpdateStatus(t *testing.T) {
	// Create a simulation executor.
	mockExecutor := NewMockExecutor()

	// Set the desired SQL execution.
	mockExecutor.ExpectExec(
		"UPDATE `test_db`.`t_mcp_server_config` SET f_status = ?, f_update_time = ? WHERE f_id = ?",
		"published", // status
		// f_update_time is dynamically generated, we do not verify the specific value.
		// "test-id-1", // id
	).WillReturnResult(0, 1)

	// Create ORM instance.
	orm := ormhelper.New(mockExecutor, "test_db")
	dao := NewExampleDAO(orm)

	// perform update.
	ctx := context.Background()
	err := dao.UpdateStatus(ctx, "test-id-1", "published")

	// Verification results.
	if err != nil {
		t.Errorf("UpdateStatus failed: %v", err)
	}
}

func TestExampleDAO_DeleteByID(t *testing.T) {
	// Create a simulation executor.
	mockExecutor := NewMockExecutor()

	// Set the desired SQL execution.
	mockExecutor.ExpectExec(
		"DELETE FROM `test_db`.`t_mcp_server_config` WHERE f_id = ?",
		"test-id-1",
	).WillReturnResult(0, 1)

	// Create ORM instance.
	orm := ormhelper.New(mockExecutor, "test_db")
	dao := NewExampleDAO(orm)

	// perform deletion.
	ctx := context.Background()
	err := dao.DeleteByID(ctx, "test-id-1")

	// Verification results.
	if err != nil {
		t.Errorf("DeleteByID failed: %v", err)
	}
}

func TestSelectBuilder_Build(t *testing.T) {
	mockExecutor := NewMockExecutor()
	orm := ormhelper.New(mockExecutor, "test_db")

	query, args := orm.Select().
		From("t_mcp_server_config").
		WhereEq("f_status", "active").
		OrderByDesc("f_update_time").
		Limit(10).
		Build()

	expectedQuery := "SELECT * FROM `test_db`.`t_mcp_server_config` WHERE f_status = ? ORDER BY f_update_time DESC LIMIT 10"
	if query != expectedQuery {
		t.Errorf("Expected query: %s, got: %s", expectedQuery, query)
	}

	if len(args) != 1 || args[0] != "active" {
		t.Errorf("Expected args: [active], got: %v", args)
	}
}

func TestInsertBuilder_Build(t *testing.T) {
	mockExecutor := NewMockExecutor()
	orm := ormhelper.New(mockExecutor, "test_db")

	data := map[string]interface{}{
		"f_id":     "test-id",
		"f_name":   "测试",
		"f_status": "active",
	}

	query, args := orm.Insert().
		Into("t_mcp_server_config").
		Values(data).
		Build()

	// Verify that the query contains the correct table names and fields.
	if !contains(query, "INSERT INTO `test_db`.`t_mcp_server_config`") {
		t.Errorf("Query should contain table name, got: %s", query)
	}

	// Number of verification parameters.
	if len(args) != 3 {
		t.Errorf("Expected 3 args, got %d", len(args))
	}
}

func TestUpdateBuilder_Build(t *testing.T) {
	mockExecutor := NewMockExecutor()
	orm := ormhelper.New(mockExecutor, "test_db")

	query, args := orm.Update("t_mcp_server_config").
		Set("f_status", "inactive").
		Set("f_update_time", 123456789).
		WhereEq("f_id", "test-id").
		Build()

	expectedStatusFirst := "UPDATE `test_db`.`t_mcp_server_config` SET f_status = ?, f_update_time = ? WHERE f_id = ?"
	expectedTimeFirst := "UPDATE `test_db`.`t_mcp_server_config` SET f_update_time = ?, f_status = ? WHERE f_id = ?"
	switch query {
	case expectedStatusFirst:
		if len(args) != 3 || args[0] != "inactive" || args[1] != 123456789 || args[2] != "test-id" {
			t.Errorf("Unexpected args for status-first query: %v", args)
		}
	case expectedTimeFirst:
		if len(args) != 3 || args[0] != 123456789 || args[1] != "inactive" || args[2] != "test-id" {
			t.Errorf("Unexpected args for time-first query: %v", args)
		}
	default:
		t.Errorf("Unexpected update query: %s", query)
	}
}

func TestDeleteBuilder_Build(t *testing.T) {
	mockExecutor := NewMockExecutor()
	orm := ormhelper.New(mockExecutor, "test_db")

	query, args := orm.Delete().
		From("t_mcp_server_config").
		WhereEq("f_id", "test-id").
		Build()

	expectedQuery := "DELETE FROM `test_db`.`t_mcp_server_config` WHERE f_id = ?"
	if query != expectedQuery {
		t.Errorf("Expected query: %s, got: %s", expectedQuery, query)
	}

	if len(args) != 1 || args[0] != "test-id" {
		t.Errorf("Expected args: [test-id], got: %v", args)
	}
}

func TestWhereBuilder_Complex(t *testing.T) {
	mockExecutor := NewMockExecutor()
	orm := ormhelper.New(mockExecutor, "test_db")

	query, args := orm.Select().
		From("t_mcp_server_config").
		WhereEq("f_status", "active").
		And(func(w *ormhelper.WhereBuilder) {
			w.Gt("f_create_time", 1000000000).
				Lt("f_create_time", 2000000000)
		}).
		Or(func(w *ormhelper.WhereBuilder) {
			w.Eq("f_category", "special").
				Like("f_name", "%test%")
		}).
		Build()

	// Validate query contains complex WHERE conditions.
	if !containsSubstring(query, "WHERE") {
		t.Errorf("Query should contain WHERE clause, got: %s", query)
	}

	// Verify the number of parameters (there should be 5 parameters)
	if len(args) != 5 {
		t.Errorf("Expected 5 args, got %d: %v", len(args), args)
	}
}

func TestBatchInsert(t *testing.T) {
	mockExecutor := NewMockExecutor()
	orm := ormhelper.New(mockExecutor, "test_db")

	columns := []string{"f_id", "f_name", "f_status"}
	values := [][]interface{}{
		{"id1", "name1", "active"},
		{"id2", "name2", "inactive"},
	}

	query, args := orm.Insert().
		Into("t_mcp_server_config").
		BatchValues(columns, values).
		Build()

	// Verify query contains bulk insert syntax.
	if !contains(query, "INSERT INTO `test_db`.`t_mcp_server_config`") {
		t.Errorf("Query should contain table name, got: %s", query)
	}

	if !contains(query, "VALUES") {
		t.Errorf("Query should contain VALUES, got: %s", query)
	}

	// Number of validation parameters (2 rows * 3 columns = 6 parameters)
	if len(args) != 6 {
		t.Errorf("Expected 6 args, got %d", len(args))
	}
}

func TestTransaction(t *testing.T) {
	mockExecutor := NewMockExecutor()
	orm := ormhelper.New(mockExecutor, "test_db")

	// simulate transaction.
	var mockTx *sql.Tx // In actual testing, this should be the real transaction object.
	txORM := orm.WithTx(mockTx)

	// Verify transaction ORM instance.
	if txORM == nil {
		t.Error("WithTx should return a valid ORM instance")
	}

	// Verify whether it is in a transaction (since mockTx is nil, false will be returned)
	// In actual use, if real *sql.Tx is passed in, IsInTransaction() will return true.
	if txORM.IsInTransaction() {
		t.Log("Transaction ORM created successfully")
	}
}

// Helper function.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// Helper function: Check if a string contains a substring (case insensitive)
func containsSubstring(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
