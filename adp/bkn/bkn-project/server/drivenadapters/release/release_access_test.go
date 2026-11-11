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

func releaseRow(id, state string, knVersion int, createTime int64) []driver.Value {
	return []driver.Value{id, "orders", "orders", "1.0.0", "0.9.0", "p1", "u1", "user", "first", state, knVersion,
		"c1", int64(4), `{"release.yaml":"YQ=="}`, "", createTime, createTime}
}

func Test_ReleaseAccess(t *testing.T) {
	Convey("Test releaseAccess\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		ra := &releaseAccess{db: db}
		ctx := context.Background()
		selectAll := "SELECT f_id, f_lineage_id, f_network_id, f_version, f_base_version, f_project_id, f_publisher, " +
			"f_publisher_type, f_notes, f_state, f_kn_version, f_branch_commit, f_draft_revision, f_capabilities, " +
			"f_publish_request, f_create_time, f_update_time FROM t_bkn_project_release"

		Convey("ListReleases reads the first page newest first\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta(selectAll + " WHERE f_lineage_id = ? ORDER BY f_create_time DESC, f_id DESC LIMIT 3")).
				WithArgs("orders").
				WillReturnRows(sqlmock.NewRows(releaseColumns).AddRow(releaseRow("r2", "published", 3, 2)...))

			releases, err := ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: "orders", Limit: 3})

			So(err, ShouldBeNil)
			So(releases, ShouldHaveLength, 1)
			So(releases[0].NetworkID, ShouldEqual, "orders")
			So(releases[0].Publisher, ShouldResemble, interfaces.AccountInfo{ID: "u1", Type: "user"})
			So(releases[0].State, ShouldEqual, "published")
			So(releases[0].KNVersion, ShouldEqual, 3)
			So(releases[0].BranchCommit, ShouldEqual, "c1")
			So(releases[0].DraftRevision, ShouldEqual, 4)
			So(releases[0].Capabilities, ShouldResemble, map[string][]byte{"release.yaml": []byte("a")})
			So(releases[0].PublishRequest, ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("ListReleases selects one state\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("WHERE f_lineage_id = ? AND f_state = ? ORDER BY")).
				WithArgs("orders", "published").
				WillReturnRows(sqlmock.NewRows(releaseColumns))

			releases, err := ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: "orders", State: "published", Limit: 3})

			So(err, ShouldBeNil)
			So(releases, ShouldBeEmpty)
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

		Convey("ListReleases returns the query error and a broken capabilities column\n", func() {
			smock.ExpectQuery("SELECT .* FROM t_bkn_project_release").WillReturnError(errors.New("db down"))

			_, err := ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: "orders", Limit: 1})
			So(err, ShouldNotBeNil)

			row := releaseRow("r1", "published", 1, 1)
			row[13] = "{not json"
			smock.ExpectQuery("SELECT .* FROM t_bkn_project_release").WillReturnRows(sqlmock.NewRows(releaseColumns).AddRow(row...))

			_, err = ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: "orders", Limit: 1})
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

		Convey("GetRelease finds a lineage's version\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta(selectAll+" WHERE f_lineage_id = ? AND f_version = ?")).
				WithArgs("orders", "1.0.0").
				WillReturnRows(sqlmock.NewRows(releaseColumns).AddRow(releaseRow("r1", "prepared", 0, 1)...))

			release, exist, err := ra.GetRelease(ctx, "orders", "1.0.0")

			So(err, ShouldBeNil)
			So(exist, ShouldBeTrue)
			So(release.ID, ShouldEqual, "r1")
			So(release.State, ShouldEqual, "prepared")
			So(release.PublishRequest, ShouldBeNil)
		})

		Convey("GetRelease reads the saved publish request, and fails on one that does not read\n", func() {
			row := releaseRow("r1", "prepared", 0, 1)
			row[14] = `{"expected_running_version":2,"expected_draft_revision":5,"notes":"n","source":{"ref":"c1"},` +
				`"fail_on_skipped_sources":true,"idempotency_key":"k1"}`
			smock.ExpectQuery("FROM t_bkn_project_release").WillReturnRows(sqlmock.NewRows(releaseColumns).AddRow(row...))

			release, _, err := ra.GetRelease(ctx, "orders", "1.0.0")

			So(err, ShouldBeNil)
			So(release.PublishRequest, ShouldResemble, &interfaces.KNVersionPublish{
				ExpectedRunningVersion: 2, ExpectedDraftRevision: 5, Notes: "n",
				Source: interfaces.KNVersionSource{Ref: "c1"}, FailOnSkippedSources: true, IdempotencyKey: "k1",
			})

			row[14] = "{not json"
			smock.ExpectQuery("FROM t_bkn_project_release").WillReturnRows(sqlmock.NewRows(releaseColumns).AddRow(row...))

			_, _, err = ra.GetRelease(ctx, "orders", "1.0.0")

			So(err, ShouldNotBeNil)
		})

		Convey("GetRelease reports a version never prepared, and passes a failure on\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_release").WillReturnRows(sqlmock.NewRows(releaseColumns))

			release, exist, err := ra.GetRelease(ctx, "orders", "2.0.0")
			So(err, ShouldBeNil)
			So(exist, ShouldBeFalse)
			So(release, ShouldBeNil)

			smock.ExpectQuery("FROM t_bkn_project_release").WillReturnError(errors.New("db down"))

			_, exist, err = ra.GetRelease(ctx, "orders", "2.0.0")
			So(err, ShouldNotBeNil)
			So(exist, ShouldBeFalse)
		})

		prepared := func() *interfaces.Release {
			return &interfaces.Release{
				ID: "r9", LineageID: "orders", NetworkID: "orders", Version: "1.0.0", BaseVersion: "0.9.0", ProjectID: "p1",
				Publisher: interfaces.AccountInfo{ID: "u1", Type: "user"}, Notes: "first", BranchCommit: "c2",
				DraftRevision: 6, Capabilities: map[string][]byte{"release.yaml": []byte("a")}, CreateTime: 7, UpdateTime: 8,
			}
		}

		Convey("PrepareRelease inserts a release never prepared as prepared\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_release").WithArgs("orders", "1.0.0").WillReturnRows(sqlmock.NewRows(releaseColumns))
			smock.ExpectExec(regexp.QuoteMeta(
				"INSERT INTO t_bkn_project_release (f_id,f_lineage_id,f_network_id,f_version,f_base_version,f_project_id,"+
					"f_publisher,f_publisher_type,f_notes,f_state,f_kn_version,f_branch_commit,f_draft_revision,f_capabilities,"+
					"f_publish_request,f_create_time,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")).
				WithArgs("r9", "orders", "orders", "1.0.0", "0.9.0", "p1", "u1", "user", "first", "prepared", 0,
					"c2", int64(6), `{"release.yaml":"YQ=="}`, "", int64(7), int64(8)).
				WillReturnResult(sqlmock.NewResult(1, 1))

			release := prepared()
			ok, err := ra.PrepareRelease(ctx, release)

			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
			So(release.State, ShouldEqual, "prepared")
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("PrepareRelease replaces a still prepared release and keeps its id\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_release").
				WillReturnRows(sqlmock.NewRows(releaseColumns).AddRow(releaseRow("r1", "prepared", 0, 3)...))
			smock.ExpectExec(regexp.QuoteMeta(
				"UPDATE t_bkn_project_release SET f_base_version = ?, f_branch_commit = ?, f_capabilities = ?, "+
					"f_draft_revision = ?, f_network_id = ?, f_notes = ?, f_project_id = ?, f_publish_request = ?, f_publisher = ?, "+
					"f_publisher_type = ?, f_update_time = ? WHERE f_id = ? AND f_state = ?")).
				WithArgs("0.9.0", "c2", `{"release.yaml":"YQ=="}`, int64(6), "orders", "first", "p1", "", "u1", "user",
					int64(8), "r1", "prepared").
				WillReturnResult(sqlmock.NewResult(0, 1))

			release := prepared()
			ok, err := ra.PrepareRelease(ctx, release)

			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
			So(release.ID, ShouldEqual, "r1")
			So(release.CreateTime, ShouldEqual, 3)
			So(release.State, ShouldEqual, "prepared")
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("PrepareRelease reports a release published meanwhile as not prepared\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_release").
				WillReturnRows(sqlmock.NewRows(releaseColumns).AddRow(releaseRow("r1", "prepared", 0, 3)...))
			smock.ExpectExec("UPDATE t_bkn_project_release").WillReturnResult(sqlmock.NewResult(0, 0))

			ok, err := ra.PrepareRelease(ctx, prepared())

			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
		})

		Convey("PrepareRelease leaves a published release alone\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_release").
				WillReturnRows(sqlmock.NewRows(releaseColumns).AddRow(releaseRow("r1", "published", 2, 3)...))

			ok, err := ra.PrepareRelease(ctx, prepared())

			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("PrepareRelease passes read and write failures on\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_release").WillReturnError(errors.New("db down"))

			_, err := ra.PrepareRelease(ctx, prepared())
			So(err, ShouldNotBeNil)

			smock.ExpectQuery("FROM t_bkn_project_release").WillReturnRows(sqlmock.NewRows(releaseColumns))
			smock.ExpectExec("INSERT INTO t_bkn_project_release").WillReturnError(errors.New("duplicate entry"))

			_, err = ra.PrepareRelease(ctx, prepared())
			So(err, ShouldNotBeNil)
		})

		Convey("MarkPublished records the network version and notes\n", func() {
			smock.ExpectExec(regexp.QuoteMeta(
				"UPDATE t_bkn_project_release SET f_kn_version = ?, f_notes = ?, f_state = ?, f_update_time = ? WHERE f_id = ? AND f_state = ?")).
				WithArgs(3, "shipped", "published", int64(9), "r1", "prepared").
				WillReturnResult(sqlmock.NewResult(0, 1))

			err := ra.MarkPublished(ctx, "r1", 3, "shipped", 9)

			So(err, ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)

			smock.ExpectExec("UPDATE t_bkn_project_release").WillReturnError(errors.New("db down"))

			So(ra.MarkPublished(ctx, "r1", 3, "shipped", 9), ShouldNotBeNil)
		})

		savePublish := regexp.QuoteMeta("UPDATE t_bkn_project_release SET f_publish_request = ? WHERE f_id = ? AND f_state = ?")

		Convey("SavePublishRequest keeps the request, idempotency key included, on the prepared release\n", func() {
			smock.ExpectExec(savePublish).
				WithArgs(`{"expected_running_version":2,"expected_draft_revision":5,"notes":"n","source":{"ref":"c1"},`+
					`"fail_on_skipped_sources":true,"idempotency_key":"k1"}`, "r1", "prepared").
				WillReturnResult(sqlmock.NewResult(0, 1))

			err := ra.SavePublishRequest(ctx, "r1", &interfaces.KNVersionPublish{
				ExpectedRunningVersion: 2, ExpectedDraftRevision: 5, Notes: "n",
				Source: interfaces.KNVersionSource{Ref: "c1"}, FailOnSkippedSources: true, IdempotencyKey: "k1",
			})

			So(err, ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("SavePublishRequest of none forgets the request\n", func() {
			smock.ExpectExec(savePublish).WithArgs("", "r1", "prepared").WillReturnResult(sqlmock.NewResult(0, 1))

			So(ra.SavePublishRequest(ctx, "r1", nil), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("SavePublishRequest passes a write failure on\n", func() {
			smock.ExpectExec(savePublish).WillReturnError(errors.New("db down"))

			So(ra.SavePublishRequest(ctx, "r1", nil), ShouldNotBeNil)
		})
	})
}
