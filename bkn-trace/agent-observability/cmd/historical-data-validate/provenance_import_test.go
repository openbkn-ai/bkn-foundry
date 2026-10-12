// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"testing"
	"time"
)

func historicalEEFixture() historicalEEPlan {
	now := historicalLedgerTime(time.Date(2026, 9, 12, 1, 2, 3, 123456000, time.UTC))
	graph := `{ "interaction_id": "interaction", "operations": [] }`
	markdown := "original markdown"
	sum := sha256.Sum256([]byte(graph + markdown))
	content := hex.EncodeToString(sum[:])
	view := []byte(`{ "interactionId": "interaction", "question":"原问题", "answer":"原答案" }`)
	sum = sha256.Sum256(view)
	return historicalEEPlan{Contexts: []historicalEEContext{{InteractionID: "interaction", ConversationID: "conversation", Owner: sessionvo.Owner{ApplicationPrincipalID: "agent", EffectiveSubjectType: "user", EffectiveSubjectID: "alice"}}}, HistoricalProjections: []historicalProjectionRow{{InteractionID: "interaction", FactsHash: content, ResolverVersion: "v1", ResolvedAt: &now, Status: "ready", GraphPayload: &graph, MarkdownSnapshot: &markdown, ContentHash: &content, CreatedAt: now, UpdatedAt: now}}, Explanations: []historicalExplanationRow{{InteractionID: "interaction", AccessProfileFingerprint: "original-profile", InputHash: content, Algorithm: "original-algorithm", WriteToken: "original-token", GeneratedAt: "2026-09-12T01:02:03.123456Z", ViewHash: hex.EncodeToString(sum[:]), ViewJSON: hex.EncodeToString(view)}}}
}
func eeMockRows(t *testing.T, row any) *sqlmock.Rows {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return sqlmock.NewRows([]string{"record"}).AddRow(string(raw))
}
func TestHistoricalEEPreservesRawBytesAndExistingCacheIdentity(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	plan := historicalEEFixture()
	p, e := plan.HistoricalProjections[0], plan.Explanations[0]
	mock.ExpectBegin()
	expectLedgerOwner(mock, "alice")
	mock.ExpectQuery("FROM bkn_trace_ee_historical_provenance_projections").WithArgs("interaction").WillReturnRows(sqlmock.NewRows([]string{"record"}))
	mock.ExpectExec("INSERT INTO bkn_trace_ee_historical_provenance_projections").WithArgs(p.InteractionID, p.FactsHash, p.ResolverVersion, time.Time(*p.ResolvedAt), p.Status, *p.GraphPayload, *p.MarkdownSnapshot, *p.ContentHash, nil, time.Time(p.CreatedAt), time.Time(p.UpdatedAt)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM bkn_trace_ee_historical_provenance_projections").WithArgs("interaction").WillReturnRows(eeMockRows(t, p))
	mock.ExpectQuery("FROM bkn_trace_ee_current_explanations").WithArgs("interaction", "original-profile").WillReturnRows(sqlmock.NewRows([]string{"record"}))
	blob, _ := hex.DecodeString(e.ViewJSON)
	mock.ExpectExec("INSERT INTO bkn_trace_ee_current_explanations").WithArgs(e.InteractionID, e.AccessProfileFingerprint, e.InputHash, e.Algorithm, e.WriteToken, e.GeneratedAt, e.ViewHash, blob).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM bkn_trace_ee_current_explanations").WithArgs("interaction", "original-profile").WillReturnRows(eeMockRows(t, e))
	mock.ExpectCommit()
	mock.ExpectQuery("FROM bkn_trace_ee_historical_provenance_projections").WithArgs("interaction").WillReturnRows(eeMockRows(t, p))
	mock.ExpectQuery("FROM bkn_trace_ee_current_explanations").WithArgs("interaction", "original-profile").WillReturnRows(eeMockRows(t, e))
	result, err := importHistoricalEEPlan(context.Background(), db, plan)
	if err != nil || !result.Verified || result.Created != 2 {
		t.Fatalf("restore: %+v %v", result, err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestHistoricalEEExactRepeatAndConflictProtection(t *testing.T) {
	for _, kind := range []string{"repeat", "owner", "content"} {
		t.Run(kind, func(t *testing.T) {
			db, mock, _ := sqlmock.New()
			defer func() { _ = db.Close() }()
			plan := historicalEEFixture()
			plan.Explanations = nil
			p := plan.HistoricalProjections[0]
			mock.ExpectBegin()
			if kind == "owner" {
				expectLedgerOwner(mock, "other")
			} else {
				expectLedgerOwner(mock, "alice")
				if kind == "content" {
					p.ResolverVersion = "current-other"
				}
				mock.ExpectQuery("FROM bkn_trace_ee_historical_provenance_projections").WithArgs("interaction").WillReturnRows(eeMockRows(t, p))
			}
			if kind == "repeat" {
				mock.ExpectCommit()
				mock.ExpectQuery("FROM bkn_trace_ee_historical_provenance_projections").WithArgs("interaction").WillReturnRows(eeMockRows(t, p))
			} else {
				mock.ExpectRollback()
			}
			result, err := importHistoricalEEPlan(context.Background(), db, plan)
			if kind == "repeat" {
				if err != nil || !result.Verified || result.AlreadyVerified != 1 || result.Created != 0 {
					t.Fatalf("repeat %+v %v", result, err)
				}
			} else if err == nil || result.Verified {
				t.Fatalf("conflict accepted %+v %v", result, err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestHistoricalEEPurePreflightRejectsCorruptionAndMissingContext(t *testing.T) {
	for _, kind := range []string{"viewhash", "graphhash", "identity", "context", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			p := historicalEEFixture()
			switch kind {
			case "viewhash":
				p.Explanations[0].ViewHash = "wrong"
			case "graphhash":
				bad := "wrong"
				p.HistoricalProjections[0].ContentHash = &bad
			case "identity":
				raw := []byte(`{"interactionId":"other"}`)
				sum := sha256.Sum256(raw)
				p.Explanations[0].ViewJSON = hex.EncodeToString(raw)
				p.Explanations[0].ViewHash = hex.EncodeToString(sum[:])
			case "context":
				p.Contexts = nil
			case "duplicate":
				other := p.HistoricalProjections[0]
				other.ResolverVersion = "different"
				p.HistoricalProjections = append(p.HistoricalProjections, other)
			}
			if _, err := prepareHistoricalEE(p); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
}

func TestHistoricalEEExplanationRepeatAndFailedReadback(t *testing.T) {
	for _, kind := range []string{"repeat", "content", "readback"} {
		t.Run(kind, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			plan := historicalEEFixture()
			plan.HistoricalProjections = nil
			e := plan.Explanations[0]
			mock.ExpectBegin()
			expectLedgerOwner(mock, "alice")
			switch kind {
			case "readback":
				mock.ExpectQuery("FROM bkn_trace_ee_current_explanations").WithArgs(e.InteractionID, e.AccessProfileFingerprint).WillReturnRows(sqlmock.NewRows([]string{"record"}))
				raw, _ := hex.DecodeString(e.ViewJSON)
				mock.ExpectExec("INSERT INTO bkn_trace_ee_current_explanations").WithArgs(e.InteractionID, e.AccessProfileFingerprint, e.InputHash, e.Algorithm, e.WriteToken, e.GeneratedAt, e.ViewHash, raw).WillReturnResult(sqlmock.NewResult(0, 1))
				e.WriteToken = "unexpected-target"
			case "content":
				e.WriteToken = "unrelated-target"
			}
			mock.ExpectQuery("FROM bkn_trace_ee_current_explanations").WithArgs(e.InteractionID, e.AccessProfileFingerprint).WillReturnRows(eeMockRows(t, e))
			if kind == "repeat" {
				mock.ExpectCommit()
				mock.ExpectQuery("FROM bkn_trace_ee_current_explanations").WithArgs(e.InteractionID, e.AccessProfileFingerprint).WillReturnRows(eeMockRows(t, e))
			} else {
				mock.ExpectRollback()
			}
			result, err := importHistoricalEEPlan(context.Background(), db, plan)
			if kind == "repeat" {
				if err != nil || !result.Verified || result.Created != 0 || result.AlreadyVerified != 1 {
					t.Fatalf("repeat %+v %v", result, err)
				}
			} else if err == nil || result.Verified || result.Created != 0 {
				t.Fatalf("invalid readback accepted %+v %v", result, err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
