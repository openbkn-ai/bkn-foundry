// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func Test_Convert_StringToStringSlice(t *testing.T) {
	Convey("Test StringToStringSlice", t, func() {
		Convey("success 3->3", func() {
			result := StringToStringSlice("1, 2, 3")
			So(result, ShouldResemble, []string{"1", "2", "3"})
		})
		Convey("success 3->2", func() {
			result := StringToStringSlice("1, 2, ")
			So(result, ShouldResemble, []string{"1", "2"})
		})
		Convey("empty", func() {
			result := StringToStringSlice("")
			So(result, ShouldResemble, []string{})
		})
	})
}

func Test_Convert_BytesToGiB(t *testing.T) {
	Convey("Test BytesToGiB", t, func() {
		Convey("BytesToGiB", func() {
			actual := BytesToGiB(1024)
			So(actual, ShouldEqual, 0)
		})
		Convey("BytesToGiB2", func() {
			actual := BytesToGiB(1024 * 1024 * 1024)
			So(actual, ShouldEqual, 1)
		})
		Convey("BytesToGiB3", func() {
			actual := BytesToGiB(1024 * 1024 * 1024 * 10)
			So(actual, ShouldEqual, 10)
		})
	})
}

func Test_Convert_GiBToBytes(t *testing.T) {
	Convey("Test GiBToBytes", t, func() {
		Convey("GiBToBytes", func() {
			actual := GiBToBytes(1)
			So(actual, ShouldEqual, 1024*1024*1024)
		})
		Convey("GiBToBytes2", func() {
			actual := GiBToBytes(10)
			So(actual, ShouldEqual, 1024*1024*1024*10)
		})
		Convey("GiBToBytes3", func() {
			actual := GiBToBytes(0)
			So(actual, ShouldEqual, 0)
		})
	})
}

func Test_Convert_AnyToFloat64(t *testing.T) {
	Convey("Test AnyToFloat64", t, func() {
		Convey("json.Number", func() {
			actual, err := AnyToFloat64(json.Number("0.95"))
			So(err, ShouldBeNil)
			So(actual, ShouldAlmostEqual, 0.95)
		})

		Convey("legacy float64", func() {
			actual, err := AnyToFloat64(float64(0.95))
			So(err, ShouldBeNil)
			So(actual, ShouldAlmostEqual, 0.95)
		})

		Convey("invalid value", func() {
			_, err := AnyToFloat64([]int{1})
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_Convert_DuplicateSlice(t *testing.T) {
	Convey("Test DuplicateSlice", t, func() {
		Convey("Success with duplicates\n", func() {
			input := []string{"a", "b", "a", "c", "b", "d"}
			result := DuplicateSlice(input)
			So(len(result), ShouldEqual, 4)
			So(result, ShouldContain, "a")
			So(result, ShouldContain, "b")
			So(result, ShouldContain, "c")
			So(result, ShouldContain, "d")
		})

		Convey("Success with no duplicates\n", func() {
			input := []string{"a", "b", "c"}
			result := DuplicateSlice(input)
			So(len(result), ShouldEqual, 3)
			So(result, ShouldResemble, []string{"a", "b", "c"})
		})

		Convey("Success with empty slice\n", func() {
			input := []string{}
			result := DuplicateSlice(input)
			So(len(result), ShouldEqual, 0)
			So(result, ShouldNotBeNil)
		})

		Convey("Success with nil slice\n", func() {
			var input []string
			result := DuplicateSlice(input)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Success with all same elements\n", func() {
			input := []string{"a", "a", "a", "a"}
			result := DuplicateSlice(input)
			So(len(result), ShouldEqual, 1)
			So(result[0], ShouldEqual, "a")
		})
	})
}
