# 🚀 ORM Helper

A lightweight, high-performance Go language ORM tool library designed to simplify database operations. Supports chain calls, transaction management, log monitoring and other functions.

## 📋 Table of Contents.

- [✨ Features](#-Features)
- [🚀 Quick Start](#-Quick Start)
- [Install](#install)
- [Basic use](#Basic use)
- [Get started in 5 minutes](#5Minutes to get started)
- [📖Basic Tutorial](#-Basic Tutorial)
- [Query operation](#query operation)
- [insert operation](#insert operation)
- [Update operation](#update operation)
- [Delete operation](#delete operation)
- [🔧 Advanced Features](#-Advanced Features)
- [Affair Management](#affair management)
- [Log function](#LOG function)
- [Complex query](#complex query)
- [DAO mode integration](#dao mode integration)
- [⚡Performance Optimization](#-Performance Optimization)
- [🛠️Best Practices](#️-Best Practices)
- [❓ FAQ](#-FAQ)
- [🏗️Architecture Design](#️-Architecture Design)

## ✨ Features.

- 🔗 **Chain call**: smooth API design, simpler code.
- 🔄 **Transaction Support**: Complete transaction management to ensure data consistency.
- 📊 **Log Monitoring**: Built-in SQL execution log, supports slow query detection.
- 🎯 **Type safety**: reflection-based structure mapping, compile-time type checking.
- ⚡ **High Performance**: Minimized reflection usage, optimized SQL builds.
- 🔌 **Strong compatibility**: supports standard database/sql and sqlx.
- 🧪 **Easy to Test**: Supports Mock testing for easy unit testing.

## 🚀 Quick Start.

### Installation.

```bash
go get github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper
```

### Basic usage.

```go
import (
    "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
    "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
)

// 1. Create an ORM instance.
dbPool := db.NewDBPool() // Your database connection pool.
orm := ormhelper.New(dbPool, "your_database_name")

// 2. Define data model.
type User struct {
    ID       string `json:"f_id" db:"f_id"`
    Name     string `json:"f_name" db:"f_name"`
    Email    string `json:"f_email" db:"f_email"`
    Status   string `json:"f_status" db:"f_status"`
    CreateTime int64 `json:"f_create_time" db:"f_create_time"`
}

// 3. Get started.
ctx := context.Background()

//Query user.
user := &User{}
err := orm.Select().From("t_users").WhereEq("f_id", "123").First(ctx, user)

//Insert user.
_, err = orm.Insert().Into("t_users").Values(map[string]interface{}{
    "f_id":    "new-user-id",
"f_name": "Zhang San",
    "f_email": "zhangsan@example.com",
    "f_status": "active",
}).Execute(ctx)
```

### Get started in 5 minutes.

The following is a complete usage example demonstrating the main features of ORM Helper:

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
    "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
)

//Define data model.
type MCPConfig struct {
    ID          string `json:"f_id" db:"f_id"`
    Name        string `json:"f_name" db:"f_name"`
    Description string `json:"f_description" db:"f_description"`
    Status      string `json:"f_status" db:"f_status"`
    CreateTime  int64  `json:"f_create_time" db:"f_create_time"`
    UpdateTime  int64  `json:"f_update_time" db:"f_update_time"`
}

func main() {
// 1. Initialize database connection and ORM.
dbPool := initDB() // Your database initialization function.

//Create an ORM instance with logs.
    logger := logger.NewLogger(logger.LevelInfo)
    logConfig := ormhelper.LogConfig{
        Level:              ormhelper.LogLevelInfo,
SlowQueryThreshold: 100, // 100 milliseconds.
        LogSlowQuery:       true,
        LogAllQueries:      false,
    }

    orm := ormhelper.NewWithLogger(dbPool, "your_database", logger, logConfig)
    ctx := context.Background()

// 2. Insert data.
    newConfig := &MCPConfig{
        ID:          "config-001",
Name: "Example Configuration",
Description: "This is an example configuration",
        Status:      "active",
        CreateTime:  time.Now().UnixNano(),
        UpdateTime:  time.Now().UnixNano(),
    }

    _, err := orm.Insert().Into("t_mcp_server_config").Values(map[string]interface{}{
        "f_id":          newConfig.ID,
        "f_name":        newConfig.Name,
        "f_description": newConfig.Description,
        "f_status":      newConfig.Status,
        "f_create_time": newConfig.CreateTime,
        "f_update_time": newConfig.UpdateTime,
    }).Execute(ctx)

    if err != nil {
log.Printf("Insertion failed: %v", err)
        return
    }
log.Println("✅ Data insertion successful")

// 3. Query a single piece of data.
    config := &MCPConfig{}
    err = orm.Select().
        From("t_mcp_server_config").
        WhereEq("f_id", "config-001").
        First(ctx, config)

    if err != nil {
log.Printf("Query failed: %v", err)
        return
    }
log.Printf("✅ Query successful: %+v", config)

// 4. Query multiple pieces of data.
    configs := []*MCPConfig{}
    err = orm.Select().
        From("t_mcp_server_config").
        WhereEq("f_status", "active").
        OrderByDesc("f_create_time").
        Limit(10).
        Get(ctx, &configs)

    if err != nil {
log.Printf("Query list failed: %v", err)
        return
    }
log.Printf("✅ %d records found", len(configs))

// 5. Update data.
    _, err = orm.Update("t_mcp_server_config").
Set("f_description", "Updated description").
        Set("f_update_time", time.Now().UnixNano()).
        WhereEq("f_id", "config-001").
        Execute(ctx)

    if err != nil {
log.Printf("Update failed: %v", err)
        return
    }
log.Println("✅ Data updated successfully")

// 6. Statistics.
    count, err := orm.Select().
        From("t_mcp_server_config").
        WhereEq("f_status", "active").
        Count(ctx)

    if err != nil {
log.Printf("Statistics failed: %v", err)
        return
    }
log.Printf("✅ Number of active configurations: %d", count)

// 7. Transaction operations.
    tx, err := dbPool.Begin()
    if err != nil {
log.Printf("Failed to open transaction: %v", err)
        return
    }
    defer tx.Rollback()

    txORM := orm.WithTx(tx)

//Perform multiple operations in a transaction.
    _, err = txORM.Update("t_mcp_server_config").
        Set("f_status", "inactive").
        WhereEq("f_id", "config-001").
        Execute(ctx)

    if err != nil {
log.Printf("Transaction operation failed: %v", err)
        return
    }

// Submit transaction.
    if err = tx.Commit(); err != nil {
log.Printf("Failed to commit transaction: %v", err)
        return
    }
log.Println("✅ Transaction executed successfully")
}
```

Running this example, you will see log output similar to the following:

```
✅ Data inserted successfully.
INF SQL execution | SQL: SELECT f_id, f_name, f_description, f_status, f_create_time, f_update_time FROM `your_database`.`t_mcp_server_config` WHERE f_id = ? LIMIT 1 | Parameter: ['config-001'] | Execution time: 5ms.
✅ Query successful: &{ID:config-001 Name:Sample configuration Description:This is a sample configuration Status:active CreateTime:1703123456789 UpdateTime:1703123456789}
✅ 1 record found.
✅ Data updated successfully.
✅ Number of active configurations: 1.
✅ Transaction executed successfully.
```

🎉 **Congratulations! ** You have mastered the basic usage of ORM Helper. Next, you can view detailed function introduction and advanced usage.

## 📖 Basic Tutorial.

### Query operation.

#### Query a single record.

```go
//Query based on ID.
user := &User{}
err := orm.Select().From("t_users").WhereEq("f_id", "123").First(ctx, user)

//Query the specified field.
user := &User{}
err := orm.Select("f_id", "f_name", "f_email").
    From("t_users").
    WhereEq("f_id", "123").
    First(ctx, user)

// Handle not found situation.
if err == sql.ErrNoRows {
//The record does not exist.
    return nil, nil
}
```

#### Query multiple records.

```go
//Query list.
users := []*User{}
err := orm.Select().
    From("t_users").
    WhereEq("f_status", "active").
    OrderByDesc("f_create_time").
    Get(ctx, &users)

//Paging query.
users := []*User{}
err := orm.Select().
    From("t_users").
    WhereEq("f_status", "active").
    OrderByDesc("f_create_time").
    Limit(20).
Offset(40). // Page 3, 20 items per page.
    Get(ctx, &users)

// count quantity.
count, err := orm.Select().
    From("t_users").
    WhereEq("f_status", "active").
    Count(ctx)
```

#### Complex query conditions.

```go
//Multiple conditions.
users := []*User{}
err := orm.Select().
    From("t_users").
    WhereEq("f_status", "active").
WhereLike("f_name", "%John%").
    WhereGt("f_create_time", startTime).
    WhereLt("f_create_time", endTime).
    Get(ctx, &users)

// IN query.
userIDs := []interface{}{"id1", "id2", "id3"}
users := []*User{}
err := orm.Select().
    From("t_users").
    WhereIn("f_id", userIDs...).
    Get(ctx, &users)

//Complex condition combination.
users := []*User{}
err := orm.Select().
    From("t_users").
    WhereEq("f_status", "active").
    And(func(w *ormhelper.WhereBuilder) {
        w.Like("f_name", "%admin%").Or(func(w2 *ormhelper.WhereBuilder) {
            w2.Eq("f_role", "admin").Eq("f_department", "IT")
        })
    }).
    Get(ctx, &users)
```

### Insert operation.

#### Single insert.

```go
//Insert using map.
data := map[string]interface{}{
    "f_id":          "user-001",
"f_name": "Zhang San",
    "f_email":       "zhangsan@example.com",
    "f_status":      "active",
    "f_create_time": time.Now().UnixNano(),
}

result, err := orm.Insert().Into("t_users").Values(data).Execute(ctx)
if err != nil {
    return err
}

// Get the number of inserted rows.
rowsAffected, _ := result.RowsAffected()
```

#### Batch insert.

```go
//Insert multiple records in batches.
columns := []string{"f_id", "f_name", "f_email", "f_status", "f_create_time"}
values := [][]interface{}{
{"user-001", "Zhang San", "zhangsan@example.com", "active", time.Now().UnixNano()},
{"user-002", "Alice", "alice@example.com", "active", time.Now().UnixNano()},
{"user-003", "Bob", "bob@example.com", "active", time.Now().UnixNano()},
}

_, err := orm.Insert().
    Into("t_users").
    BatchValues(columns, values).
    Execute(ctx)
```

### Update operation.

#### Update based on conditions.

```go
//Update a single field.
_, err := orm.Update("t_users").
    Set("f_status", "inactive").
    WhereEq("f_id", "user-001").
    Execute(ctx)

//Update multiple fields.
_, err := orm.Update("t_users").
Set("f_name", "new name").
    Set("f_email", "newemail@example.com").
    Set("f_update_time", time.Now().UnixNano()).
    WhereEq("f_id", "user-001").
    Execute(ctx)

// Batch update.
affectedRows, err := orm.Update("t_users").
    Set("f_status", "inactive").
    Set("f_update_time", time.Now().UnixNano()).
    WhereIn("f_id", "user-001", "user-002", "user-003").
    ExecuteAndReturnAffected(ctx)
```

### Delete operation.

```go
//Delete based on ID.
_, err := orm.Delete().From("t_users").WhereEq("f_id", "user-001").Execute(ctx)

// Batch delete.
_, err := orm.Delete().
    From("t_users").
    WhereEq("f_status", "inactive").
    WhereLt("f_create_time", oldTime).
    Execute(ctx)

// Soft delete (recommended)
_, err := orm.Update("t_users").
    Set("f_status", "deleted").
    Set("f_delete_time", time.Now().UnixNano()).
    WhereEq("f_id", "user-001").
    Execute(ctx)
```

## 🔧 Advanced features.

### Transaction Management.

#### Basic transaction usage.

```go
//Start transaction.
tx, err := dbPool.Begin()
if err != nil {
    return err
}
defer tx.Rollback() // Ensure rollback in case of exception.

// Use transaction ORM.
txORM := orm.WithTx(tx)

//Perform operations in transaction.
_, err = txORM.Insert().Into("t_users").Values(userData).Execute(ctx)
if err != nil {
    return err
}

_, err = txORM.Insert().Into("t_user_profiles").Values(profileData).Execute(ctx)
if err != nil {
    return err
}

// Submit transaction.
return tx.Commit()
```

#### Transaction function encapsulation.

```go
func (dao *UserDAO) CreateUserWithProfile(ctx context.Context, user *User, profile *UserProfile) error {
    return dao.withTransaction(ctx, func(txORM *ormhelper.DB) error {
//Insert user.
        _, err := txORM.Insert().Into("t_users").Values(map[string]interface{}{
            "f_id":          user.ID,
            "f_name":        user.Name,
            "f_email":       user.Email,
            "f_create_time": time.Now().UnixNano(),
        }).Execute(ctx)
        if err != nil {
return fmt.Errorf("Failed to insert user: %w", err)
        }

//Insert user information.
        _, err = txORM.Insert().Into("t_user_profiles").Values(map[string]interface{}{
            "f_user_id":     user.ID,
            "f_avatar":      profile.Avatar,
            "f_bio":         profile.Bio,
            "f_create_time": time.Now().UnixNano(),
        }).Execute(ctx)
        if err != nil {
return fmt.Errorf("Failed to insert user data: %w", err)
        }

        return nil
    })
}

//Transaction helper function.
func (dao *UserDAO) withTransaction(ctx context.Context, fn func(*ormhelper.DB) error) error {
    tx, err := dao.dbPool.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    txORM := dao.orm.WithTx(tx)
    if err := fn(txORM); err != nil {
        return err
    }

    return tx.Commit()
}
```

### Log function.

#### Detailed explanation of log configuration.

```go
// Development environment configuration - detailed log.
devLogger := logger.NewLogger(logger.LevelDebug)
devLogConfig := ormhelper.LogConfig{
Level: ormhelper.LogLevelDebug, // Record all logs.
SlowQueryThreshold: 50, // 50 millisecond slow query threshold.
LogSlowQuery: true, // Log slow queries.
LogAllQueries: true, // Log all queries.
}

//Production environment configuration - streamlined logs.
prodLogger := logger.NewLogger(logger.LevelWarn)
prodLogConfig := ormhelper.LogConfig{
Level: ormhelper.LogLevelWarn, // Only log warnings and errors.
SlowQueryThreshold: 200, // 200 milliseconds slow query threshold.
LogSlowQuery: true, // Log slow queries.
LogAllQueries: false, // Do not log all queries.
}

//Create ORM instance.
orm := ormhelper.NewWithLogger(dbPool, "database_name", prodLogger, prodLogConfig)
```

#### Dynamic log control.

```go
// Enable debug logging at runtime.
func (dao *UserDAO) EnableDebugLogging() {
    debugLogger := logger.NewLogger(logger.LevelDebug)
    debugConfig := ormhelper.LogConfig{
        Level:         ormhelper.LogLevelDebug,
        LogAllQueries: true,
    }
    dao.orm.EnableLogging(debugLogger, debugConfig)
}

//Disable logging at runtime.
func (dao *UserDAO) DisableLogging() {
    dao.orm.DisableLogging()
}

//Temporarily enable logging for debugging.
func (dao *UserDAO) DebugQuery(ctx context.Context, userID string) (*User, error) {
//Temporarily enable debug logging.
    originalEnabled := dao.orm.IsLoggingEnabled()
    if !originalEnabled {
        dao.EnableDebugLogging()
        defer dao.DisableLogging()
    }

//Execute the query that needs to be debugged.
    user := &User{}
    err := dao.orm.Select().From("t_users").WhereEq("f_id", userID).First(ctx, user)

    return user, err
}
```

### Complex query.

#### JOIN query.

```go
//Left join query.
type UserWithProfile struct {
    UserID     string `db:"user_id"`
    UserName   string `db:"user_name"`
    UserEmail  string `db:"user_email"`
    Avatar     string `db:"avatar"`
    Bio        string `db:"bio"`
}

func (dao *UserDAO) GetUsersWithProfiles(ctx context.Context) ([]*UserWithProfile, error) {
    var results []*UserWithProfile

    err := dao.orm.Select(
        "u.f_id AS user_id",
        "u.f_name AS user_name",
        "u.f_email AS user_email",
        "p.f_avatar AS avatar",
        "p.f_bio AS bio",
    ).
        From("t_users u").
        LeftJoin("t_user_profiles p", "u.f_id = p.f_user_id").
        WhereEq("u.f_status", "active").
        OrderByDesc("u.f_create_time").
        Get(ctx, &results)

    return results, err
}
```

#### Aggregation query.

```go
//Statistical query.
type UserStats struct {
    Status string `db:"status"`
    Count  int64  `db:"count"`
}

func (dao *UserDAO) GetUserStatsByStatus(ctx context.Context) ([]*UserStats, error) {
    var stats []*UserStats

    err := dao.orm.Select("f_status AS status", "COUNT(*) AS count").
        From("t_users").
        GroupBy("f_status").
        Having("COUNT(*) > 0").
        OrderByDesc("count").
        Get(ctx, &stats)

    return stats, err
}

// Complex aggregation.
func (dao *UserDAO) GetMonthlyUserStats(ctx context.Context, year int) (map[string]int64, error) {
    type MonthlyStats struct {
        Month string `db:"month"`
        Count int64  `db:"count"`
    }

    var monthlyStats []*MonthlyStats
    err := dao.orm.Select(
        "DATE_FORMAT(FROM_UNIXTIME(f_create_time/1000000000), '%Y-%m') AS month",
        "COUNT(*) AS count",
    ).
        From("t_users").
        WhereGt("f_create_time", time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()).
        WhereLt("f_create_time", time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()).
        GroupBy("month").
        OrderByAsc("month").
        Get(ctx, &monthlyStats)

    if err != nil {
        return nil, err
    }

//Convert to map.
    result := make(map[string]int64)
    for _, stat := range monthlyStats {
        result[stat.Month] = stat.Count
    }

    return result, nil
}
```

### DAO mode integration.

#### Complete DAO implementation.

```go
type UserDAO struct {
    orm    *ormhelper.DB
    dbPool *sqlx.DB
    logger interfaces.Logger
}

func NewUserDAO(dbName string, logger interfaces.Logger) *UserDAO {
    dbPool := db.NewDBPool()

//Configuration log.
    logConfig := ormhelper.LogConfig{
        Level:              ormhelper.LogLevelInfo,
        SlowQueryThreshold: 100,
        LogSlowQuery:       true,
        LogAllQueries:      false,
    }

    return &UserDAO{
        orm:    ormhelper.NewWithLogger(dbPool, dbName, logger, logConfig),
        dbPool: dbPool,
        logger: logger,
    }
}

// Query method.
func (dao *UserDAO) GetByID(ctx context.Context, id string) (*User, error) {
    user := &User{}
    err := dao.orm.Select().From("t_users").WhereEq("f_id", id).First(ctx, user)

    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
dao.logger.Errorf("Failed to query user, id=%s, error=%v", id, err)
return nil, fmt.Errorf("Failed to query user: %w", err)
    }

    return user, nil
}

//Paging query.
func (dao *UserDAO) GetList(ctx context.Context, filters *UserFilters, page, pageSize int) ([]*User, int64, error) {
    query := dao.orm.Select().From("t_users")

// Dynamically construct query conditions.
    if filters.Status != "" {
        query = query.WhereEq("f_status", filters.Status)
    }
    if filters.Name != "" {
        query = query.WhereLike("f_name", "%"+filters.Name+"%")
    }
    if filters.Email != "" {
        query = query.WhereLike("f_email", "%"+filters.Email+"%")
    }

//Total number of queries.
    total, err := query.Count(ctx)
    if err != nil {
return nil, 0, fmt.Errorf("Failed to query the total number of users: %w", err)
    }

//Paging query.
    var users []*User
    offset := (page - 1) * pageSize
    err = query.
        OrderByDesc("f_create_time").
        Limit(pageSize).
        Offset(offset).
        Get(ctx, &users)

    if err != nil {
dao.logger.Errorf("Paging user query failed, page=%d, pageSize=%d, error=%v", page, pageSize, err)
return nil, 0, fmt.Errorf("Paging user query failed: %w", err)
    }

    return users, total, nil
}

//Create user.
func (dao *UserDAO) Create(ctx context.Context, user *User) error {
    now := time.Now().UnixNano()
    user.CreateTime = now
    user.UpdateTime = now

    _, err := dao.orm.Insert().Into("t_users").Values(map[string]interface{}{
        "f_id":          user.ID,
        "f_name":        user.Name,
        "f_email":       user.Email,
        "f_status":      user.Status,
        "f_create_time": user.CreateTime,
        "f_update_time": user.UpdateTime,
    }).Execute(ctx)

    if err != nil {
dao.logger.Errorf("Failed to create user, user=%+v, error=%v", user, err)
return fmt.Errorf("Failed to create user: %w", err)
    }

dao.logger.Infof("User created successfully, id=%s, name=%s", user.ID, user.Name)
    return nil
}

// update user.
func (dao *UserDAO) Update(ctx context.Context, user *User) error {
    user.UpdateTime = time.Now().UnixNano()

    _, err := dao.orm.Update("t_users").
        Set("f_name", user.Name).
        Set("f_email", user.Email).
        Set("f_status", user.Status).
        Set("f_update_time", user.UpdateTime).
        WhereEq("f_id", user.ID).
        Execute(ctx)

    if err != nil {
dao.logger.Errorf("Failed to update user, user=%+v, error=%v", user, err)
return fmt.Errorf("Failed to update user: %w", err)
    }

    return nil
}

// Delete user (soft delete)
func (dao *UserDAO) Delete(ctx context.Context, id string) error {
    _, err := dao.orm.Update("t_users").
        Set("f_status", "deleted").
        Set("f_delete_time", time.Now().UnixNano()).
        WhereEq("f_id", id).
        Execute(ctx)

    if err != nil {
dao.logger.Errorf("Failed to delete user, id=%s, error=%v", id, err)
return fmt.Errorf("Failed to delete user: %w", err)
    }

    return nil
}
```

## ⚡ Performance optimization.

### Query optimization.

```go
// ✅ Good practice: use index fields.
users := []*User{}
err := orm.Select().
    From("t_users").
WhereEq("f_status", "active"). // f_status has an index.
WhereEq("f_department", "IT"). // f_department has an index.
OrderByDesc("f_create_time"). // f_create_time has an index.
Limit(20). //Limit the number of results.
    Get(ctx, &users)

// ❌ Avoid: full table scan.
users := []*User{}
err := orm.Select().
    From("t_users").
WhereLike("f_description", "%keyword%"). // LIKE query without index.
Get(ctx, &users) // No LIMIT limit.
```

### Batch operation optimization.

```go
// ✅ Batch insert.
columns := []string{"f_id", "f_name", "f_email", "f_create_time"}
values := make([][]interface{}, 0, len(users))
for _, user := range users {
    values = append(values, []interface{}{
        user.ID, user.Name, user.Email, time.Now().UnixNano(),
    })
}

_, err := orm.Insert().Into("t_users").BatchValues(columns, values).Execute(ctx)

// ❌ Avoid: Looping single insertion.
for _, user := range users {
    _, err := orm.Insert().Into("t_users").Values(map[string]interface{}{
        "f_id":          user.ID,
        "f_name":        user.Name,
        "f_email":       user.Email,
        "f_create_time": time.Now().UnixNano(),
    }).Execute(ctx)
}
```

### Connection pool optimization.

```go
// Database connection pool configuration.
func setupDBPool() *sqlx.DB {
    db := sqlx.MustConnect("mysql", dsn)

//Set connection pool parameters.
db.SetMaxOpenConns(100) //Maximum number of open connections.
db.SetMaxIdleConns(20) //Maximum number of idle connections.
db.SetConnMaxLifetime(time.Hour) // Maximum connection life time.
db.SetConnMaxIdleTime(time.Minute * 30) //Maximum idle time of connection.

    return db
}
```

## 🛠️ Best Practices.

### Error handling.

```go
func (dao *UserDAO) SafeGetUser(ctx context.Context, id string) (*User, error) {
    if id == "" {
return nil, errors.New("User ID cannot be empty")
    }

    user := &User{}
    err := dao.orm.Select().From("t_users").WhereEq("f_id", id).First(ctx, user)

    if err == sql.ErrNoRows {
return nil, nil // Explicitly return nil to indicate not found.
    }

    if err != nil {
dao.logger.Errorf("Failed to query user, id=%s, error=%v", id, err)
return nil, fmt.Errorf("Failed to query user: %w", err)
    }

    return user, nil
}
```

### Parameter verification.

```go
func (dao *UserDAO) CreateUser(ctx context.Context, user *User) error {
// Parameter validation.
    if user == nil {
return errors.New("User information cannot be empty")
    }
    if user.Name == "" {
return errors.New("Username cannot be empty")
    }
    if user.Email == "" {
return errors.New("Mailbox cannot be empty")
    }

// Check email format.
    if !isValidEmail(user.Email) {
return errors.New("The email format is incorrect")
    }

// Check if the user already exists.
    existing, err := dao.GetByEmail(ctx, user.Email)
    if err != nil {
return fmt.Errorf("Failed to check if user exists: %w", err)
    }
    if existing != nil {
return errors.New("Email has been used")
    }

//Execute creation.
    return dao.Create(ctx, user)
}
```

### Logging.

```go
func (dao *UserDAO) UpdateUserWithLog(ctx context.Context, user *User) error {
    start := time.Now()

//Start recording operation.
dao.logger.Infof("Start updating user, id=%s, name=%s", user.ID, user.Name)

    err := dao.Update(ctx, user)
    duration := time.Since(start)

    if err != nil {
dao.logger.Errorf("Failed to update user, id=%s, duration=%v, error=%v",
            user.ID, duration, err)
        return err
    }

dao.logger.Infof("Update user successfully, id=%s, duration=%v", user.ID, duration)
    return nil
}
```

## ❓ FAQ.

### Q: How to handle the mapping between database fields and structure fields?.

A: Use the `db` tag for mapping:

```go
type User struct {
ID string `json:"id" db:"f_id"` // Database field: f_id.
Name string `json:"name" db:"f_name"` // Database field: f_name.
Email string `json:"email" db:"f_email"` // Database field: f_email.
    CreateTime int64  `json:"create_time" db:"f_create_time"`
}
```

### Q: How to deal with NULL values?.

A: Use pointer types or sql.NullXXX types:

```go
type User struct {
    ID          string         `db:"f_id"`
    Name        string         `db:"f_name"`
Description *string `db:"f_description"` // Can be NULL.
Age sql.NullInt64 `db:"f_age"` // Can be NULL.
}
```

### Q: How to perform database migration?.

A: It is recommended to use specialized migration tools. ORM Helper focuses on data operations:

```go
// Use golang-migrate or other migration tools.
// ORM Helper does not provide DDL operations and focuses on DML operations.
```

### Q: How to deal with complex business logic?.

A: The Service layer handles business logic, and the DAO layer is only responsible for data access:

```go
// Service layer.
type UserService struct {
    userDAO    *UserDAO
    profileDAO *UserProfileDAO
}

func (s *UserService) CreateUserWithProfile(ctx context.Context, req *CreateUserRequest) error {
//Business logic verification.
    if err := s.validateCreateRequest(req); err != nil {
        return err
    }

// Create users and profiles using transactions.
    return s.userDAO.withTransaction(ctx, func(txORM *ormhelper.DB) error {
//Create user.
        user := &User{
            ID:    generateUserID(),
            Name:  req.Name,
            Email: req.Email,
        }
        if err := s.userDAO.CreateWithTx(ctx, txORM, user); err != nil {
            return err
        }

//Create user profile.
        profile := &UserProfile{
            UserID: user.ID,
            Avatar: req.Avatar,
            Bio:    req.Bio,
        }
        return s.profileDAO.CreateWithTx(ctx, txORM, profile)
    })
}
```

### Q: How to perform unit testing?.

A: Use test database or Mock:

```go
func TestUserDAO_Create(t *testing.T) {
//Set up test database.
    testDB := setupTestDB()
    defer testDB.Close()

    logger := logger.NewLogger(logger.LevelDebug)
    dao := NewUserDAO("test_db", logger)

    ctx := context.Background()
    user := &User{
        ID:    "test-user-001",
Name: "Test User",
        Email: "test@example.com",
        Status: "active",
    }

//Execute test.
    err := dao.Create(ctx, user)
    assert.NoError(t, err)

//Verify results.
    created, err := dao.GetByID(ctx, user.ID)
    assert.NoError(t, err)
    assert.Equal(t, user.Name, created.Name)
    assert.Equal(t, user.Email, created.Email)
}
```

## 🏗️ Architecture design.

ORM Helper adopts modular design. For detailed architecture description, please refer to [ARCHITECTURE.md](./ARCHITECTURE.md).

### Core components.

- **ORM core** (`orm.go`) - the main ORM class, providing a unified database operation interface.
- **Query Builder** (`select.go`) - Responsible for building SELECT query statements.
- **Insert builder** (`insert.go`) - Responsible for building INSERT statements.
- **Update Builder** (`update.go`) - Responsible for building UPDATE statements.
- **Delete builder** (`delete.go`) - Responsible for building DELETE statements.
- **Condition Builder** (`where.go`) - Responsible for building WHERE conditions.
- **Result Scanner** (`scanner.go`) - Responsible for mapping query results to structures.
- **Log System** (`logger.go`) - Provides SQL execution logs and performance monitoring.

### Design principles.

1. **Easy to use** - Intuitive API design, low learning cost.
2. **Fully Compatible** - Seamless integration with existing `sqlx.DB`
3. **Non-intrusive** - can be migrated incrementally without affecting existing code.
4. **Type safety** - compile-time checks to reduce run-time errors.
5. **High Performance** - Minimize performance overhead, close to native SQL performance.

---

🎉 **Start using ORM Helper to make database operations simple, safe and efficient! **.

If you encounter any problems during use, you are welcome to raise an issue or contribute code.

# ORM Helper - Unified paging and sorting functions.

The `ormhelper` package now provides unified paging and sorting functions, making it easy to implement a consistent data query interface at the business layer.

## Core Features.

### 1. Pagination Params.

```go
type PaginationParams struct {
Page int `json:"page" validate:"min=1"` // Page number, starting from 1.
PageSize int `json:"page_size" validate:"min=1,max=100"` //Quantity per page.
}
```

### 2. Sort Params (SortParams)

```go
type SortOrder string

const (
SortOrderAsc SortOrder = "asc" // ascending order.
SortOrderDesc SortOrder = "desc" // descending order.
)

type SortField struct {
Field string `json:"field"` // Database field name.
Order SortOrder `json:"order"` // Sorting direction.
}

type SortParams struct {
Fields []SortField `json:"fields,omitempty"` // Supports multi-field sorting.
}
```

### 3. Query Result (QueryResult)

```go
type QueryResult struct {
Total int64 `json:"total"` // Total number of records.
Page int `json:"page"` // Current page number.
PageSize int `json:"page_size"` // Number of pages per page.
TotalPages int `json:"total_pages"` //Total number of pages.
HasNext bool `json:"has_next"` // Is there a next page?.
HasPrev bool `json:"has_prev"` // Whether there is a previous page.
}
```

## SelectBuilder new method.

### Pagination()

Apply pagination parameters to the query:

```go
func (s *SelectBuilder) Pagination(pagination *PaginationParams) *SelectBuilder
```

### Sort()

Apply sorting parameters to the query:

```go
func (s *SelectBuilder) Sort(sort *SortParams) *SelectBuilder
```

## Usage example.

### Basic paging query.

```go
paginationParams := &PaginationParams{
    Page:     1,
    PageSize: 10,
}

var results []ExampleData
err := orm.Select().
    From("t_example").
    Pagination(paginationParams).
    Get(ctx, &results)
```

### Paging query with sorting.

```go
sortParams := &SortParams{
    Fields: []SortField{
        {Field: "name", Order: SortOrderAsc},
        {Field: "id", Order: SortOrderDesc},
    },
}

paginationParams := &PaginationParams{
    Page:     1,
    PageSize: 10,
}

err := orm.Select().
    From("t_example").
    Pagination(paginationParams).
    Sort(sortParams).
    Get(ctx, &results)
```

### Get paging information.

```go
// Get the total number.
totalCount, err := orm.Select().
    From("t_example").
    Count(ctx)

// Calculate paging results.
queryResult := CalculateQueryResult(totalCount, paginationParams)

// queryResult contains complete paging information.
fmt.Printf("Total number of records: %d\n", queryResult.Total)
fmt.Printf("Current page: %d/%d\n", queryResult.Page, queryResult.TotalPages)
fmt.Printf("Is there a next page: %v\n", queryResult.HasNext)
```

### Complex query example.

```go
sortParams := &SortParams{
    Fields: []SortField{
        {Field: "priority", Order: SortOrderDesc},
        {Field: "create_time", Order: SortOrderAsc},
    },
}

paginationParams := &PaginationParams{
    Page:     2,
    PageSize: 20,
}

err := orm.Select().
    From("t_example").
WhereEq("status", "active"). // Conditional filtering.
WhereLike("name", "%test%"). // Fuzzy query.
Sort(sortParams). //Multiple field sorting.
Pagination(paginationParams). // Pagination.
    Get(ctx, &results)
```

## Utility functions.

### CalculateQueryResult()

Calculate pagination information for query results:

```go
func CalculateQueryResult(total int64, pagination *PaginationParams) *QueryResult
```

This function automatically calculates:
-Total number of pages.
- Whether there is next page/previous page.
- Handle edge cases (such as paging parameters being empty or invalid)

## Notes.

1. **Field name safety**: The field names in `SortField.Field` should be safe by the caller to avoid SQL injection.
2. **Parameter verification**: It is recommended to verify the paging and sorting parameters at the business layer.
3. **Performance considerations**: Pay attention to the performance impact when performing large paging queries.
4. **Index**: Make sure the sort field has an appropriate database index.

## Integration Guide.

These features are designed not to impact existing business logic, allowing you to:

1. Gradually add paging and sorting support to existing data access layer methods.
2. Keep the existing interface unchanged and add new functions through optional parameters.
3. Use the unified `QueryResult` structure to return consistent paging information at the API layer.

## Sample file.

Refer to the `usage_example.go` file for a complete usage example.
