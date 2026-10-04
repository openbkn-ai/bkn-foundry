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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestTerminalPayloadCodecReadsPrivateMetadata(t *testing.T) {
	for _, terminal := range []string{
		`{"mode":"inline","media_type":"application/json","byte_length":11,"inline":{"html":"<>&"}}`,
		`{"mode":"referenced","media_type":"application/json","byte_length":1048577,"ref":"artifact:confirmed"}`,
		`{"mode":"omitted","media_type":"application/json","byte_length":0,"omitted_reason":"serialization_failed"}`,
	} {
		t.Run(terminal, func(t *testing.T) {
			store, mock := newTransactionErrorStore(t)
			now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
			var wrapper map[string]json.RawMessage
			if err := json.Unmarshal([]byte(terminal), &wrapper); err != nil {
				t.Fatal(err)
			}
			wrapper["_trace_evidence_completion"] = json.RawMessage(`{"expectation":{"version":1,"closed":true,"events":[]},"original_durability":"pending","original_observed_refs":[],"original_partial_reasons":[]}`)
			stored, err := json.Marshal(wrapper)
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(now))
			columns := []string{"op", "attempt", "conv", "int", "receipt", "tool", "protocol", "module", "parent", "profile", "input", "output", "error", "request", "trace", "span", "start", "end", "status", "retry"}
			mock.ExpectQuery("FROM bkn_trace_operation_call_facts").WithArgs("op", uint32(1)).WillReturnRows(sqlmock.NewRows(columns).AddRow("op", 1, "conv", "int", "receipt", "tool", "internal", "module", "", "", `{"mode":"inline","media_type":"application/json","byte_length":2,"inline":{}}`, string(stored), "", "req", "trace", "span", now, now, "completed", false))
			mock.ExpectCommit()
			err = store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
				fact, found := tx.FindOperationCallFact("op", 1)
				if !found || fact.EvidenceCompletion == nil {
					t.Fatal("DB terminal payload reader discarded immutable confirmation metadata")
				}
				var original sessionvo.PayloadEnvelope
				if err := json.Unmarshal(stored, &original); err != nil {
					return err
				}
				if fact.Output.Mode != original.Mode || fact.Output.Ref != original.Ref || string(fact.Output.Inline) != string(original.Inline) {
					t.Fatal("metadata contaminated terminal business payload")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTerminalPayloadCodecRejectsMalformedMetadata(t *testing.T) {
	for _, metadata := range []string{
		`null`,
		`{"expectation":{"version":2,"closed":true,"events":[]},"original_durability":"pending"}`,
		`{"expectation":{"version":1,"closed":true,"events":[{"event_id":"event","event_type":"retrieval.completed","publish_disposition":"accepted"}]},"original_durability":"pending"}`,
	} {
		t.Run(metadata, func(t *testing.T) {
			store, mock := newTransactionErrorStore(t)
			now := time.Now()
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(now))
			columns := []string{"op", "attempt", "conv", "int", "receipt", "tool", "protocol", "module", "parent", "profile", "input", "output", "error", "request", "trace", "span", "start", "end", "status", "retry"}
			terminal := `{"mode":"inline","media_type":"application/json","byte_length":2,"inline":{},"_trace_evidence_completion":` + metadata + `}`
			mock.ExpectQuery("FROM bkn_trace_operation_call_facts").WithArgs("op", uint32(1)).WillReturnRows(sqlmock.NewRows(columns).AddRow("op", 1, "conv", "int", "receipt", "tool", "internal", "module", "", "", `{"mode":"inline","media_type":"application/json","byte_length":2,"inline":{}}`, terminal, "", "req", "trace", "span", now, now, "completed", false))
			mock.ExpectRollback()
			err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error { tx.FindOperationCallFact("op", 1); return nil })
			if !errors.Is(err, isessionstore.ErrInvalidEvidenceJSON) {
				t.Fatalf("malformed metadata silently accepted: %v", err)
			}
		})
	}
}
