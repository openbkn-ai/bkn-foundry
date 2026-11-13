// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_SupplementAndConversations(t *testing.T) {
	Convey("Test the supplement note and Ask AI conversations\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		ra := &requirementAccess{db: db}
		ctx := context.Background()
		convColumns := []string{"f_id", "f_project_id", "f_page", "f_title", "f_turns", "f_creator", "f_creator_type",
			"f_create_time", "f_update_time"}

		Convey("GetSupplement reads the note, and an empty one without a row or a note\n", func() {
			query := regexp.QuoteMeta("SELECT f_supplement FROM t_bkn_project_requirement WHERE f_project_id = ?")
			smock.ExpectQuery(query).WithArgs("p1").WillReturnRows(sqlmock.NewRows([]string{"f_supplement"}).
				AddRow(`{"text":"Planning only.","by":{"id":"u1","type":"user"},"time":5}`))
			smock.ExpectQuery(query).WithArgs("p2").WillReturnError(sql.ErrNoRows)
			smock.ExpectQuery(query).WithArgs("p3").WillReturnRows(sqlmock.NewRows([]string{"f_supplement"}).AddRow(nil))

			got, err := ra.GetSupplement(ctx, "p1")
			So(err, ShouldBeNil)
			So(got, ShouldResemble, &interfaces.RequirementSupplement{Text: "Planning only.",
				By: &interfaces.RequirementAuthor{ID: "u1", Type: "user"}, Time: 5})
			got, err = ra.GetSupplement(ctx, "p2")
			So(err, ShouldBeNil)
			So(got.Text, ShouldEqual, "")
			got, err = ra.GetSupplement(ctx, "p3")
			So(err, ShouldBeNil)
			So(got.Text, ShouldEqual, "")
		})

		Convey("SetSupplement writes the note alone, making sure of the row\n", func() {
			smock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_bkn_project_requirement (f_project_id,f_spec,f_inputs,f_supplement) VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE f_supplement = VALUES(f_supplement)")).
				WithArgs("p1", "", "", `{"text":"x","time":5}`).WillReturnResult(sqlmock.NewResult(1, 1))

			So(ra.SetSupplement(ctx, "p1", &interfaces.RequirementSupplement{Text: "x", Time: 5}), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("A row of the note alone reads as no specification\n", func() {
			smock.ExpectQuery("SELECT (.+) FROM t_bkn_project_requirement WHERE f_project_id = ?").WithArgs("p1").
				WillReturnRows(sqlmock.NewRows(reqColumns).AddRow("p1", 0, "", "", "", "", "", "", "", "", 0, 0))

			_, ok, err := ra.GetRequirement(ctx, "p1")
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
		})

		Convey("Conversations are listed by page, last updated first, with their turns\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_project_id, f_page, f_title, f_turns, f_creator, f_creator_type, f_create_time, f_update_time FROM t_bkn_project_requirement_conversation WHERE f_page = ? AND f_project_id = ? ORDER BY f_update_time DESC, f_id DESC")).
				WithArgs("kinetic", "p1").WillReturnRows(sqlmock.NewRows(convColumns).
				AddRow("c1", "p1", "kinetic", "Why?", `[{"id":"t1","question":"Why?","status":"ready"}]`, "u1", "user", 1, 2))

			list, err := ra.ListConversations(ctx, "p1", "kinetic")
			So(err, ShouldBeNil)
			So(len(list), ShouldEqual, 1)
			So(list[0].Turns[0].Question, ShouldEqual, "Why?")
			So(list[0].Creator, ShouldResemble, interfaces.AccountInfo{ID: "u1", Type: "user"})
		})

		Convey("A conversation is created, written over the one read, and deleted\n", func() {
			c := &interfaces.RequirementConversation{ID: "c1", ProjectID: "p1", Page: "scenarios", Title: "t",
				Turns: []*interfaces.RequirementConversationTurn{}, Creator: interfaces.AccountInfo{ID: "u1", Type: "user"},
				CreateTime: 1, UpdateTime: 1}
			smock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_bkn_project_requirement_conversation (f_id,f_project_id,f_page,f_title,f_turns,f_creator,f_creator_type,f_create_time,f_update_time) VALUES (?,?,?,?,?,?,?,?,?)")).
				WithArgs("c1", "p1", "scenarios", "t", "[]", "u1", "user", int64(1), int64(1)).WillReturnResult(sqlmock.NewResult(1, 1))
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_requirement_conversation SET f_title = ?, f_turns = ?, f_update_time = ? WHERE f_id = ? AND f_project_id = ? AND f_update_time = ?")).
				WithArgs("t", "[]", int64(2), "c1", "p1", int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_bkn_project_requirement_conversation WHERE f_id = ? AND f_project_id = ?")).
				WithArgs("c1", "p1").WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectQuery("SELECT (.+) FROM t_bkn_project_requirement_conversation").WithArgs("c1", "p1").
				WillReturnError(sql.ErrNoRows)

			So(ra.CreateConversation(ctx, c), ShouldBeNil)
			c.UpdateTime = 2
			written, err := ra.UpdateConversation(ctx, c, 1)
			So(err, ShouldBeNil)
			So(written, ShouldBeFalse)
			deleted, err := ra.DeleteConversation(ctx, "p1", "c1")
			So(err, ShouldBeNil)
			So(deleted, ShouldBeTrue)
			_, ok, err := ra.GetConversation(ctx, "p1", "c1")
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})
	})
}
