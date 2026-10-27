// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func releaseRow(id, networkID string, createTime int64) []driver.Value {
	return []driver.Value{id, "orders", networkID, "1.0.0", "0.9.0", "p1", "u1", "user", "first", "live", createTime, createTime}
}

func Test_ReleaseAccess(t *testing.T) {
	Convey("Test releaseAccess\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		ra := &releaseAccess{db: db}
		ctx := context.Background()

		Convey("ListReleases reads the first page newest first\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta(
				"SELECT f_id, f_lineage_id, f_network_id, f_version, f_base_version, f_project_id, f_publisher, f_publisher_type, f_notes, f_state, f_create_time, f_update_time " +
					"FROM t_bkn_project_release WHERE f_lineage_id = ? ORDER BY f_create_time DESC, f_id DESC LIMIT 3")).
				WithArgs("orders").
				WillReturnRows(sqlmock.NewRows(releaseColumns).AddRow(releaseRow("r2", "orders_1-0-0", 2)...))

			releases, err := ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: "orders", Limit: 3})

			So(err, ShouldBeNil)
			So(releases, ShouldHaveLength, 1)
			So(releases[0].NetworkID, ShouldEqual, "orders_1-0-0")
			So(releases[0].Publisher, ShouldResemble, interfaces.AccountInfo{ID: "u1", Type: "user"})
			So(releases[0].State, ShouldEqual, interfaces.RELEASE_STATE_LIVE)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("ListReleases continues after the cursor\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta(
				"WHERE f_lineage_id = ? AND (f_create_time < ? OR (f_create_time = ? AND f_id < ?)) ORDER BY f_create_time DESC, f_id DESC LIMIT 2")).
				WithArgs("orders", int64(5), int64(5), "r5").
				WillReturnRows(sqlmock.NewRows(releaseColumns))

			releases, err := ra.ListReleases(ctx, interfaces.ReleasesQuery{
				LineageID: "orders", Limit: 2, Cursor: &interfaces.ReleaseCursor{CreateTime: 5, ID: "r5"},
			})

			So(err, ShouldBeNil)
			So(releases, ShouldNotBeNil)
			So(releases, ShouldBeEmpty)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("ListReleases returns the query error\n", func() {
			smock.ExpectQuery("SELECT .* FROM t_bkn_project_release").WillReturnError(errors.New("db down"))

			_, err := ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: "orders", Limit: 1})

			So(err, ShouldNotBeNil)
		})

		Convey("CountReleases counts the lineage\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(f_id) FROM t_bkn_project_release WHERE f_lineage_id = ?")).
				WithArgs("orders").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(4)))

			total, err := ra.CountReleases(ctx, "orders")

			So(err, ShouldBeNil)
			So(total, ShouldEqual, 4)
		})

		Convey("GetReleaseByNetworkID finds the release\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("FROM t_bkn_project_release WHERE f_network_id = ?")).
				WithArgs("orders_1-0-0").
				WillReturnRows(sqlmock.NewRows(releaseColumns).AddRow(releaseRow("r1", "orders_1-0-0", 1)...))

			release, exist, err := ra.GetReleaseByNetworkID(ctx, "orders_1-0-0")

			So(err, ShouldBeNil)
			So(exist, ShouldBeTrue)
			So(release.ID, ShouldEqual, "r1")
		})

		Convey("GetReleaseByNetworkID reports a network no release published\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_release").WillReturnRows(sqlmock.NewRows(releaseColumns))

			release, exist, err := ra.GetReleaseByNetworkID(ctx, "orders")

			So(err, ShouldBeNil)
			So(exist, ShouldBeFalse)
			So(release, ShouldBeNil)
		})

		Convey("GetReleaseByNetworkID returns the query error\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_release").WillReturnError(errors.New("db down"))

			_, exist, err := ra.GetReleaseByNetworkID(ctx, "orders")

			So(err, ShouldNotBeNil)
			So(exist, ShouldBeFalse)
		})

		Convey("CreateRelease inserts every column\n", func() {
			release := &interfaces.Release{
				ID: "r1", LineageID: "orders", NetworkID: "orders_v1_0_0", Version: "1.0.0", BaseVersion: "", ProjectID: "p1",
				Publisher: interfaces.AccountInfo{ID: "u1", Type: "user"}, Notes: "first", State: interfaces.RELEASE_STATE_LIVE,
				CreateTime: 7, UpdateTime: 7,
			}
			smock.ExpectExec(regexp.QuoteMeta(
				"INSERT INTO t_bkn_project_release (f_id,f_lineage_id,f_network_id,f_version,f_base_version,f_project_id,"+
					"f_publisher,f_publisher_type,f_notes,f_state,f_create_time,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)")).
				WithArgs("r1", "orders", "orders_v1_0_0", "1.0.0", "", "p1", "u1", "user", "first", "live", int64(7), int64(7)).
				WillReturnResult(sqlmock.NewResult(1, 1))

			err := ra.CreateRelease(ctx, release)

			So(err, ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("CreateRelease returns the insert error\n", func() {
			smock.ExpectExec("INSERT INTO t_bkn_project_release").WillReturnError(errors.New("duplicate entry"))

			err := ra.CreateRelease(ctx, &interfaces.Release{ID: "r1"})

			So(err, ShouldNotBeNil)
		})

		Convey("GetLatestSwitch reads the newest switch of the lineage\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta(
				"SELECT f_id, f_lineage_id, f_from_network_id, f_to_network_id, f_operator, f_operator_type, f_schedules, f_create_time " +
					"FROM t_bkn_project_switch WHERE f_lineage_id = ? ORDER BY f_create_time DESC, f_id DESC LIMIT 1")).
				WithArgs("orders").
				WillReturnRows(sqlmock.NewRows(switchColumns).AddRow("s2", "orders", "orders", "orders_v1_0_0", "u1", "user",
					`[{"from_id":"a","to_id":"b","name":"nightly"}]`, int64(9)))

			sw, exist, err := ra.GetLatestSwitch(ctx, "orders")

			So(err, ShouldBeNil)
			So(exist, ShouldBeTrue)
			So(sw, ShouldResemble, &interfaces.Switch{
				ID: "s2", LineageID: "orders", FromNetworkID: "orders", ToNetworkID: "orders_v1_0_0",
				Operator:   interfaces.AccountInfo{ID: "u1", Type: "user"},
				Schedules:  []interfaces.SwitchedSchedule{{FromID: "a", ToID: "b", Name: "nightly"}},
				CreateTime: 9,
			})
		})

		Convey("GetLatestSwitch reports a lineage never switched\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_switch").WillReturnRows(sqlmock.NewRows(switchColumns))

			sw, exist, err := ra.GetLatestSwitch(ctx, "orders")

			So(err, ShouldBeNil)
			So(exist, ShouldBeFalse)
			So(sw, ShouldBeNil)
		})

		Convey("RecordSwitch inserts the switch and moves the live state in one transaction\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(regexp.QuoteMeta(
				"INSERT INTO t_bkn_project_switch (f_id,f_lineage_id,f_from_network_id,f_to_network_id,f_operator,f_operator_type,f_schedules,f_create_time) VALUES (?,?,?,?,?,?,?,?)")).
				WithArgs("s1", "orders", "orders_v1_0_0", "orders_v1_1_0", "u1", "user", "[]", int64(5)).
				WillReturnResult(sqlmock.NewResult(1, 1))
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_release SET f_state = ?, f_update_time = ? WHERE f_network_id = ?")).
				WithArgs("archived", int64(5), "orders_v1_0_0").
				WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_release SET f_state = ?, f_update_time = ? WHERE f_network_id = ?")).
				WithArgs("live", int64(5), "orders_v1_1_0").
				WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			err := ra.RecordSwitch(ctx, &interfaces.Switch{
				ID: "s1", LineageID: "orders", FromNetworkID: "orders_v1_0_0", ToNetworkID: "orders_v1_1_0",
				Operator: interfaces.AccountInfo{ID: "u1", Type: "user"}, CreateTime: 5,
			})

			So(err, ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("RecordSwitch rolls back when a state update fails\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec("INSERT INTO t_bkn_project_switch").WillReturnResult(sqlmock.NewResult(1, 1))
			smock.ExpectExec("UPDATE t_bkn_project_release").WillReturnError(errors.New("lock wait timeout"))
			smock.ExpectRollback()

			err := ra.RecordSwitch(ctx, &interfaces.Switch{ID: "s1", LineageID: "orders", FromNetworkID: "a", ToNetworkID: "b"})

			So(err, ShouldNotBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})
	})
}
