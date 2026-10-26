// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func derivedClaimEvent() ledgervo.Event {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	event := ledgervo.Event{EventID: "evt-derived", EventType: "claim.created", SchemaVersion: "3.0.0", ConversationID: "conv", InteractionID: "int", OperationID: "op", Attempt: 1, Owner: sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "owner"}, ProducerID: "agent-observability-core", ProducerStreamID: "claim-stream", ProducerEpoch: 1, ProducerSequence: 1, StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: json.RawMessage(`{"source":"fixed-bom"}`)}
	event.PayloadHash = ledgervo.CanonicalPayloadHash(event.Envelope)
	ref := sessionvo.EvidenceRef{Ref: "output:op:1", RefType: sessionvo.EvidenceRefOperationOutput, SourceInteractionID: "int", SourceRevisionID: "rev", SourceOperationID: "op", Version: "v1", ContentHash: "sha256:" + strings.Repeat("a", 64)}
	event.EvidenceRefs = []sessionvo.EvidenceRef{ref}
	event.Claims = []sessionvo.Claim{{ID: "claim", Type: "bom.shared_materials/v1", Materiality: sessionvo.ClaimMaterial, Status: sessionvo.ClaimAsserted, ContentArtifactRef: "artifact:claim-result", RequiredSupportRoles: []string{"computed_from"}, Supports: []sessionvo.ClaimSupport{{TargetRef: ref.Ref, TargetType: sessionvo.SupportOperationOutput, SourceInteractionID: ref.SourceInteractionID, SourceRevisionID: ref.SourceRevisionID, SourceOperationID: ref.SourceOperationID, Version: ref.Version, ContentHash: ref.ContentHash, Role: "computed_from", Status: sessionvo.SupportAdopted}}}}
	return event
}

func TestDerivedEvidenceRejectsUntrustedShapeBeforeSQL(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ledgervo.Event)
	}{
		{"foreign producer", func(e *ledgervo.Event) { e.ProducerID = "agent" }},
		{"incorrect hash", func(e *ledgervo.Event) { e.PayloadHash = "wrong" }},
		{"missing evidence", func(e *ledgervo.Event) { e.EvidenceRefs = nil }},
		{"oversized declaration", func(e *ledgervo.Event) {
			e.Envelope = json.RawMessage(`{"payload":"` + strings.Repeat("x", sessionvo.MaxInlinePayloadBytes) + `"}`)
			e.PayloadHash = ledgervo.CanonicalPayloadHash(e.Envelope)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := derivedClaimEvent()
			test.mutate(&e)
			adapter := &transaction{}
			if _, err := adapter.AppendDerivedEvidence(e); err == nil {
				t.Fatal("invalid event reached SQL")
			}
		})
	}
}

func TestDerivedCausationUsesBoundedMetadataAndRejectsCycle(t *testing.T) {
	for _, test := range []struct {
		name  string
		links any
		want  error
	}{
		{"cycle", `["evt-derived"]`, ievidenceledger.ErrCausalityConflict},
		{"byte budget", nil, isessionstore.ErrEvidenceConfirmationLimit},
		{"corrupt metadata", `invalid`, isessionstore.ErrInvalidEvidenceJSON},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT event_id,").WithArgs(sessionvo.MaxEvidenceConfirmationBytes, "int", maxDerivedCausationEvents+1).WillReturnRows(sqlmock.NewRows([]string{"event_id", "causes"}).AddRow("source", test.links))
			mock.ExpectRollback()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			e := derivedClaimEvent()
			e.CausationEventIDs = []string{"source"}
			err = verifyDerivedCausationAcyclic(context.Background(), tx, e)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
			_ = tx.Rollback()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
