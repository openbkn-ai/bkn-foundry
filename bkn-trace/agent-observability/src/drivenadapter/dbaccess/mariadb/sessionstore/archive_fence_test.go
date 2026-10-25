// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

func TestTraceArchiveSQLFence(t *testing.T) {
	for _, name := range []string{"unchanged", "version zero", "source changed", "verdict added", "verdict changed", "legacy missing version", "wrong identity", "unverifiable current", "oversized current", "already removed"} {
		t.Run(name, func(t *testing.T) {
			db, m, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			version := uint64(3)
			archived := map[string]any{"interaction_id": "i", "record_integrity_version": version}
			var current any
			wantErr := error(nil)
			switch name {
			case "version zero":
				version = 0
				archived["record_integrity_version"] = version
			case "source changed":
				version = 4
				wantErr = errArchiveSQLChanged
			case "verdict added":
				current = `{"source_version":3,"applicable":false}`
				wantErr = errArchiveSQLChanged
			case "verdict changed":
				current = `{"source_version":3,"applicable":true}`
				archived["stored_record_integrity"] = map[string]any{"source_version": 3, "applicable": false}
				wantErr = errArchiveSQLChanged
			case "legacy missing version":
				delete(archived, "record_integrity_version")
				wantErr = errArchiveSQLUnverifiable
			case "wrong identity":
				archived["interaction_id"] = "another"
				wantErr = errArchiveSQLUnverifiable
			case "oversized current":
				wantErr = errArchiveSQLUnverifiable
			case "unverifiable current":
				current = `not-json`
				wantErr = errArchiveSQLUnverifiable
			}
			raw, err := json.Marshal(map[string]any{"interaction": archived})
			if err != nil {
				t.Fatal(err)
			}
			m.ExpectBegin()
			rows := sqlmock.NewRows([]string{"interaction_id", "record_integrity_version", "size", "record_integrity_json", "manifest_size", "manifest"})
			if name != "already removed" {
				var size any
				if current != nil {
					size = len(current.(string))
				}
				if name == "oversized current" {
					size = 512<<10 + 1
				}
				rows.AddRow("i", version, size, current, nil, nil)
			}
			m.ExpectQuery(`SELECT interaction_id,record_integrity_version.*FOR UPDATE`).WithArgs("i", sessionvo.InteractionActive).WillReturnRows(rows)
			if wantErr != nil {
				m.ExpectRollback()
			} else {
				if name != "already removed" {
					for _, table := range []string{"bkn_trace_operation_call_facts", "bkn_trace_receipts", "bkn_trace_operations", "bkn_trace_assembly_revisions", "bkn_trace_record_integrity_retries", "bkn_trace_interactions"} {
						m.ExpectExec("DELETE FROM " + table + " WHERE interaction_id=\\?").WithArgs("i").WillReturnResult(sqlmock.NewResult(0, 1))
					}
				}
				m.ExpectCommit()
			}
			err = NewTraceArchiveSource(New(db)).Purge(context.Background(), observabilityvo.ArchiveKindTrace, []archivesvc.Candidate{{ID: "i", Payload: raw}})
			if !errors.Is(err, wantErr) {
				t.Fatalf("got %v want %v", err, wantErr)
			}
			if err := m.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSQLFenceComparesEntireIntegrityWithoutNumberRounding(t *testing.T) {
	candidate := archivesvc.Candidate{ID: "i", Payload: []byte(`{"interaction":{"interaction_id":"i","record_integrity_version":0,"stored_record_integrity":{"source_version":18446744073709551615,"applicable":false,"extra":{"b":2,"a":1}}}}`)}
	reordered := []byte(`{ "extra":{"a":1,"b":2}, "applicable":false,"source_version":18446744073709551615}`)
	if err := verifyFrozenSQLSource(candidate, 0, reordered); err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"extra":{"a":1,"b":2},"applicable":false,"source_version":18446744073709551614}`)
	if !errors.Is(verifyFrozenSQLSource(candidate, 0, changed), errArchiveSQLChanged) {
		t.Fatal("lost exact verdict number or unknown fields")
	}
}
