// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driver

import (
	"database/sql"
	"database/sql/driver"
	"os"
	"strings"

	"github.com/openbkn-ai/bkn-comm-go/db/driver/dmdb"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/goldendb"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/mysql"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/tidb"
)

type RDSDriver struct {
}

var supportedOpen = map[string]func(string) (driver.Conn, error){
	"MYSQL":    mysql.Open,
	"MARIADB":  mysql.Open,
	"GOLDENDB": goldendb.Open,
	"DM8":      dmdb.Open,
	"TIDB":     tidb.Open,
	"DEFAULT":  mysql.Open,
	"KDB9":     kingbase.Open,
}

var supportedOpenConnector = map[string]func(string) (driver.Connector, error){
	"MYSQL":    mysql.OpenConnector,
	"MARIADB":  mysql.OpenConnector,
	"GOLDENDB": goldendb.OpenConnector,
	"DM8":      dmdb.OpenConnector,
	"TIDB":     tidb.OpenConnector,
	"DEFAULT":  mysql.OpenConnector,
	"KDB9":     kingbase.OpenConnector,
}

func (d RDSDriver) Open(dsn string) (driver.Conn, error) {
	dbType := os.Getenv("DB_TYPE")
	dbType = strings.ToUpper(dbType)
	if v, ok := supportedOpen[dbType]; ok {
		return v(dsn)
	}
	return supportedOpen["DEFAULT"](dsn)
}

func (d RDSDriver) OpenConnector(dsn string) (driver.Connector, error) {
	dbType := os.Getenv("DB_TYPE")
	dbType = strings.ToUpper(dbType)
	if v, ok := supportedOpenConnector[dbType]; ok {
		return v(dsn)
	}
	return supportedOpenConnector["DEFAULT"](dsn)
}

func init() {
	sql.Register("openbkn-rds", &RDSDriver{})
}
