// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dmdb

import (
	"context"
	"database/sql/driver"
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestExecContext(t *testing.T) {
	Convey("Test dmdb.ExecContext\n", t, func() {
		info := getTestDBInfo()
		Convey("ExecContext fail\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&autocommit=true&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
			dmConn, err := Open(dsn)
			So(err, ShouldBeNil)
			_, err = dmConn.(driver.ExecerContext).ExecContext(context.Background(), "insert t1(id) values 1", nil)
			So(err, ShouldNotBeNil)
		})
		Convey("ExecContext success\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&autocommit=true&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
			dmConn, err := Open(dsn)
			So(err, ShouldBeNil)
			_, err = dmConn.(driver.ExecerContext).ExecContext(context.Background(), "CREATE TABLE IF NOT EXISTS t1(id int)", []driver.NamedValue{})
			So(err, ShouldBeNil)
			_, err = dmConn.(driver.ExecerContext).ExecContext(context.Background(), "insert t1(id) values (1)", []driver.NamedValue{})
			So(err, ShouldBeNil)
			_, err = dmConn.(driver.ExecerContext).ExecContext(context.Background(), "DROP TABLE IF EXISTS t1", []driver.NamedValue{})
			So(err, ShouldBeNil)
		})
	})
}

func TestQueryContext(t *testing.T) {
	Convey("Test dmdb.QueryContext\n", t, func() {
		info := getTestDBInfo()
		Convey("QueryContext fail\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&autocommit=true&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
			dmConn, err := Open(dsn)
			So(err, ShouldBeNil)
			_, err = dmConn.(driver.QueryerContext).QueryContext(context.Background(), "selectt 1", nil)
			So(err, ShouldNotBeNil)
		})
		Convey("QueryContext success\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&autocommit=true&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
			dmConn, err := Open(dsn)
			So(err, ShouldBeNil)
			_, err = dmConn.(driver.QueryerContext).QueryContext(context.Background(), "select 1", nil)
			So(err, ShouldBeNil)
		})
	})
}

func TestPrepareContext(t *testing.T) {
	Convey("Test dmdb.PrepareContext\n", t, func() {
		info := getTestDBInfo()
		Convey("PrepareContext fail,no slash\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&autocommit=true&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
			dmConn, err := Open(dsn)
			So(err, ShouldBeNil)
			_, err = dmConn.(driver.ConnPrepareContext).PrepareContext(context.Background(), "selectt 1")
			So(err, ShouldNotBeNil)
		})
		Convey("PrepareContext success\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&autocommit=true&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
			dmConn, err := Open(dsn)
			So(err, ShouldBeNil)
			_, err = dmConn.(driver.ConnPrepareContext).PrepareContext(context.Background(), "select 1")
			So(err, ShouldBeNil)
		})
	})
}
