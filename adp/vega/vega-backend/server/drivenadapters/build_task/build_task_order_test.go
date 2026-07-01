// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package build_task

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"vega-backend/interfaces"
)

func Test_buildOrderByClause(t *testing.T) {
	Convey("buildOrderByClause\n", t, func() {

		Convey("default: 活跃置顶(桶 ASC)+ 桶内最新在前,忽略 order\n", func() {
			clause := buildOrderByClause(interfaces.BuildTaskOrderByDefault, "asc")
			So(clause, ShouldContainSubstring, "CASE f_status")
			So(clause, ShouldContainSubstring, "WHEN 'running' THEN 1")
			So(clause, ShouldContainSubstring, "WHEN 'completed' THEN 6")
			So(clause, ShouldEndWith, "END ASC, f_create_time DESC")
		})

		Convey("unknown order_by 兜底为 default\n", func() {
			So(buildOrderByClause("bogus", "desc"), ShouldEndWith, "END ASC, f_create_time DESC")
		})

		Convey("created_at 跟 order 方向,无平手键\n", func() {
			So(buildOrderByClause(interfaces.BuildTaskOrderByCreatedAt, "asc"), ShouldEqual, "f_create_time ASC")
			So(buildOrderByClause(interfaces.BuildTaskOrderByCreatedAt, "desc"), ShouldEqual, "f_create_time DESC")
		})

		Convey("updated_at 跟 order 方向,无平手键\n", func() {
			So(buildOrderByClause(interfaces.BuildTaskOrderByUpdatedAt, "asc"), ShouldEqual, "f_update_time ASC")
			So(buildOrderByClause(interfaces.BuildTaskOrderByUpdatedAt, "desc"), ShouldEqual, "f_update_time DESC")
		})

		Convey("status 桶序跟 order 方向 + 平手 create DESC\n", func() {
			So(buildOrderByClause(interfaces.BuildTaskOrderByStatus, "asc"), ShouldEndWith, "END ASC, f_create_time DESC")
			So(buildOrderByClause(interfaces.BuildTaskOrderByStatus, "desc"), ShouldEndWith, "END DESC, f_create_time DESC")
		})

		Convey("mode 跟 order 方向 + 平手 create DESC\n", func() {
			So(buildOrderByClause(interfaces.BuildTaskOrderByMode, "asc"), ShouldEqual, "f_mode ASC, f_create_time DESC")
		})
	})
}

func Test_statusBucketCase(t *testing.T) {
	Convey("statusBucketCase 覆盖全部 6 状态且优先级与 BuildTaskStatusOrder 一致\n", t, func() {
		clause := statusBucketCase()
		for i, s := range interfaces.BuildTaskStatusOrder {
			So(clause, ShouldContainSubstring, "WHEN '"+s+"' THEN ")
			_ = i
		}
		So(clause, ShouldStartWith, "CASE f_status")
		So(clause, ShouldEndWith, "ELSE 99 END")
	})
}
