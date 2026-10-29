// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package indexbuild

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_IndexBuildAccess(t *testing.T) {
	Convey("Test indexBuildAccess\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		iba := &indexBuildAccess{db: db}
		ctx := context.Background()
		insert := "INSERT INTO t_bkn_project_index_build (f_project_id,f_commit,f_resource_id,f_object_type_id,f_task_id," +
			"f_outcome,f_reason,f_runner,f_runner_type,f_create_time) VALUES "
		// A retry of the same commit and table replaces what the build before recorded.
		upsert := " ON DUPLICATE KEY UPDATE f_object_type_id = VALUES(f_object_type_id), f_task_id = VALUES(f_task_id), " +
			"f_outcome = VALUES(f_outcome), f_reason = VALUES(f_reason), f_runner = VALUES(f_runner), " +
			"f_runner_type = VALUES(f_runner_type), f_create_time = VALUES(f_create_time)"
		placeholders := func(rows int) string {
			return strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?,?,?,?),", rows), ",")
		}
		build := func(resource string) *interfaces.IndexBuild {
			return &interfaces.IndexBuild{ProjectID: "p1", Commit: "c1", ResourceID: resource, ObjectTypeID: "orders",
				TaskID: "t1", Outcome: interfaces.INDEX_BUILD_CREATED, Runner: interfaces.AccountInfo{ID: "u1", Type: "user"},
				CreateTime: 7}
		}

		Convey("RecordBuilds writes in batches, replacing a retried build\\n", func() {
			builds := make([]*interfaces.IndexBuild, batchSize+1)
			for i := range builds {
				builds[i] = build(fmt.Sprintf("r%d", i))
			}
			builds[batchSize].Outcome, builds[batchSize].TaskID, builds[batchSize].Reason = interfaces.INDEX_BUILD_NO_PERMISSION, "", "forbidden"
			smock.ExpectExec(regexp.QuoteMeta(insert + placeholders(batchSize) + upsert)).WillReturnResult(sqlmock.NewResult(0, batchSize))
			smock.ExpectExec(regexp.QuoteMeta(insert+placeholders(1)+upsert)).
				WithArgs("p1", "c1", fmt.Sprintf("r%d", batchSize), "orders", "", interfaces.INDEX_BUILD_NO_PERMISSION,
					"forbidden", "u1", "user", int64(7)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			So(iba.RecordBuilds(ctx, builds), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("RecordBuilds does nothing for nothing\\n", func() {
			So(iba.RecordBuilds(ctx, nil), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("ListBuilds reads a confirmation's builds by resource id\\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta(
				"SELECT f_project_id, f_commit, f_resource_id, f_object_type_id, f_task_id, f_outcome, f_reason, f_runner, "+
					"f_runner_type, f_create_time FROM t_bkn_project_index_build WHERE f_commit = ? AND f_project_id = ? ORDER BY f_resource_id")).
				WithArgs("c1", "p1").
				WillReturnRows(sqlmock.NewRows(buildColumns).
					AddRow("p1", "c1", "r1", "orders", "t1", interfaces.INDEX_BUILD_CREATED, "", "u1", "user", 7))
			builds, err := iba.ListBuilds(ctx, "p1", "c1")
			So(err, ShouldBeNil)
			So(builds, ShouldResemble, []*interfaces.IndexBuild{build("r1")})
		})
	})
}
