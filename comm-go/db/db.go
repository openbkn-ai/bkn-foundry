// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package db

import (
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	_ "github.com/openbkn-ai/bkn-foundry/comm-go/db/driver"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

const (
	DRIVER_NAME = "openbkn-rds"
)

// DBSetting configures a database connection.
type DBSetting struct {
	Host     string
	Port     int
	Username string
	Password string `json:"-"`
	DBName   string
}

var (
	dbOnce sync.Once
	db     *sql.DB
	dbUrl  string
)

// NewDB configures and returns the shared database client.
func NewDB(setting *DBSetting) *sql.DB {
	dbOnce.Do(func() {
		db = InitDB(setting)
	})

	return db
}

// InitDB initializes a database connection.
func InitDB(setting *DBSetting) *sql.DB {
	dbUrl = fmt.Sprintf("%s@tcp(%s:%d)/%s?charset=utf8mb4&loc=Local", setting.Username, setting.Host, setting.Port, setting.DBName)
	dbDSN := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&loc=Local",
		setting.Username, setting.Password, setting.Host, setting.Port, setting.DBName)

	Db, err := sql.Open(DRIVER_NAME, dbDSN)
	if err != nil {
		// Opening the connection failed.
		logger.Infof("dbDSN: %s", dbDSN)
		panic("invalid data source configuration: " + err.Error())
	}

	// Maximum open connections.
	Db.SetMaxOpenConns(100)
	// Maximum idle connections.
	Db.SetMaxIdleConns(20)
	// Maximum connection lifetime.
	Db.SetConnMaxLifetime(100 * time.Second)

	if err = Db.Ping(); err != nil {
		panic("database connection failed: " + err.Error())
	}

	logger.Info("connect success")
	return Db
}

func GetDBUrl() string {
	return dbUrl
}

func GetDBType() string {
	return os.Getenv("DB_TYPE")
}
