// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"testing"
	"time"
)

func TestAnswerPacketGuard(t *testing.T) {
	for _, tc := range []struct {
		name             string
		capacity         int
		ordinary, failed bool
	}{
		{name: "exact fit", capacity: 4144}, {name: "one byte over", capacity: 4143}, {name: "lookup failure", failed: true}, {name: "ordinary skips lookup", ordinary: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			sqlTx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			tx := &transaction{ctx: context.Background(), tx: sqlTx, now: time.Now().UTC()}
			m := sessionvo.ProjectionMutation{AggregateType: "interaction", AggregateID: "i", EventType: "artifact.persist_requested", EventID: "e", Payload: []byte(`{"a":"x"}`)}
			// 11+1+26+1+9 +4096 =4144 bytes.
			if tc.ordinary {
				m.EventType = "interaction.updated"
			} else {
				q := mock.ExpectQuery("SELECT @@max_allowed_packet")
				if tc.failed {
					q.WillReturnError(errors.New("cap unavailable"))
				} else {
					q.WillReturnRows(sqlmock.NewRows([]string{"cap"}).AddRow(tc.capacity))
				}
			}
			success := tc.ordinary || tc.capacity == 4144
			if success {
				mock.ExpectExec("INSERT INTO bkn_trace_projection_outbox").WillReturnResult(sqlmock.NewResult(1, 1))
			}
			tx.AppendProjection(m)
			if success && tx.err != nil {
				t.Fatal(tx.err)
			}
			if !success && tx.err == nil {
				t.Fatal("unsafe insert accepted")
			}
			if tc.capacity == 4143 && !errors.Is(tx.err, isessionstore.ErrAnswerArtifactTooLarge) {
				t.Fatalf("wrong error %v", tx.err)
			}
			mock.ExpectRollback()
			if err := sqlTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
