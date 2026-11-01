// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package project

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

const selectProjects = "SELECT f_id, f_name, f_kn_id, f_branch, f_baseline_network_id, f_catalog_ids, " +
	"f_naming_language, f_archived, f_owner, f_owner_type, f_create_time, f_update_time FROM t_bkn_project_project "

// projectRow is a project made before naming languages, whose language is empty.
func projectRow(id, knID string, createTime int64) []driver.Value {
	return []driver.Value{id, "Orders", knID, "project/" + id, knID, `["c1"]`, "", false, "u1", "user", createTime, createTime}
}

func Test_ProjectAccess(t *testing.T) {
	Convey("Test projectAccess\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		pa := &projectAccess{db: db}
		ctx := context.Background()
		unarchived := false

		Convey("CreateProject inserts every column, catalog ids as JSON\n", func() {
			smock.ExpectExec(regexp.QuoteMeta(
				"INSERT INTO t_bkn_project_project (f_id,f_name,f_kn_id,f_branch,f_baseline_network_id,f_catalog_ids,"+
					"f_naming_language,f_archived,f_owner,f_owner_type,f_create_time,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)")).
				WithArgs("p1", "Orders", "orders", "project/p1", "orders_v1_0_0", `["c1","c2"]`, "en-US", false, "u1", "user", int64(7), int64(7)).
				WillReturnResult(sqlmock.NewResult(1, 1))

			err := pa.CreateProject(ctx, &interfaces.Project{
				ID: "p1", Name: "Orders", KNID: "orders", Branch: "project/p1", BaselineNetworkID: "orders_v1_0_0",
				CatalogIDs: []string{"c1", "c2"}, NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US,
				Owner: interfaces.AccountInfo{ID: "u1", Type: "user"}, CreateTime: 7, UpdateTime: 7,
			})

			So(err, ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("CreateProject stores no catalog ids as an empty array\n", func() {
			smock.ExpectExec("INSERT INTO t_bkn_project_project").
				WithArgs("p1", "", "", "", "", "[]", "", false, "", "", int64(0), int64(0)).
				WillReturnResult(sqlmock.NewResult(1, 1))

			So(pa.CreateProject(ctx, &interfaces.Project{ID: "p1"}), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("CreateProject returns the insert error\n", func() {
			smock.ExpectExec("INSERT INTO t_bkn_project_project").WillReturnError(errors.New("duplicate entry"))

			So(pa.CreateProject(ctx, &interfaces.Project{ID: "p1"}), ShouldNotBeNil)
		})

		Convey("GetProject reads the row; a project made before naming languages names in zh-CN\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta(selectProjects + "WHERE f_id = ?")).
				WithArgs("p1").
				WillReturnRows(sqlmock.NewRows(projectColumns).AddRow(projectRow("p1", "orders", 3)...))

			project, exist, err := pa.GetProject(ctx, "p1")

			So(err, ShouldBeNil)
			So(exist, ShouldBeTrue)
			So(project, ShouldResemble, &interfaces.Project{
				ID: "p1", Name: "Orders", KNID: "orders", Branch: "project/p1", BaselineNetworkID: "orders",
				CatalogIDs: []string{"c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_ZH_CN,
				Owner: interfaces.AccountInfo{ID: "u1", Type: "user"}, CreateTime: 3, UpdateTime: 3,
			})
		})

		Convey("GetProject reads the naming language a project has\n", func() {
			row := projectRow("p1", "orders", 3)
			row[6] = "en-US"
			smock.ExpectQuery("FROM t_bkn_project_project").WillReturnRows(sqlmock.NewRows(projectColumns).AddRow(row...))

			project, _, err := pa.GetProject(ctx, "p1")

			So(err, ShouldBeNil)
			So(project.NamingLanguage, ShouldEqual, interfaces.NAMING_LANGUAGE_EN_US)
		})

		Convey("SetProjectNamingLanguage stores the language and the update time\n", func() {
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_project SET f_naming_language = ?, f_update_time = ? WHERE f_id = ?")).
				WithArgs("en-US", int64(9), "p1").
				WillReturnResult(sqlmock.NewResult(0, 1))

			So(pa.SetProjectNamingLanguage(ctx, "p1", interfaces.NAMING_LANGUAGE_EN_US, 9), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("GetProject reports a missing project\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_project").WillReturnRows(sqlmock.NewRows(projectColumns))

			project, exist, err := pa.GetProject(ctx, "none")

			So(err, ShouldBeNil)
			So(exist, ShouldBeFalse)
			So(project, ShouldBeNil)
		})

		Convey("GetProject returns a query error\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_project").WillReturnError(errors.New("db down"))

			_, exist, err := pa.GetProject(ctx, "p1")

			So(err, ShouldNotBeNil)
			So(exist, ShouldBeFalse)
		})

		Convey("ListProjects filters by network, archive state and a literal name, after the cursor\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta(selectProjects+
				"WHERE f_kn_id IN (?,?) AND f_archived = ? AND f_name LIKE ? ESCAPE '\\\\' "+
				"AND (f_create_time < ? OR (f_create_time = ? AND f_id < ?)) "+
				"ORDER BY f_create_time DESC, f_id DESC LIMIT 3")).
				WithArgs("orders", "parts", false, `%50\%\_off%`, int64(5), int64(5), "p9").
				WillReturnRows(sqlmock.NewRows(projectColumns).
					AddRow(projectRow("p2", "orders", 4)...).
					AddRow(projectRow("p1", "parts", 3)...))

			projects, err := pa.ListProjects(ctx, interfaces.ProjectsQuery{
				KNIDs: []string{"orders", "parts"}, Archived: &unarchived, Name: "50%_off",
				Cursor: &interfaces.ProjectCursor{CreateTime: 5, ID: "p9"}, Limit: 3,
			})

			So(err, ShouldBeNil)
			So(len(projects), ShouldEqual, 2)
			So(projects[0].ID, ShouldEqual, "p2")
			So(projects[1].KNID, ShouldEqual, "parts")
		})

		Convey("ListProjects selects nothing for no visible network, without a query\n", func() {
			projects, err := pa.ListProjects(ctx, interfaces.ProjectsQuery{Limit: 3})

			So(err, ShouldBeNil)
			So(projects, ShouldNotBeNil)
			So(projects, ShouldBeEmpty)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("ListProjects returns a row it cannot read as an error\n", func() {
			row := projectRow("p1", "orders", 3)
			row[5] = "not json"
			smock.ExpectQuery("FROM t_bkn_project_project").WillReturnRows(sqlmock.NewRows(projectColumns).AddRow(row...))

			_, err := pa.ListProjects(ctx, interfaces.ProjectsQuery{KNIDs: []string{"orders"}, Limit: 3})

			So(err, ShouldNotBeNil)
		})

		Convey("CountProjects counts with the same filters and no cursor\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(f_id) FROM t_bkn_project_project WHERE f_kn_id IN (?)")).
				WithArgs("orders").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(4)))

			total, err := pa.CountProjects(ctx, interfaces.ProjectsQuery{
				KNIDs: []string{"orders"}, Cursor: &interfaces.ProjectCursor{CreateTime: 5, ID: "p9"}, Limit: 3,
			})

			So(err, ShouldBeNil)
			So(total, ShouldEqual, 4)
		})

		Convey("CountProjects is zero for no visible network, without a query\n", func() {
			total, err := pa.CountProjects(ctx, interfaces.ProjectsQuery{})

			So(err, ShouldBeNil)
			So(total, ShouldEqual, 0)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("CountUnarchivedProjects groups unarchived projects by network\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta(
				"SELECT f_kn_id, COUNT(f_id) FROM t_bkn_project_project WHERE f_archived = ? AND f_kn_id IN (?,?) GROUP BY f_kn_id")).
				WithArgs(false, "orders", "parts").
				WillReturnRows(sqlmock.NewRows([]string{"f_kn_id", "count"}).AddRow("orders", int64(2)))

			counts, err := pa.CountUnarchivedProjects(ctx, []string{"orders", "parts"})

			So(err, ShouldBeNil)
			So(counts, ShouldResemble, map[string]int64{"orders": 2})
		})

		Convey("CountUnarchivedProjects of no network is empty, without a query\n", func() {
			counts, err := pa.CountUnarchivedProjects(ctx, nil)

			So(err, ShouldBeNil)
			So(counts, ShouldBeEmpty)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("SetProjectArchived updates the flag and the update time\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_project SET f_archived = ?, f_update_time = ? WHERE f_id = ?")).
				WithArgs(true, int64(9), "p1").
				WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectExec("UPDATE t_bkn_project_kb_selection SET f_state=CASE WHEN f_retained=1 THEN 'retained' ELSE 'release_pending' END").WithArgs(int64(9), "p1").WillReturnResult(sqlmock.NewResult(0, 2))
			smock.ExpectCommit()

			So(pa.SetProjectArchived(ctx, "p1", true, 9), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("SetProjectCatalogs stores the catalog ids as JSON and the update time\n", func() {
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_project SET f_catalog_ids = ?, f_update_time = ? WHERE f_id = ?")).
				WithArgs(`["c2","c3"]`, int64(9), "p1").
				WillReturnResult(sqlmock.NewResult(0, 1))

			So(pa.SetProjectCatalogs(ctx, "p1", []string{"c2", "c3"}, 9), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("SetProjectCatalogs stores no catalog ids as an empty array and returns the update error\n", func() {
			smock.ExpectExec("UPDATE t_bkn_project_project").WithArgs("[]", int64(9), "p1").
				WillReturnError(errors.New("lock wait timeout"))

			err := pa.SetProjectCatalogs(ctx, "p1", nil, 9)
			// A statement with other arguments would fail on the mismatch instead.
			So(err, ShouldBeError, "lock wait timeout")
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("SetProjectArchived returns the update error\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec("UPDATE t_bkn_project_project").WillReturnError(errors.New("lock wait timeout"))
			smock.ExpectRollback()

			So(pa.SetProjectArchived(ctx, "p1", true, 9), ShouldNotBeNil)
		})

		Convey("Archiving rolls back if retention cleanup cannot be queued\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec("UPDATE t_bkn_project_project").WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectExec("UPDATE t_bkn_project_kb_selection").WillReturnError(errors.New("write failed"))
			smock.ExpectRollback()
			So(pa.SetProjectArchived(ctx, "p1", true, 9), ShouldBeError, "write failed")
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("Unarchiving does not revive released source holds\n", func() {
			smock.ExpectExec("UPDATE t_bkn_project_project").WithArgs(false, int64(9), "p1").WillReturnResult(sqlmock.NewResult(0, 1))
			So(pa.SetProjectArchived(ctx, "p1", false, 9), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})
	})
}
