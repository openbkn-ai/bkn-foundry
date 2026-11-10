// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package dbaccess

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

// queryRecorder matches any statement and keeps it, so a test can look at the
// columns a write names.
type queryRecorder struct{ queries []string }

func (r *queryRecorder) Match(_, actual string) error {
	r.queries = append(r.queries, actual)
	return nil
}

func newRecordingFunctionMetadataDB(t *testing.T) (*functionMetadataDB, sqlmock.Sqlmock, *queryRecorder) {
	t.Helper()
	recorder := &queryRecorder{}
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(recorder))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &functionMetadataDB{orm: ormhelper.New(sqlDB, "openbkn")}, mock, recorder
}

func TestFunctionMetadataWritesNameCypherColumnsOnlyForCypher(t *testing.T) {
	python := func() *model.FunctionMetadataDB {
		return &model.FunctionMetadataDB{Version: "v-python", ScriptType: "python", Code: "def handler(event): pass"}
	}
	cypher := func() *model.FunctionMetadataDB {
		return &model.FunctionMetadataDB{
			Version: "v-cypher", ScriptType: model.ScriptTypeCypher,
			Code: "MATCH (s:supplier {id: $id}) RETURN s.name", KnID: "kn-1", ResultShape: "value",
		}
	}
	tests := []struct {
		name       string
		run        func(context.Context, *functionMetadataDB) error
		wantCypher bool
	}{
		{"insert python", func(ctx context.Context, db *functionMetadataDB) error {
			_, err := db.InsertFuncMetadata(ctx, nil, python())
			return err
		}, false},
		{"update python", func(ctx context.Context, db *functionMetadataDB) error {
			return db.UpdateByVersion(ctx, nil, python())
		}, false},
		{"batch insert python", func(ctx context.Context, db *functionMetadataDB) error {
			_, err := db.InsertFuncMetadatas(ctx, nil, []*model.FunctionMetadataDB{python(), python()})
			return err
		}, false},
		{"insert cypher", func(ctx context.Context, db *functionMetadataDB) error {
			_, err := db.InsertFuncMetadata(ctx, nil, cypher())
			return err
		}, true},
		{"update cypher", func(ctx context.Context, db *functionMetadataDB) error {
			return db.UpdateByVersion(ctx, nil, cypher())
		}, true},
		{"batch insert mixed", func(ctx context.Context, db *functionMetadataDB) error {
			_, err := db.InsertFuncMetadatas(ctx, nil, []*model.FunctionMetadataDB{python(), cypher()})
			return err
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, recorder := newRecordingFunctionMetadataDB(t)
			mock.ExpectExec("").WillReturnResult(sqlmock.NewResult(1, 1))
			if err := tt.run(t.Context(), db); err != nil {
				t.Fatalf("write failed: %v", err)
			}
			if len(recorder.queries) != 1 {
				t.Fatalf("got %d statements, want 1", len(recorder.queries))
			}
			query := recorder.queries[0]
			for _, column := range []string{"f_kn_id", "f_result_shape"} {
				if got := strings.Contains(query, column); got != tt.wantCypher {
					t.Fatalf("statement names %s = %v, want %v: %s", column, got, tt.wantCypher, query)
				}
			}
		})
	}
}

func TestFunctionMetadataBatchInsertFillsCypherColumnsForEveryRow(t *testing.T) {
	db, mock, _ := newRecordingFunctionMetadataDB(t)
	// 15 shared columns + 2 Cypher columns per row; the Python row carries empty values.
	args := make([]driver.Value, 0, 34)
	for i := 0; i < 34; i++ {
		args = append(args, sqlmock.AnyArg())
	}
	mock.ExpectExec("").WithArgs(args...).WillReturnResult(sqlmock.NewResult(1, 2))
	_, err := db.InsertFuncMetadatas(t.Context(), nil, []*model.FunctionMetadataDB{
		{Version: "v-python", ScriptType: "python"},
		{Version: "v-cypher", ScriptType: model.ScriptTypeCypher, KnID: "kn-1", ResultShape: "table"},
	})
	if err != nil {
		t.Fatalf("batch insert failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
