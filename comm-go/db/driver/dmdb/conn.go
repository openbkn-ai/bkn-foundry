// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dmdb

import (
	"context"
	"fmt"
	"os"
	"strings"

	"database/sql/driver"
)

type DMConn struct {
	driver.Conn
}

func (dmConn *DMConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	query = newDmQuery(query, args)
	for i, v := range args {
		if _, ok := v.Value.([]byte); ok {
			args[i].Value = string(v.Value.([]byte))
		}
	}
	if os.Getenv("RDS_SDK_DEBUG") == "true" {
		fmt.Println("conn exec: ", query, args)
	}
	return dmConn.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (dmConn *DMConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	query = newDmQuery(query, args)
	for i, v := range args {
		if _, ok := v.Value.([]byte); ok {
			args[i].Value = string(v.Value.([]byte))
		}
	}
	if os.Getenv("RDS_SDK_DEBUG") == "true" {
		fmt.Println("conn query: ", query, args)
	}
	return dmConn.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (dmConn *DMConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	query = newDmQuery(query, nil)
	if os.Getenv("RDS_SDK_DEBUG") == "true" {
		fmt.Println("conn prepare: ", query)
	}
	stmt, err := dmConn.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	return DMStmt{stmt}, err
}

func (dmConn *DMConn) Prepare(query string) (driver.Stmt, error) {
	if os.Getenv("RDS_SDK_DEBUG") == "true" {
		fmt.Println("conn prepare: ", query)
	}
	return dmConn.Conn.Prepare(query)
}

func (dmConn *DMConn) Begin() (driver.Tx, error) {
	return dmConn.Conn.Begin()
}

func (dmConn *DMConn) Close() error {
	return dmConn.Conn.Close()
}

func newDmQuery(query string, args []driver.NamedValue) (dmquery string) {
	dmquery = strings.ReplaceAll(query, "`", "\"")
	return dmquery
}
