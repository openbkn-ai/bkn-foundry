// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package idregistry

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_IDRegistryAccess(t *testing.T) {
	Convey("Test idRegistryAccess\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		ira := &idRegistryAccess{db: db}
		ctx := context.Background()
		insert := "INSERT INTO t_bkn_project_id (f_project_id,f_kind,f_source_key,f_id,f_origin,f_basis,f_locked,f_create_time,f_update_time) VALUES "
		replace := "f_locked = 0 AND f_origin = 'fallback' AND VALUES(f_origin) = 'semantic'"
		// f_origin is assigned last: the condition reads it, and MariaDB applies the assignments in order.
		upsert := " ON DUPLICATE KEY UPDATE f_id = IF(" + replace + ", VALUES(f_id), f_id), f_basis = IF(" + replace +
			", VALUES(f_basis), f_basis), f_update_time = IF(" + replace + ", VALUES(f_update_time), f_update_time), f_origin = IF(" +
			replace + ", VALUES(f_origin), f_origin)"
		placeholders := func(rows int) string {
			return strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?,?,?),", rows), ",")
		}

		Convey("ListIDs reads the project's registered ids\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta(
				"SELECT f_project_id, f_kind, f_source_key, f_id, f_origin, f_basis, f_locked, f_create_time, f_update_time FROM t_bkn_project_id WHERE f_project_id = ? ORDER BY f_kind, f_source_key")).
				WithArgs("p1").
				WillReturnRows(sqlmock.NewRows(idColumns).
					AddRow("p1", interfaces.ID_KIND_PROPERTIES, "r1/编号", "supplier_code", interfaces.ID_ORIGIN_SEMANTIC, `{"api_name":"supplier_code"}`, true, 1, 2))
			ids, err := ira.ListIDs(ctx, "p1")
			So(err, ShouldBeNil)
			So(ids, ShouldResemble, []*interfaces.RegisteredID{{
				ProjectID: "p1", IDKey: interfaces.IDKey{Kind: interfaces.ID_KIND_PROPERTIES, SourceKey: "r1/编号"},
				ID: "supplier_code", Origin: interfaces.ID_ORIGIN_SEMANTIC, Basis: json.RawMessage(`{"api_name":"supplier_code"}`),
				Locked: true, CreateTime: 1, UpdateTime: 2,
			}})
		})

		Convey("RecordIDs writes in batches and leaves registered ids to the database's rule\n", func() {
			ids := make([]*interfaces.RegisteredID, batchSize+1)
			for i := range ids {
				ids[i] = &interfaces.RegisteredID{ProjectID: "p1",
					IDKey: interfaces.IDKey{Kind: interfaces.ID_KIND_OBJECT_TYPES, SourceKey: fmt.Sprintf("r%d", i)},
					ID:    fmt.Sprintf("ot_%06d", i), Origin: interfaces.ID_ORIGIN_FALLBACK, CreateTime: 5, UpdateTime: 5}
			}
			ids[batchSize].Basis = json.RawMessage(`{"physical_name":"供应商"}`)
			smock.ExpectExec(regexp.QuoteMeta(insert + placeholders(batchSize) + upsert)).WillReturnResult(sqlmock.NewResult(0, batchSize))
			smock.ExpectExec(regexp.QuoteMeta(insert + placeholders(1) + upsert)).
				WithArgs("p1", interfaces.ID_KIND_OBJECT_TYPES, fmt.Sprintf("r%d", batchSize), fmt.Sprintf("ot_%06d", batchSize),
					interfaces.ID_ORIGIN_FALLBACK, `{"physical_name":"供应商"}`, false, int64(5), int64(5)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			So(ira.RecordIDs(ctx, ids), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("RecordIDs writes an empty basis as an empty object\n", func() {
			smock.ExpectExec(regexp.QuoteMeta(insert + placeholders(1) + upsert)).
				WithArgs("p1", interfaces.ID_KIND_PROPERTIES, "r1/编号", "p_1a2b3c", interfaces.ID_ORIGIN_FALLBACK, "{}", false, int64(0), int64(0)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			So(ira.RecordIDs(ctx, []*interfaces.RegisteredID{{ProjectID: "p1",
				IDKey: interfaces.IDKey{Kind: interfaces.ID_KIND_PROPERTIES, SourceKey: "r1/编号"},
				ID:    "p_1a2b3c", Origin: interfaces.ID_ORIGIN_FALLBACK}}), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("RecordIDs and LockIDs do nothing for nothing\n", func() {
			So(ira.RecordIDs(ctx, nil), ShouldBeNil)
			So(ira.LockIDs(ctx, "p1", nil, 9), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("LockIDs locks only the project's unlocked ids of the keys, in batches\n", func() {
			keys := make([]interfaces.IDKey, batchSize+1)
			for i := range keys {
				keys[i] = interfaces.IDKey{Kind: interfaces.ID_KIND_PROPERTIES, SourceKey: fmt.Sprintf("r1/f%d", i)}
			}
			match := func(n int) string {
				return "(" + strings.TrimSuffix(strings.Repeat("(f_kind = ? AND f_source_key = ?) OR ", n), " OR ") + ")"
			}
			lock := "UPDATE t_bkn_project_id SET f_locked = ?, f_update_time = ? WHERE f_locked = ? AND f_project_id = ? AND "
			smock.ExpectExec(regexp.QuoteMeta(lock + match(batchSize))).WillReturnResult(sqlmock.NewResult(0, batchSize))
			last := []driver.Value{true, int64(9), false, "p1", interfaces.ID_KIND_PROPERTIES, fmt.Sprintf("r1/f%d", batchSize)}
			smock.ExpectExec(regexp.QuoteMeta(lock + match(1))).WithArgs(last...).WillReturnResult(sqlmock.NewResult(0, 1))
			So(ira.LockIDs(ctx, "p1", keys, 9), ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})
	})
}
