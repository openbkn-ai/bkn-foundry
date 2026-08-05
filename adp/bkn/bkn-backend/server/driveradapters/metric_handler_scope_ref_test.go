// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestSplitScopeRefs(t *testing.T) {
	Convey("splitScopeRefs parses the scope_ref query parameter", t, func() {
		Convey("empty input yields no filter", func() {
			So(splitScopeRefs(""), ShouldBeNil)
			So(splitScopeRefs("   "), ShouldBeNil)
			So(splitScopeRefs(" , , "), ShouldBeEmpty)
		})

		Convey("single id", func() {
			So(splitScopeRefs(" ot1 "), ShouldResemble, []string{"ot1"})
		})

		Convey("comma separated ids are trimmed and de-duplicated", func() {
			So(splitScopeRefs("ot1, ot2 ,ot1,,ot3"), ShouldResemble, []string{"ot1", "ot2", "ot3"})
		})
	})
}
