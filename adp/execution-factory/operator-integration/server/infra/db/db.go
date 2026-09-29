// Package db database connection pool.
// @file db.go
// @description initialize connection pool.
package db

import (
	"database/sql"
	"fmt"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/qustavo/sqlhooks/v2"

	// _ Register openbkn-rds driver.
	openbknRDS "github.com/openbkn-ai/bkn-foundry/comm-go/db/driver"
	"github.com/openbkn-ai/bkn-foundry/comm-go/db/sqlx"
)

const (
	traceDriverName = "rds-trace"
)

var (
	dbOnce sync.Once
	dbPool *sqlx.DB
)

func initTraceHook() {
	hook := &telemetry.RDSHook{System: "rds"}
	sql.Register(traceDriverName, sqlhooks.Wrap(new(openbknRDS.RDSDriver), hook))
}

// NewDBPool gets the database connection pool.
func NewDBPool() *sqlx.DB {
	dbOnce.Do(func() {
		initTraceHook()
		conf := config.NewConfigLoader()
		logger := conf.GetLogger()
		dbName := conf.GetDBName()
		connInfo := sqlx.DBConfig{
			User:         conf.DB.UserName,
			Password:     conf.DB.Password,
			Host:         conf.DB.Host,
			Port:         conf.DB.Port,
			HostRead:     conf.DB.Host,
			PortRead:     conf.DB.Port,
			Database:     dbName,
			Charset:      conf.DB.Charset,
			Timeout:      conf.DB.ConnTimeout,
			ReadTimeout:  conf.DB.ReadTimeout,
			WriteTimeout: conf.DB.WriteTimeout,
			MaxOpenConns: conf.DB.MaxOpenConns,
		}
		connInfo.CustomDriver = traceDriverName
		var err error
		dbPool, err = sqlx.NewDB(&connInfo)
		if err != nil {
			// Determine err.
			if err.Error() == "driver must implement driver.ConnBeginTx" {
				connInfo.CustomDriver = "openbkn-rds"
				dbPool, err = sqlx.NewDB(&connInfo)
			}
			if err != nil {
				logger.Errorf("new db operator failed; error:%s, %s", err.Error(), describeDBConn(&connInfo))
				panic(err)
			}
		}
	})
	return dbPool
}

// describeDBConn renders the non-secret connection coordinates for logs. The connection info and
// the DB config both carry the password, so they must never be logged whole.
func describeDBConn(connInfo *sqlx.DBConfig) string {
	return fmt.Sprintf("host:%s, port:%d, database:%s, driver:%s",
		connInfo.Host, connInfo.Port, connInfo.Database, connInfo.CustomDriver)
}
