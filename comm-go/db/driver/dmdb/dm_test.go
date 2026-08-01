// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dmdb

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

type TestDBInfo struct {
	Host     string
	Port     int
	Username string
	Password string
}

func getTestDBInfo() TestDBInfo {
	user := os.Getenv("DM_TEST_USER")
	password := os.Getenv("DM_TEST_PASSWORD")
	host := os.Getenv("DM_TEST_HOST")
	port, err := strconv.Atoi(os.Getenv("DM_TEST_PORT"))
	if err != nil {
		log.Fatalf("DM_TEST_PORT is not a number: %v", err)
	}
	return TestDBInfo{
		Host:     host,
		Port:     port,
		Username: user,
		Password: password,
	}
}

func TestOpen(t *testing.T) {
	Convey("Test dmdb.Open\n", t, func() {
		info := getTestDBInfo()
		Convey("Open fail,no slash\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)SYSDBA?timeout=10s", info.Username, info.Password, info.Host, info.Port)
			_, err := Open(dsn)
			So(err, ShouldEqual, errors.New("invalid DSN: invalidFormat"))
		})
		Convey("Open fail,missing symbol\n", func() {
			dsn := fmt.Sprintf("%s:%stcp(%s:%d/SYSDBA?timeout=10s", info.Username, info.Password, info.Host, info.Port)
			_, err := Open(dsn)
			So(err, ShouldEqual, errors.New("invalid DSN: invalidFormat"))
		})
		Convey("Open fail,change fail,invalid time unit suffix\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10xxs", info.Username, info.Password, info.Host, info.Port)
			_, err := Open(dsn)
			So(err, ShouldEqual, errors.New("time: unknown unit \"xxs\" in duration \"10xxs\""))
		})
		Convey("Open success,test invalid param continue\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout", info.Username, info.Password, info.Host, info.Port)
			_, err := Open(dsn)
			So(err, ShouldBeNil)
		})
		Convey("Open success,case param two\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?readTimeout=10s&timeout=10s", info.Username, info.Password, info.Host, info.Port)
			_, err := Open(dsn)
			So(err, ShouldBeNil)
		})
		Convey("Open success\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&autocommit=true&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
			_, err := Open(dsn)
			So(err, ShouldBeNil)
		})
	})
}

func TestOpenConnector(t *testing.T) {
	Convey("Test dmdb.OpenConnector\n", t, func() {
		info := getTestDBInfo()
		Convey("OpenConnector fail,no slash\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)SYSDBA?timeout=10s", info.Username, info.Password, info.Host, info.Port)
			_, err := OpenConnector(dsn)
			So(err, ShouldEqual, errors.New("invalid DSN: invalidFormat"))
		})
		Convey("OpenConnector fail,missing symbol\n", func() {
			dsn := fmt.Sprintf("%s:%stcp(%s:%d/SYSDBA?timeout=10s", info.Username, info.Password, info.Host, info.Port)
			_, err := OpenConnector(dsn)
			So(err, ShouldEqual, errors.New("invalid DSN: invalidFormat"))
		})
		Convey("OpenConnector fail,change fail,invalid time unit suffix\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10xxs", info.Username, info.Password, info.Host, info.Port)
			_, err := OpenConnector(dsn)
			So(err, ShouldEqual, errors.New("time: unknown unit \"xxs\" in duration \"10xxs\""))
		})
		Convey("OpenConnector success,test invalid param continue\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout", info.Username, info.Password, info.Host, info.Port)
			_, err := OpenConnector(dsn)
			So(err, ShouldBeNil)
		})
		Convey("OpenConnector success,case param two\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?readTimeout=10s&timeout=10s", info.Username, info.Password, info.Host, info.Port)
			_, err := OpenConnector(dsn)
			So(err, ShouldBeNil)
		})
		Convey("OpenConnector success\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&autocommit=true&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
			_, err := OpenConnector(dsn)
			So(err, ShouldBeNil)
		})
	})
}
