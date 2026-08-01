// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestParseDSN(t *testing.T) {
	Convey("Test common.ParseDSN\n", t, func() {
		Convey("basic tcp format", func() {
			dsn := "user:pass@tcp(127.0.0.1:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "127.0.0.1")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
			So(got.Props.Size(), ShouldEqual, 0)
		})
		Convey("tcp format with query params", func() {
			dsn := "user:pass@tcp(localhost:3306)/mydb?charset=utf8&timeout=10s"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "localhost")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
			value, _ := got.Props.Get("charset")
			So(value, ShouldEqual, "utf8")
			value, _ = got.Props.Get("timeout")
			So(value, ShouldEqual, "10s")
		})
		Convey("simple host:port format", func() {
			dsn := "user:pass@192.168.1.1:3307/testdb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "192.168.1.1")
			So(got.Port, ShouldEqual, "3307")
			So(got.Protocol, ShouldEqual, "")
			So(got.DBName, ShouldEqual, "testdb")
		})
		Convey("unix socket format", func() {
			dsn := "user:pass@unix(/var/run/mysql.sock)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "/var/run/mysql.sock")
			So(got.Port, ShouldEqual, "")
			So(got.Protocol, ShouldEqual, "unix")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("with complex query params", func() {
			dsn := "root:123456@tcp(10.0.0.1:3306)/production?parseTime=true&loc=Local&multiStatements=true"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "root")
			So(got.Password, ShouldEqual, "123456")
			So(got.Host, ShouldEqual, "10.0.0.1")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "production")
			value, _ := got.Props.Get("parseTime")
			So(value, ShouldEqual, "true")
			value, _ = got.Props.Get("loc")
			So(value, ShouldEqual, "Local")
			value, _ = got.Props.Get("multiStatements")
			So(value, ShouldEqual, "true")
		})
		Convey("without password", func() {
			dsn := "user@tcp(localhost:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "")
			So(got.Host, ShouldEqual, "localhost")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("without schema", func() {
			dsn := "user:pass@tcp(localhost:3306)/"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "localhost")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "")
		})
		Convey("missing @ separator", func() {
			dsn := "user:passtcp(localhost:3306)/mydb"
			_, err := ParseDSN(dsn)
			So(err, ShouldNotBeNil)
		})
		Convey("missing right parenthesis", func() {
			dsn := "user:pass@tcp(localhost:3306/mydb"
			_, err := ParseDSN(dsn)
			So(err, ShouldNotBeNil)
		})
		Convey("missing slash", func() {
			dsn := "user:pass@tcp(localhost:3306)mydb"
			_, err := ParseDSN(dsn)
			So(err, ShouldNotBeNil)
		})
		Convey("empty query param value", func() {
			dsn := "user:pass@tcp(localhost:3306)/mydb?flag"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "localhost")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
			So(got.Props.Size(), ShouldEqual, 0)
		})
		Convey("tcp without port", func() {
			dsn := "user:pass@tcp(localhost)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "localhost")
			So(got.Port, ShouldEqual, "")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("special characters in password", func() {
			dsn := "user:p@ss:word@tcp(localhost:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "p@ss:word")
			So(got.Host, ShouldEqual, "localhost")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("empty DSN", func() {
			dsn := ""
			_, err := ParseDSN(dsn)
			So(err, ShouldNotBeNil)
		})
		Convey("only username", func() {
			dsn := "user@tcp(localhost:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "")
			So(got.Host, ShouldEqual, "localhost")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("param with multiple equals", func() {
			dsn := "user:pass@tcp(localhost:3306)/mydb?param=a=b=c"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			value, _ := got.Props.Get("param")
			So(value, ShouldEqual, "a=b=c")
		})
		Convey("empty query string", func() {
			dsn := "user:pass@tcp(localhost:3306)/mydb?"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Props.Size(), ShouldEqual, 0)
		})
		Convey("multiple @ symbols", func() {
			dsn := "user@pass@tcp(localhost:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user@pass")
			So(got.Password, ShouldEqual, "")
			So(got.Host, ShouldEqual, "localhost")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("left parenthesis at start", func() {
			dsn := "user:pass@(localhost:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "(localhost")
			So(got.Port, ShouldEqual, "3306)")
			So(got.Protocol, ShouldEqual, "")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("IPv4 address", func() {
			dsn := "user:pass@tcp(192.168.1.100:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "192.168.1.100")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("IPv6 address", func() {
			dsn := "user:pass@tcp([::1]:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "[::1]")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("IPv6 full address", func() {
			dsn := "user:pass@tcp([2001:db8::1]:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "[2001:db8::1]")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("hostname", func() {
			dsn := "user:pass@tcp(myserver.example.com:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "myserver.example.com")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("hostname with subdomain", func() {
			dsn := "user:pass@tcp(db.prod.example.com:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "db.prod.example.com")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("IPv6 without port", func() {
			dsn := "user:pass@tcp([::1])/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "[::1]")
			So(got.Port, ShouldEqual, "")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("IPv6 localhost", func() {
			dsn := "user:pass@tcp([fe80::1]:3306)/mydb"
			got, err := ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(got.Username, ShouldEqual, "user")
			So(got.Password, ShouldEqual, "pass")
			So(got.Host, ShouldEqual, "[fe80::1]")
			So(got.Port, ShouldEqual, "3306")
			So(got.Protocol, ShouldEqual, "tcp")
			So(got.DBName, ShouldEqual, "mydb")
		})
		Convey("invalid IPv6 missing closing bracket", func() {
			dsn := "user:pass@tcp([::1:3306)/mydb"
			_, err := ParseDSN(dsn)
			So(err, ShouldNotBeNil)
		})
	})
}
