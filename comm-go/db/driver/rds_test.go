// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driver

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	. "github.com/smartystreets/goconvey/convey"
)

const (
	DBType_MARIADB = "MARIADB"
	DBType_DM8     = "DM8"
	DBType_KDB9    = "KDB9"
)

type TestDBInfo struct {
	Host     string
	Port     int
	Username string
	Password string
}

func getTestDBInfo(db_type string) TestDBInfo {
	db_type = strings.ToUpper(db_type)
	switch db_type {
	case "MARIADB":
		user := os.Getenv("MYSQL_TEST_USER")
		password := os.Getenv("MYSQL_TEST_PASSWORD")
		host := os.Getenv("MYSQL_TEST_HOST")
		port, err := strconv.Atoi(os.Getenv("MYSQL_TEST_PORT"))
		if err != nil {
			log.Fatalf("MYSQL_TEST_PORT is not a number: %v", err)
		}
		return TestDBInfo{
			Host:     host,
			Port:     port,
			Username: user,
			Password: password,
		}
	case "DM8":
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
	case "KDB9":
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
	default:
		log.Fatalf("DB_TYPE %s is not supported", db_type)
	}
	return TestDBInfo{}
}

func TestOpen_MARIADB(t *testing.T) {
	err := os.Setenv("DB_TYPE", DBType_MARIADB)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	Convey("Test db.Open\n", t, func() {
		info := getTestDBInfo(DBType_MARIADB)
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?timeout=10s", info.Username, info.Password, info.Host, info.Port)
		Convey("Error dsn, return error\n", func() {
			_, err = RDSDriver{}.Open("xxxxx")
			So(err, ShouldNotBeNil)
		})
		Convey("Open fail\n", func() {
			_, err := RDSDriver{}.Open("/test?timeout=10s&readTimeout=10s")
			So(err, ShouldNotBeNil)
		})
		Convey("Open fail,invalid time param\n", func() {
			errdsn := dsn + "&timeout=10xxxxxs"
			_, err := RDSDriver{}.Open(errdsn)
			So(err, ShouldNotBeNil)
		})
		Convey("Open ok\n", func() {
			_, err := RDSDriver{}.Open(dsn)
			So(err, ShouldBeNil)
		})
	})
}

func TestOpen_DM8(t *testing.T) {
	err := os.Setenv("DB_TYPE", DBType_DM8)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	Convey("Test db.Open\n", t, func() {
		info := getTestDBInfo(DBType_DM8)
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
		Convey("Error dsn, return error\n", func() {
			_, err = RDSDriver{}.Open("xxxxx")
			So(err, ShouldNotBeNil)
		})
		Convey("Open ok\n", func() {
			_, err := RDSDriver{}.Open(dsn)
			So(err, ShouldBeNil)
		})
	})
}

func TestOpen_KDB9(t *testing.T) {
	err := os.Setenv("DB_TYPE", DBType_KDB9)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	Convey("Test db.Open\n", t, func() {
		info := getTestDBInfo(DBType_KDB9)
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/openbkn?timeout=10s", info.Username, info.Password, info.Host, info.Port)
		Convey("Error dsn, return error\n", func() {
			_, err = RDSDriver{}.Open("xxxxx")
			So(err, ShouldNotBeNil)
		})
		Convey("Open ok\n", func() {
			_, err := RDSDriver{}.Open(dsn)
			So(err, ShouldBeNil)
		})
	})
}

func TestInit(t *testing.T) {
	err := os.Setenv("DB_TYPE", "DM8")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	Convey("Test init\n", t, func() {
		info := getTestDBInfo(DBType_DM8)
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
		Convey("Error driver name, return error\n", func() {
			_, err := sql.Open("xxx", dsn)
			So(err, ShouldNotBeNil)
		})
		Convey("openbkn-rds driver\n", func() {
			_, err := sql.Open("openbkn-rds", dsn)
			So(err, ShouldBeNil)
		})
	})
}

func TestOpenConnector_MARIADB(t *testing.T) {
	err := os.Setenv("DB_TYPE", DBType_MARIADB)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	Convey("Test db.OpenConnector\n", t, func() {
		info := getTestDBInfo(DBType_MARIADB)
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?timeout=10s", info.Username, info.Password, info.Host, info.Port)
		Convey("Error dsn, return error\n", func() {
			_, err = RDSDriver{}.OpenConnector("xxxxx")
			So(err, ShouldNotBeNil)
		})
		Convey("OpenConnector fail\n", func() {
			_, err := RDSDriver{}.OpenConnector("test?timeout=10s&readTimeout=10s")
			So(err, ShouldNotBeNil)
		})
		Convey("OpenConnector fail,invalid time param\n", func() {
			errdsn := dsn + "&timeout=10xxxxxs"
			_, err := RDSDriver{}.OpenConnector(errdsn)
			So(err, ShouldNotBeNil)
		})
		Convey("OpenConnector ok\n", func() {
			_, err := RDSDriver{}.OpenConnector(dsn)
			So(err, ShouldBeNil)
		})
	})
}

func TestOpenConnector_DM8(t *testing.T) {
	err := os.Setenv("DB_TYPE", DBType_DM8)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	Convey("Test db.OpenConnector\n", t, func() {
		info := getTestDBInfo(DBType_DM8)
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/SYSDBA?timeout=10s&readTimeout=10s", info.Username, info.Password, info.Host, info.Port)
		Convey("Error dsn, return error\n", func() {
			_, err = RDSDriver{}.OpenConnector("xxxxx")
			So(err, ShouldNotBeNil)
		})
		Convey("OpenConnector ok\n", func() {
			_, err := RDSDriver{}.OpenConnector(dsn)
			So(err, ShouldBeNil)
		})
	})
}

func TestOpenConnector_KDB9(t *testing.T) {
	err := os.Setenv("DB_TYPE", DBType_KDB9)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	Convey("Test db.OpenConnector\n", t, func() {
		info := getTestDBInfo(DBType_KDB9)
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/openbkn?timeout=10s", info.Username, info.Password, info.Host, info.Port)
		Convey("Error dsn, return error\n", func() {
			_, err = RDSDriver{}.OpenConnector("xxxxx")
			So(err, ShouldNotBeNil)
		})
		Convey("OpenConnector ok\n", func() {
			_, err := RDSDriver{}.OpenConnector(dsn)
			So(err, ShouldBeNil)
		})
	})
}
