// Package example A simple usage example.
package example

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

var (
	defaultLimit = 10
)

// BasicUsageExample Basic usage example.
func BasicUsageExample() {
	// Assuming you already have a database connection.
	var orm *ormhelper.DB
	db, _ := sql.Open("mysql", "dsn")

	// nil is used as an example here. Please pass in a real database connection when using it in practice.
	// var db *sql.DB
	orm = ormhelper.New(db, "example_db")

	ctx := context.Background()

	// 1. Insert data example.
	insertExample(ctx, orm)

	// 2. Query data example.
	queryExample(ctx, orm)

	// 3. Update data example.
	updateExample(ctx, orm)

	// 4. Delete data example.
	deleteExample(ctx, orm)

	// 5. Paging query example.
	paginationExample(ctx, orm)

	// 6. Transaction usage examples.
	transactionExample(ctx, orm)
}

// Insert data example.
func insertExample(ctx context.Context, orm *ormhelper.DB) {
	// Single insert.
	_, err := orm.Insert().Into("users").Values(map[string]interface{}{
		"f_id":          "user-001",
		"f_name":        "张三",
		"f_email":       "zhangsan@example.com",
		"f_create_time": time.Now().Unix(),
	}).Execute(ctx)
	if err != nil { // handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}

	// Batch insert.
	columns := []string{"f_id", "f_name", "f_email", "f_create_time"}
	values := [][]interface{}{
		{"user-002", "李四", "lisi@example.com", time.Now().Unix()},
		{"user-003", "王五", "wangwu@example.com", time.Now().Unix()},
	}
	_, err = orm.Insert().Into("users").BatchValues(columns, values).Execute(ctx)
	if err != nil { // handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}
}

// Query data example.
func queryExample(ctx context.Context, orm *ormhelper.DB) {
	// Define the result structure.
	type User struct {
		ID         string `db:"f_id"`
		Name       string `db:"f_name"`
		Email      string `db:"f_email"`
		CreateTime int64  `db:"f_create_time"`
	}

	// Query a single record.
	var user User
	err := orm.Select().From("users").WhereEq("f_id", "user-001").First(ctx, &user)
	if err != nil { // handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}

	// Query multiple records.
	var users []*User
	err = orm.Select().From("users").
		WhereLike("f_name", "%张%").
		OrderByDesc("f_create_time").
		Limit(defaultLimit).
		Get(ctx, &users)
	if err != nil {
		// handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}

	// Statistical quantity.
	count, err := orm.Select().From("users").WhereEq("f_status", "active").Count(ctx)
	if err != nil {
		// handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}
	_ = count
}

// Update data example.
func updateExample(ctx context.Context, orm *ormhelper.DB) {
	// Update a single field.
	_, err := orm.Update("users").
		Set("f_name", "张三丰").
		WhereEq("f_id", "user-001").
		Execute(ctx)
	if err != nil {
		// handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}

	// Batch update.
	_, err = orm.Update("users").SetData(map[string]interface{}{
		"f_status":      "inactive",
		"f_update_time": time.Now().Unix(),
	}).WhereLike("f_email", "%@old-domain.com").Execute(ctx)
	if err != nil {
		// handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}

	// Field auto-increment.
	_, err = orm.Update("users").
		Increment("f_login_count", 1).
		WhereEq("f_id", "user-001").
		Execute(ctx)
	if err != nil {
		// handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}
}

// Delete data example.
func deleteExample(ctx context.Context, orm *ormhelper.DB) {
	// Delete a single record.
	_, err := orm.Delete().From("users").WhereEq("f_id", "user-001").Execute(ctx)
	if err != nil {
		// handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}

	// Batch delete.
	_, err = orm.Delete().From("users").
		WhereEq("f_status", "inactive").
		WhereLt("f_create_time", time.Now().AddDate(0, 0, -30).Unix()).
		Execute(ctx)
	if err != nil {
		// handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}
}

// Pagination query example.
func paginationExample(ctx context.Context, orm *ormhelper.DB) {
	type User struct {
		ID         string `db:"f_id"`
		Name       string `db:"f_name"`
		Email      string `db:"f_email"`
		CreateTime int64  `db:"f_create_time"`
	}

	// Use pagination parameters.
	pagination := &ormhelper.PaginationParams{
		Page:     1,
		PageSize: defaultLimit,
	}

	// Use sort parameters.
	sort := &ormhelper.SortParams{
		Fields: []ormhelper.SortField{
			{Field: "f_create_time", Order: ormhelper.SortOrderDesc},
			{Field: "f_name", Order: ormhelper.SortOrderAsc},
		},
	}

	var users []*User
	err := orm.Select().From("users").
		WhereEq("f_status", "active").
		Sort(sort).
		Pagination(pagination).
		Get(ctx, &users)
	if err != nil {
		// handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}

	// Get the total number for paging calculation.
	total, err := orm.Select().From("users").WhereEq("f_status", "active").Count(ctx)
	if err != nil {
		// handling errors.
		fmt.Printf("err : %s\n", err)
		return
	}

	// Calculate paging information.
	result := ormhelper.CalculateQueryResult(total, pagination)
	_ = result
}

// Transaction usage example.
func transactionExample(ctx context.Context, orm *ormhelper.DB) {
	// Suppose you have a transaction.
	var tx *sql.Tx // Here nil is used as an example.

	// Using ORM in transactions.
	txOrm := orm.WithTx(tx)

	// Perform operations within a transaction.
	_, err := txOrm.Insert().Into("users").Values(map[string]interface{}{
		"f_id":   "user-tx-001",
		"f_name": "事务用户",
	}).Execute(ctx)
	if err != nil {
		// Rollback transaction.
		_ = tx.Rollback()
		return
	}

	_, err = txOrm.Update("users").
		Set("f_status", "verified").
		WhereEq("f_id", "user-tx-001").
		Execute(ctx)
	if err != nil {
		// Rollback transaction.
		_ = tx.Rollback()
		return
	}

	// commit transaction.
	err = tx.Commit()
	if err != nil {
		fmt.Printf("err : %s\n", err)
		return
	}
}

// Complex query example.
// func complexQueryExample(ctx context.Context, orm *ormhelper.DB) {
// 	type UserProfile struct {
// 		UserID     string `db:"f_user_id"`
// 		UserName   string `db:"f_user_name"`
// 		ProfileID  string `db:"f_profile_id"`
// 		Avatar     string `db:"f_avatar"`
// 		CreateTime int64  `db:"f_create_time"`
// 	}

// 	var profiles []*UserProfile
// 	err := orm.Select("u.f_id as f_user_id", "u.f_name as f_user_name", "p.f_id as f_profile_id", "p.f_avatar", "u.f_create_time").
// 		From("users u").
// 		LeftJoin("user_profiles p", "u.f_id = p.f_user_id").
// 		Where("u.f_status", "=", "active").
// 		And(func(w *ormhelper.WhereBuilder) {
// 			w.Gt("u.f_create_time", time.Now().AddDate(0, -1, 0).Unix()).
// 				Or(func(w2 *ormhelper.WhereBuilder) {
// 					w2.Eq("u.f_vip_level", "premium").
// 						Eq("u.f_verified", true)
// 				})
// 		}).
// 		OrderByDesc("u.f_create_time").
// 		Limit(50).
// 		Get(ctx, &profiles)
// 	if err != nil {
// // Handle errors.
// 	}
// }
