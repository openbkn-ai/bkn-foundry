// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package kingbase

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-comm-go/db/driver/common"
)

func TestFormatDSN(t *testing.T) {
	Convey("Test kingbase.FormatDSN\n", t, func() {
		Convey("case1", func() {
			dsn := "username:password@tcp(localhost:3306)/test?timeout=10s&readTimeout=10s&writeTimeout=10s&autocommit=true"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "user=username password=password host=localhost port=3306 search_path=test connect_timeout=10 sslmode=disable dbname=openbkn")
		})
		Convey("case2", func() {
			dsn := "username:&#%*#.com123@tcp(localhost:3306)/test"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "user=username password=&#%*#.com123 host=localhost port=3306 search_path=test sslmode=disable dbname=openbkn")
		})
		Convey("case3", func() {
			dsn := "username:password@tcp(localhost:3306)/"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "user=username password=password host=localhost port=3306 sslmode=disable dbname=openbkn")
		})
		Convey("case4", func() {
			dsn := "username:password@tcp(localhost:3306)/?timeout=10s&readTimeout=10s&writeTimeout=10s&autocommit=true"
			cfg, err := common.ParseDSN(dsn)
			So(err, ShouldBeNil)
			got, err := FormatDSN(cfg)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "user=username password=password host=localhost port=3306 connect_timeout=10 sslmode=disable dbname=openbkn")
		})
	})
}
