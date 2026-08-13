// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package bkntrace

import (
	"database/sql"
	"fmt"

	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	_ "github.com/openbkn-ai/bkn-foundry/comm-go/db/driver"
)

// OpenProducerOutboxDB uses parseTime because the Outbox lease and retry
// scheduler reads DATETIME(6) columns into time.Time values.
func OpenProducerOutboxDB(setting libdb.DBSetting) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=Local",
		setting.Username, setting.Password, setting.Host, setting.Port, setting.DBName)
	db, err := sql.Open("openbkn-rds", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
