// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package kingbase

import (
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
	user := os.Getenv("KDB_TEST_USER")
	password := os.Getenv("KDB_TEST_PASSWORD")
	host := os.Getenv("KDB_TEST_HOST")
	port, err := strconv.Atoi(os.Getenv("KDB_TEST_PORT"))
	if err != nil {
		log.Fatalf("KDB_TEST_PORT is not a number: %v", err)
	}
	return TestDBInfo{
		Host:     host,
		Port:     port,
		Username: user,
		Password: password,
	}
}

func TestOpen(t *testing.T) {
	Convey("Test kingbase.Open\n", t, func() {
		info := getTestDBInfo()
		Convey("Open fail\n", func() {
			error_port := 12345
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/", info.Username, info.Password, info.Host, error_port)
			_, err := Open(dsn)
			So(err, ShouldNotBeNil)
		})
		Convey("Open success\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/", info.Username, info.Password, info.Host, info.Port)
			_, err := Open(dsn)
			So(err, ShouldBeNil)
		})
	})
}

func TestOpenConnector(t *testing.T) {
	Convey("Test kingbase.OpenConnector\n", t, func() {
		info := getTestDBInfo()
		Convey("Open fail\n", func() {
			error_port := 12345
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)test", info.Username, info.Password, info.Host, error_port)
			_, err := OpenConnector(dsn)
			So(err, ShouldNotBeNil)
		})
		Convey("OpenConnector success\n", func() {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/", info.Username, info.Password, info.Host, info.Port)
			_, err := OpenConnector(dsn)
			So(err, ShouldBeNil)
		})
	})
}
