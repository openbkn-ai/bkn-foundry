// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/smartystreets/goconvey/convey"

	"vega-backend/interfaces"
)

// newListCtx 造一个仅带 query 的 GET 测试上下文。
func newListCtx(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/build-tasks?"+query, nil)
	return c
}

func Test_parseBuildTaskStatuses(t *testing.T) {
	Convey("parseBuildTaskStatuses\n", t, func() {
		ctx := context.Background()

		Convey("single valid\n", func() {
			ss, err := parseBuildTaskStatuses(ctx, "running")
			So(err, ShouldBeNil)
			So(ss, ShouldResemble, []string{"running"})
		})

		Convey("multi valid with spaces\n", func() {
			ss, err := parseBuildTaskStatuses(ctx, "running, init")
			So(err, ShouldBeNil)
			So(ss, ShouldResemble, []string{"running", "init"})
		})

		Convey("one invalid value -> error\n", func() {
			_, err := parseBuildTaskStatuses(ctx, "running,unknown")
			So(err, ShouldNotBeNil)
		})

		Convey("only empty segments -> empty slice\n", func() {
			ss, err := parseBuildTaskStatuses(ctx, ", , ")
			So(err, ShouldBeNil)
			So(len(ss), ShouldEqual, 0)
		})
	})
}

func Test_isValidBuildTaskOrderBy(t *testing.T) {
	Convey("isValidBuildTaskOrderBy\n", t, func() {
		So(isValidBuildTaskOrderBy(interfaces.BuildTaskOrderByDefault), ShouldBeTrue)
		So(isValidBuildTaskOrderBy(interfaces.BuildTaskOrderByCreatedAt), ShouldBeTrue)
		So(isValidBuildTaskOrderBy(interfaces.BuildTaskOrderByUpdatedAt), ShouldBeTrue)
		So(isValidBuildTaskOrderBy(interfaces.BuildTaskOrderByStatus), ShouldBeTrue)
		So(isValidBuildTaskOrderBy(interfaces.BuildTaskOrderByMode), ShouldBeTrue)
		So(isValidBuildTaskOrderBy("progress"), ShouldBeFalse)
		So(isValidBuildTaskOrderBy(""), ShouldBeFalse)
	})
}

func Test_parseBuildTaskListParams(t *testing.T) {
	Convey("parseBuildTaskListParams\n", t, func() {
		ctx := context.Background()

		Convey("defaults when no query\n", func() {
			p, err := parseBuildTaskListParams(ctx, newListCtx(""))
			So(err, ShouldBeNil)
			So(p.Offset, ShouldEqual, 0)
			So(p.Limit, ShouldEqual, 20)
			So(p.OrderBy, ShouldEqual, interfaces.BuildTaskOrderByDefault)
			So(p.Order, ShouldEqual, interfaces.DESC_DIRECTION)
			So(len(p.Statuses), ShouldEqual, 0)
		})

		Convey("active=true -> running+init, overrides status\n", func() {
			p, err := parseBuildTaskListParams(ctx, newListCtx("active=true&status=completed"))
			So(err, ShouldBeNil)
			So(p.Statuses, ShouldResemble, []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusInit})
		})

		Convey("multi-value status\n", func() {
			p, err := parseBuildTaskListParams(ctx, newListCtx("status=running,init"))
			So(err, ShouldBeNil)
			So(p.Statuses, ShouldResemble, []string{"running", "init"})
		})

		Convey("order_by + order honored\n", func() {
			p, err := parseBuildTaskListParams(ctx, newListCtx("order_by=created_at&order=asc"))
			So(err, ShouldBeNil)
			So(p.OrderBy, ShouldEqual, interfaces.BuildTaskOrderByCreatedAt)
			So(p.Order, ShouldEqual, interfaces.ASC_DIRECTION)
		})

		Convey("invalid order_by -> error\n", func() {
			_, err := parseBuildTaskListParams(ctx, newListCtx("order_by=bogus"))
			So(err, ShouldNotBeNil)
		})

		Convey("invalid order -> error\n", func() {
			_, err := parseBuildTaskListParams(ctx, newListCtx("order=sideways"))
			So(err, ShouldNotBeNil)
		})

		Convey("invalid status -> error\n", func() {
			_, err := parseBuildTaskListParams(ctx, newListCtx("status=running,nope"))
			So(err, ShouldNotBeNil)
		})

		Convey("invalid mode -> error\n", func() {
			_, err := parseBuildTaskListParams(ctx, newListCtx("mode=nope"))
			So(err, ShouldNotBeNil)
		})

		Convey("negative offset -> error\n", func() {
			_, err := parseBuildTaskListParams(ctx, newListCtx("offset=-1"))
			So(err, ShouldNotBeNil)
		})

		Convey("limit=-1 (no-limit) allowed\n", func() {
			p, err := parseBuildTaskListParams(ctx, newListCtx("limit=-1"))
			So(err, ShouldBeNil)
			So(p.Limit, ShouldEqual, -1)
		})
	})
}
