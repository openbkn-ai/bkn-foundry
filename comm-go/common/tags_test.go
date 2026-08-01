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

func TestTagSlice2TagString(t *testing.T) {
	Convey("Test TagSlice2TagString", t, func() {
		Convey("empty", func() {
			actual := TagSlice2TagString([]string{})
			So(actual, ShouldEqual, "")
		})

		Convey("not empty", func() {
			actual := TagSlice2TagString([]string{"123", "456", "678"})
			So(actual, ShouldEqual, `"123","456","678"`)
		})
	})
}

func TestTagString2TagSlice(t *testing.T) {
	Convey("Test TagString2TagSlice", t, func() {
		Convey("empty", func() {
			actual := TagString2TagSlice("")
			So(actual, ShouldResemble, []string{})
		})

		Convey("len 1", func() {
			actual := TagString2TagSlice(`"123"`)
			So(actual, ShouldResemble, []string{"123"})
		})

		Convey("len 3", func() {
			actual := TagString2TagSlice(`"123","456","678"`)
			So(actual, ShouldResemble, []string{"123", "456", "678"})
		})
	})
}

func TestTagSliceTransform(t *testing.T) {
	Convey("Test Transform", t, func() {
		input := []string{"banana", "apple ", " apple", "orange", "banana", " grape "}
		expected := []string{"apple", "banana", "grape", "orange"}
		actual := TagSliceTransform(input)
		So(actual, ShouldResemble, expected)
	})
}
