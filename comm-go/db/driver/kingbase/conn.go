// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package kingbase

import (
	"context"
	"database/sql/driver"
	"fmt"
	"os"
)

type KBConn struct {
	driver.Conn
}

func (kbConn KBConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if os.Getenv("RDS_SDK_DEBUG") == "true" {
		fmt.Println("conn exec: ", query, args)
	}
	return kbConn.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (kbConn KBConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if os.Getenv("RDS_SDK_DEBUG") == "true" {
		fmt.Println("conn query: ", query, args)
	}
	return kbConn.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (kbConn KBConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if os.Getenv("RDS_SDK_DEBUG") == "true" {
		fmt.Println("conn prepare: ", query)
	}
	return kbConn.Conn.Prepare(query)
}

func (kbConn KBConn) Prepare(query string) (driver.Stmt, error) {
	if os.Getenv("RDS_SDK_DEBUG") == "true" {
		fmt.Println("conn prepare: ", query)
	}
	return kbConn.Conn.Prepare(query)
}

func (kbConn KBConn) Begin() (driver.Tx, error) {
	return kbConn.Conn.Begin()
}

func (kbConn KBConn) Close() error {
	return kbConn.Conn.Close()
}
