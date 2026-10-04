// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestEvidenceConfirmationQueryBoundsBodiesBeforeFetching(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(map[bool]string{false: "within_budget", true: "over_budget"}[overflow], func(t *testing.T) {
			store, mock := newTransactionErrorStore(t)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Now()))
			rows := sqlmock.NewRows([]string{"event_id", "payload_hash", "envelope_bytes", "bounded_envelope"})
			if overflow {
				rows.AddRow("event", "hash", sessionvo.MaxEvidenceConfirmationBytes+1, nil)
			} else {
				event := ledgervo.Event{EventID: "event", PayloadHash: "hash", Envelope: json.RawMessage(`{"html":"<>&","wide":9007199254740993}`)}
				raw, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				rows.AddRow("event", "hash", len(raw), raw)
			}
			mock.ExpectQuery("CASE WHEN SUM\\(OCTET_LENGTH\\(envelope\\)\\) OVER \\(\\) <= \\?").WithArgs(sessionvo.MaxEvidenceConfirmationBytes, "event").WillReturnRows(rows)
			if overflow {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
				reader, ok := tx.(isessionstore.EvidenceConfirmationTransaction)
				if !ok {
					t.Fatal("SQL tx lacks confirmation read capability")
				}
				events, err := reader.ReadEvidenceEventsByIDs([]string{"event"})
				if err != nil {
					return err
				}
				if len(events) != 1 || string(events[0].Envelope) != `{"html":"\u003c\u003e\u0026","wide":9007199254740993}` {
					t.Fatalf("confirmation decoder lost original numeric/HTML representation: %+v", events)
				}
				return nil
			})
			if overflow {
				if !errors.Is(err, isessionstore.ErrEvidenceConfirmationLimit) {
					t.Fatalf("body budget: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
