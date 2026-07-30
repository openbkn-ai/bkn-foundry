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

	_ "github.com/openbkn-ai/bkn-comm-go/db/driver"
	"github.com/openbkn-ai/bkn-comm-go/logger"
)

const (
	DRIVER_NAME = "openbkn-rds"
)

// db配置项
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

// 配置db的客户端参数
func NewDB(setting *DBSetting) *sql.DB {
	dbOnce.Do(func() {
		db = InitDB(setting)
	})

	return db
}

// 初始化链接
func InitDB(setting *DBSetting) *sql.DB {
	dbUrl = fmt.Sprintf("%s@tcp(%s:%d)/%s?charset=utf8mb4&loc=Local", setting.Username, setting.Host, setting.Port, setting.DBName)
	dbDSN := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&loc=Local",
		setting.Username, setting.Password, setting.Host, setting.Port, setting.DBName)

	Db, err := sql.Open(DRIVER_NAME, dbDSN)
	if err != nil {
		// 打开连接失败
		logger.Infof("dbDSN: %s", dbDSN)
		panic("数据源配置不正确: " + err.Error())
	}

	// 最大连接数
	Db.SetMaxOpenConns(100)
	// 闲置连接数
	Db.SetMaxIdleConns(20)
	// 最大连接周期
	Db.SetConnMaxLifetime(100 * time.Second)

	if err = Db.Ping(); err != nil {
		panic("数据库连接失败: " + err.Error())
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
