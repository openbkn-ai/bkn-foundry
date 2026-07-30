// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dmdb

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-comm-go/db/driver/common"
)

func TestFormatDSN(t *testing.T) {
	Convey("Test dmdb.FormatDSN\n", t, func() {
		Convey("basic format", func() {
			dsn := "username:password@tcp(localhost:5237)/test"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "dm://username:password@localhost:5237?schema=test&compatibleMode=mysql&escapeProcess=true&svcConfPath=/tmp/dm_svc.conf")
		})
		Convey("with timeout", func() {
			dsn := "username:password@tcp(localhost:5237)/test?timeout=10s"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "dm://username:password@localhost:5237?schema=test&compatibleMode=mysql&connectTimeout=10000&escapeProcess=true&svcConfPath=/tmp/dm_svc.conf")
		})
		Convey("with autocommit", func() {
			dsn := "username:password@tcp(localhost:5237)/test?autocommit=true"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "dm://username:password@localhost:5237?schema=test&autoCommit=true&compatibleMode=mysql&escapeProcess=true&svcConfPath=/tmp/dm_svc.conf")
		})
		Convey("with multiple params", func() {
			dsn := "username:password@tcp(localhost:5237)/test?timeout=10s&autocommit=true"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "dm://username:password@localhost:5237?schema=test&autoCommit=true&compatibleMode=mysql&connectTimeout=10000&escapeProcess=true&svcConfPath=/tmp/dm_svc.conf")
		})
		Convey("IPv4 address", func() {
			dsn := "username:password@tcp(192.168.1.100:5237)/test"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "dm://username:password@192.168.1.100:5237?schema=test&compatibleMode=mysql&escapeProcess=true&svcConfPath=/tmp/dm_svc.conf")
		})
		Convey("IPv6 address", func() {
			dsn := "username:password@tcp([::1]:5237)/test"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "dm://username:password@[::1]:5237?schema=test&compatibleMode=mysql&escapeProcess=true&svcConfPath=/tmp/dm_svc.conf")
		})
		Convey("hostname", func() {
			dsn := "username:password@tcp(myserver.example.com:5237)/test"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "dm://username:password@myserver.example.com:5237?schema=test&compatibleMode=mysql&escapeProcess=true&svcConfPath=/tmp/dm_svc.conf")
		})
		Convey("special characters in password", func() {
			dsn := "username:&#%*@#.com123@tcp(localhost:5237)/test"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			So(cfg.Password, ShouldEqual, "&#%*@#.com123")
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "dm://username:&#%*@#.com123@localhost:5237?schema=test&compatibleMode=mysql&escapeProcess=true&svcConfPath=/tmp/dm_svc.conf")
		})
	})
}
