// Copyright openbkn.ai

package common

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	sq "github.com/Masterminds/squirrel"
)

func TestEnsureRowUpdated(t *testing.T) {
	const existsSQL = "SELECT 1 FROM t WHERE f_branch = ? AND f_id = ? LIMIT 1"
	key := sq.Eq{"f_id": "a", "f_branch": "dev"}
	queryErr := errors.New("connection lost")

	cases := []struct {
		name   string
		result driver.Result
		expect func(sqlmock.Sqlmock)
		want   error
	}{
		{name: "changed row", result: sqlmock.NewResult(0, 1)},
		{
			name:   "row already holds the values",
			result: sqlmock.NewResult(0, 0),
			expect: func(m sqlmock.Sqlmock) {
				m.ExpectQuery(existsSQL).WithArgs("dev", "a").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
			},
		},
		{
			name:   "row missing",
			result: sqlmock.NewResult(0, 0),
			expect: func(m sqlmock.Sqlmock) {
				m.ExpectQuery(existsSQL).WithArgs("dev", "a").WillReturnRows(sqlmock.NewRows([]string{"1"}))
			},
			want: ErrRowNotFound,
		},
		{
			name:   "existence query fails",
			result: sqlmock.NewResult(0, 0),
			expect: func(m sqlmock.Sqlmock) {
				m.ExpectQuery(existsSQL).WithArgs("dev", "a").WillReturnError(queryErr)
			},
			want: queryErr,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if tc.expect != nil {
				tc.expect(mock)
			}

			err = EnsureRowUpdated(context.Background(), db, tc.result, "t", key)

			if !errors.Is(err, tc.want) {
				t.Fatalf("EnsureRowUpdated() = %v, want %v", err, tc.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
