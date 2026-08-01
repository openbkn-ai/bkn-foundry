// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package tidb

import (
	"database/sql/driver"

	"github.com/go-sql-driver/mysql"
)

func Open(dsn string) (driver.Conn, error) {
	return mysql.MySQLDriver{}.Open(dsn)
}

func OpenConnector(dsn string) (driver.Connector, error) {
	return mysql.MySQLDriver{}.OpenConnector(dsn)
}
