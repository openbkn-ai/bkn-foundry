// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package capability_binding

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

func mockCapabilityBindingAccess(t *testing.T) (*capabilityBindingAccess, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	ca := &capabilityBindingAccess{appSetting: &common.AppSetting{}, db: db}
	return ca, mock, func() { _ = db.Close() }
}

func bindingRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"f_id", "f_kn_id", "f_branch", "f_capability_type", "f_owner_id", "f_capability_id",
		"f_bound_as_box", "f_comment", "f_creator", "f_creator_type", "f_create_time",
		"f_updater", "f_updater_type", "f_update_time",
	})
}

func TestCapabilityBindingAccess_CreateBindings(t *testing.T) {
	Convey("CreateBindings writes one row per capability", t, func() {
		ca, mock, cleanup := mockCapabilityBindingAccess(t)
		defer cleanup()

		mock.ExpectExec("INSERT INTO t_kn_capability_binding").
			WithArgs(
				"bind-1", "kn1", "main", interfaces.CAPABILITY_TYPE_SKILL, "", "skill-1", 0, "交期评估",
				"user-1", "user", int64(1), "user-1", "user", int64(1),
				"bind-2", "kn1", "main", interfaces.CAPABILITY_TYPE_FUNCTION, "box-1", "tool-1", 1, "",
				"user-1", "user", int64(1), "user-1", "user", int64(1),
			).
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := ca.CreateBindings(context.Background(), nil, []*interfaces.CapabilityBinding{
			{
				ID: "bind-1", KNID: "kn1", Branch: "main", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL,
				CapabilityID: "skill-1", Comment: "交期评估",
				Creator:    interfaces.AccountInfo{ID: "user-1", Type: "user"},
				Updater:    interfaces.AccountInfo{ID: "user-1", Type: "user"},
				CreateTime: 1, UpdateTime: 1,
			},
			{
				ID: "bind-2", KNID: "kn1", Branch: "main", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
				OwnerID: "box-1", CapabilityID: "tool-1", BoundAsBox: true,
				Creator:    interfaces.AccountInfo{ID: "user-1", Type: "user"},
				Updater:    interfaces.AccountInfo{ID: "user-1", Type: "user"},
				CreateTime: 1, UpdateTime: 1,
			},
		})

		So(err, ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})

	Convey("CreateBindings with no rows touches no connection", t, func() {
		ca, mock, cleanup := mockCapabilityBindingAccess(t)
		defer cleanup()

		So(ca.CreateBindings(context.Background(), nil, nil), ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})
}

func TestCapabilityBindingAccess_GetBindingByCapability(t *testing.T) {
	Convey("GetBindingByCapability resolves the unique-key tuple", t, func() {
		ca, mock, cleanup := mockCapabilityBindingAccess(t)
		defer cleanup()

		mock.ExpectQuery("SELECT (.+) FROM t_kn_capability_binding").
			WithArgs("kn1", "main", interfaces.CAPABILITY_TYPE_FUNCTION, "box-1", "tool-1").
			WillReturnRows(bindingRows().AddRow(
				"bind-2", "kn1", "main", interfaces.CAPABILITY_TYPE_FUNCTION, "box-1", "tool-1",
				1, "", "user-1", "user", int64(1), "user-1", "user", int64(1),
			))

		binding, err := ca.GetBindingByCapability(context.Background(), "kn1", "main",
			interfaces.CAPABILITY_TYPE_FUNCTION, "box-1", "tool-1")

		So(err, ShouldBeNil)
		So(binding.ID, ShouldEqual, "bind-2")
		So(binding.BoundAsBox, ShouldBeTrue)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})

	Convey("A capability that is not bound reads back as nil, not an error", t, func() {
		ca, mock, cleanup := mockCapabilityBindingAccess(t)
		defer cleanup()

		mock.ExpectQuery("SELECT (.+) FROM t_kn_capability_binding").
			WithArgs("kn1", "main", interfaces.CAPABILITY_TYPE_SKILL, "", "skill-9").
			WillReturnRows(bindingRows())

		binding, err := ca.GetBindingByCapability(context.Background(), "kn1", "main",
			interfaces.CAPABILITY_TYPE_SKILL, "", "skill-9")

		So(err, ShouldBeNil)
		So(binding, ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})
}

func TestCapabilityBindingAccess_ListBindings(t *testing.T) {
	Convey("ListBindings filters by branch and type and applies paging", t, func() {
		ca, mock, cleanup := mockCapabilityBindingAccess(t)
		defer cleanup()

		mock.ExpectQuery("SELECT (.+) FROM t_kn_capability_binding WHERE f_kn_id = \\? AND f_branch = \\? AND f_capability_type = \\? ORDER BY f_create_time desc, f_id ASC LIMIT 10 OFFSET 20").
			WithArgs("kn1", "dev", interfaces.CAPABILITY_TYPE_SKILL).
			WillReturnRows(bindingRows().AddRow(
				"bind-1", "kn1", "dev", interfaces.CAPABILITY_TYPE_SKILL, "", "skill-1",
				0, "", "user-1", "user", int64(1), "user-1", "user", int64(1),
			))

		list, err := ca.ListBindings(context.Background(), interfaces.CapabilityBindingsQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Offset: 20, Limit: 10, Sort: "f_create_time", Direction: interfaces.DESC_DIRECTION,
			},
			KNID: "kn1", Branch: "dev", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL,
		})

		So(err, ShouldBeNil)
		So(len(list), ShouldEqual, 1)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})

	Convey("An empty branch reads the main branch rather than every branch", t, func() {
		ca, mock, cleanup := mockCapabilityBindingAccess(t)
		defer cleanup()

		mock.ExpectQuery("SELECT (.+) FROM t_kn_capability_binding").
			WithArgs("kn1", interfaces.MAIN_BRANCH).
			WillReturnRows(bindingRows())

		list, err := ca.ListBindings(context.Background(), interfaces.CapabilityBindingsQueryParams{KNID: "kn1"})

		So(err, ShouldBeNil)
		So(list, ShouldBeEmpty)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})
}

func TestCapabilityBindingAccess_GetBindingsTotalByType(t *testing.T) {
	Convey("GetBindingsTotalByType counts every type in one query", t, func() {
		ca, mock, cleanup := mockCapabilityBindingAccess(t)
		defer cleanup()

		mock.ExpectQuery("SELECT f_capability_type, COUNT\\(f_id\\) FROM t_kn_capability_binding").
			WithArgs("kn1", "main").
			WillReturnRows(sqlmock.NewRows([]string{"f_capability_type", "count"}).
				AddRow(interfaces.CAPABILITY_TYPE_SKILL, 3).
				AddRow(interfaces.CAPABILITY_TYPE_FUNCTION, 4))

		totals, err := ca.GetBindingsTotalByType(context.Background(), "kn1", "main")

		So(err, ShouldBeNil)
		So(totals[interfaces.CAPABILITY_TYPE_SKILL], ShouldEqual, 3)
		So(totals[interfaces.CAPABILITY_TYPE_FUNCTION], ShouldEqual, 4)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})
}

func TestCapabilityBindingAccess_DeleteBindings(t *testing.T) {
	Convey("DeleteBindingsByIDs reports how many rows actually went away", t, func() {
		ca, mock, cleanup := mockCapabilityBindingAccess(t)
		defer cleanup()

		mock.ExpectExec("DELETE FROM t_kn_capability_binding").
			WithArgs("kn1", "main", "bind-1", "bind-missing").
			WillReturnResult(sqlmock.NewResult(0, 1))

		rows, err := ca.DeleteBindingsByIDs(context.Background(), nil, "kn1", "main",
			[]string{"bind-1", "bind-missing"})

		So(err, ShouldBeNil)
		So(rows, ShouldEqual, int64(1))
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})

	Convey("DeleteBindingsByKnID without a branch clears every branch of the network", t, func() {
		ca, mock, cleanup := mockCapabilityBindingAccess(t)
		defer cleanup()

		mock.ExpectExec("DELETE FROM t_kn_capability_binding WHERE f_kn_id = \\?$").
			WithArgs("kn1").
			WillReturnResult(sqlmock.NewResult(0, 5))

		rows, err := ca.DeleteBindingsByKnID(context.Background(), nil, "kn1", "")

		So(err, ShouldBeNil)
		So(rows, ShouldEqual, int64(5))
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})
}
