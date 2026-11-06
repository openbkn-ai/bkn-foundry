// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// Archiving a project queues releasing its current sources, keeping those retained.
func TestArchivingQueuesReleasingTheSources(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE t_bkn_project_kb_selection SET f_state=CASE WHEN f_retained=1 THEN 'retained' ELSE 'release_pending' END").WithArgs(int64(9), "p1").WillReturnResult(sqlmock.NewResult(0, 2))
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := (Hooks{}).Archiving(context.Background(), tx, "p1", 9); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
