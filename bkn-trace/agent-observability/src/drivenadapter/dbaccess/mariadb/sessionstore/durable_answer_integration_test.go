// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

// The durable-answer cases exercise real sessionsvc transactions and SQL jobs,
// with only artifact storage replaced by a fault-injectable test adapter:
//   - BEFORE INSERT trigger rejects the answer outbox insert: terminal state,
//     lease release, revisions, and the job must roll back together.
//   - completed+assembling releases the next StartInteraction; identical finish
//     replay does not create another answer job.
//   - expire a claimed SQL lease, reconstruct Store and Worker, then recover the
//     exact answer; a stale worker must not settle the new lease.
//   - pending/dead jobs block archive cleanup and preserve the answer; settled
//     jobs allow a fresh snapshot or reject a stale source-version snapshot.

func durableAnswerDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("BKN_TRACE_DURABLE_ANSWER_ADMIN_DSN")
	if dsn == "" {
		t.Skip("explicit local durable-answer admin DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid durable-answer admin DSN")
	}
	allowed := cfg.Addr == "127.0.0.1:33061" || os.Getenv("CI") == "true" && cfg.Addr == "127.0.0.1:3306"
	if cfg.Net != "tcp" || !allowed || cfg.DBName != "" {
		t.Fatal("durable-answer tests require the approved loopback endpoint and no selected database")
	}
	cfg.ParseTime = true
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 10 * time.Second
	cfg.WriteTimeout = 10 * time.Second
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal("open durable-answer admin connection failed")
	}
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "bkn_trace_durable_answer_" + hex.EncodeToString(suffix[:])
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+name+"`"); err != nil {
		t.Fatal("create isolated durable-answer database failed")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP DATABASE `"+name+"`"); err != nil {
			t.Error("drop isolated durable-answer database failed")
		}
	})
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal("open isolated durable-answer database failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=?", name).Scan(&count); err != nil || count != 0 {
		t.Fatal("durable-answer fixture database must start empty")
	}
	// v031 owns a separate shared bkn_audit schema. This is a Core transaction
	// integration fixture, not a full migrator test: never create/alter that schema.
	for _, migration := range Migrations() {
		if migration.Version == "031" {
			continue
		}
		statements, err := splitSQLStatements(migration.SQL)
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range statements {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("isolated Core migration %s: %v", migration.Version, err)
			}
		}
	}
	return db
}

// Only the external artifact boundary is fake; SQL transaction, queue lease,
// retry state, session admission and archive fencing remain production code.
type durableAnswerArtifacts struct {
	mu     sync.Mutex
	fail   error
	calls  int
	values map[string]evidencevo.EvidenceArtifact
}

func (s *durableAnswerArtifacts) StoreArtifact(_ context.Context, a evidencevo.EvidenceArtifact) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.fail != nil {
		return false, s.fail
	}
	if s.values == nil {
		s.values = map[string]evidencevo.EvidenceArtifact{}
	}
	if old, ok := s.values[a.ArtifactID]; ok {
		if old.ContentHash != a.ContentHash {
			return false, iartifactstore.ErrArtifactIDConflict
		}
		return true, nil
	}
	s.values[a.ArtifactID] = a
	return false, nil
}
func (s *durableAnswerArtifacts) GetArtifact(_ context.Context, id string, scope evidencevo.QueryScope) (evidencevo.EvidenceArtifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.values[id]
	if !ok || !evidencevo.MatchesArtifactScope(a, scope) {
		return evidencevo.EvidenceArtifact{}, false, nil
	}
	return a, true, nil
}
func (s *durableAnswerArtifacts) ListArtifactsByRequestID(_ context.Context, id string, options iartifactstore.QueryOptions) (iartifactstore.QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result iartifactstore.QueryResult
	for _, a := range s.values {
		if a.RequestID == id && evidencevo.MatchesArtifactScope(a, options.Scope) {
			result.Entries = append(result.Entries, a)
		}
	}
	return result, nil
}

type durableAnswerProjectionSink struct{}

func (durableAnswerProjectionSink) Project(_ context.Context, item iprojectionoutbox.Item) error {
	if item.EventType == "artifact.persist_requested" {
		return errors.New("durable answer must not be routed to ordinary projection sink")
	}
	return nil
}
func durableAnswerJSON(t *testing.T, interactionID, ownerID string) (json.RawMessage, evidencevo.EvidenceArtifact) {
	t.Helper()
	artifact, validation := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
		ArtifactID: "answer-" + interactionID, ArtifactType: evidencevo.ArtifactTypeResult, RequestID: "request-" + interactionID,
		InteractionID: interactionID, AccountID: ownerID, AccountType: "user", EffectiveSubjectID: ownerID,
		ApplicationPrincipalID: "durable-answer-app", ContentType: "text/plain", SchemaVersion: evidencevo.ArtifactContractVersion,
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Content: "isolated durable answer",
	})
	if len(validation) != 0 {
		t.Fatalf("answer fixture invalid: %v", validation)
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return raw, artifact
}

func TestDurableAnswerTransactionRecoveryMariaDB(t *testing.T) {
	t.Run("outbox_insert_rollback", func(t *testing.T) {
		db := durableAnswerDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		service, owner, interaction, command, _ := durableAnswerSession(t, ctx, db)
		before := durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_projection_outbox")
		_, err := db.ExecContext(ctx, `CREATE TRIGGER durable_answer_insert_failure BEFORE INSERT ON bkn_trace_projection_outbox FOR EACH ROW BEGIN IF NEW.event_type='artifact.persist_requested' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected durable answer insert failure'; END IF; END`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.TerminateInteraction(ctx, command); err == nil {
			t.Fatal("outbox failure must abort terminal transaction")
		}
		current, err := service.GetInteraction(ctx, owner, interaction.ID)
		if err != nil || current.ExecutionStatus != sessionvo.InteractionActive || current.RowVersion != interaction.RowVersion || current.LeaseVersion != interaction.LeaseVersion || current.ClosureManifest != nil {
			t.Fatalf("terminal transaction did not roll back: status=%s err=%v", current.ExecutionStatus, err)
		}
		if durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_projection_outbox") != before || durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_assembly_revisions WHERE interaction_id=?", interaction.ID) != 0 {
			t.Fatal("failed terminal transaction left jobs or revisions")
		}
		if _, err = service.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: interaction.ConversationID, IdempotencyKey: "blocked-next"}); err == nil {
			t.Fatal("rollback must retain the active conversation lease")
		}
		if _, err = db.ExecContext(ctx, "DROP TRIGGER durable_answer_insert_failure"); err != nil {
			t.Fatal(err)
		}
		if _, err = service.TerminateInteraction(ctx, command); err != nil {
			t.Fatal(err)
		}
		if durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE event_type='artifact.persist_requested'") != 1 {
			t.Fatal("successful retry must commit one durable answer")
		}
	})
	t.Run("lease_restart_and_archive_fence", func(t *testing.T) {
		db := durableAnswerDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		service, owner, interaction, command, artifact := durableAnswerSession(t, ctx, db)
		terminal, err := service.TerminateInteraction(ctx, command)
		if err != nil || terminal.ExecutionStatus != sessionvo.InteractionCompleted || terminal.EvidenceStatus != sessionvo.EvidenceAssembling {
			t.Fatalf("completion must release execution while answer persists: status=%s evidence=%s err=%v", terminal.ExecutionStatus, terminal.EvidenceStatus, err)
		}
		if _, err = service.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: interaction.ConversationID, IdempotencyKey: "next"}); err != nil {
			t.Fatalf("next interaction blocked by pending artifact: %v", err)
		}
		if _, err = service.TerminateInteraction(ctx, command); err != nil {
			t.Fatalf("identical finish retry: %v", err)
		}
		if durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE event_type='artifact.persist_requested'") != 1 {
			t.Fatal("finish replay duplicated answer job")
		}
		store := New(db)
		high, err := store.ProjectionHighWatermark(ctx)
		if err != nil {
			t.Fatal(err)
		}
		history, err := store.ScanProjectionHistory(ctx, 0, high, 100)
		if err != nil {
			t.Fatal(err)
		}
		answerSeen, ordinarySeen := false, false
		for _, item := range history {
			if item.EventType == sessionsvc.AnswerArtifactRequested {
				answerSeen = true
				if item.ID == 0 || item.EventID != terminal.ClosureManifest.AnswerArtifactPersistence.EventID || item.AggregateID != interaction.ID || item.Payload != nil {
					t.Fatal("history must preserve answer metadata without loading its body")
				}
			} else {
				ordinarySeen = true
				if len(item.Payload) == 0 {
					t.Fatal("history lost ordinary projection body")
				}
			}
		}
		if !answerSeen || !ordinarySeen {
			t.Fatal("history fixture must include answer and ordinary projections")
		}
		source := NewTraceArchiveSource(store)
		stale := durableAnswerFrozenFence(t, db, interaction.ID)
		durableAnswerAssertArchiveBlocked(t, ctx, db, source, stale)
		leased, err := store.Lease(ctx, 100, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		var old iprojectionoutbox.Item
		for _, item := range leased {
			if item.EventType == sessionsvc.AnswerArtifactRequested {
				old = item
			}
		}
		if old.ID == 0 {
			t.Fatal("durable job was not leased")
		}
		if _, err = db.ExecContext(ctx, "UPDATE bkn_trace_projection_outbox SET locked_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE status='processing'"); err != nil {
			t.Fatal(err)
		}
		// New objects emulate process loss. Reclamation uses SQL time, not a sleep.
		recoveredStore := New(db)
		recoveredService := sessionsvc.New(recoveredStore, sessionsvc.Options{DurableAnswerEnabled: true})
		artifacts := &durableAnswerArtifacts{fail: errors.New("injected artifact outage")}
		handler := sessionsvc.NewAnswerArtifactHandler(recoveredService, artifacts)
		worker := projectorsvc.NewWorker(recoveredStore, durableAnswerProjectionSink{}, projectorsvc.WorkerOptions{AnswerArtifactHandler: handler, BatchSize: 100})
		result, err := worker.RunOnce(ctx)
		if err != nil || result.Retried != 1 {
			t.Fatalf("artifact outage must retain a retryable SQL answer: result=%+v err=%v", result, err)
		}
		durableAnswerAssertArchiveBlocked(t, ctx, db, source, stale)
		artifacts.fail = nil
		if _, err = db.ExecContext(ctx, "UPDATE bkn_trace_projection_outbox SET available_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE event_type='artifact.persist_requested'"); err != nil {
			t.Fatal(err)
		}
		worker = projectorsvc.NewWorker(New(db), durableAnswerProjectionSink{}, projectorsvc.WorkerOptions{AnswerArtifactHandler: handler, BatchSize: 100})
		result, err = worker.RunOnce(ctx)
		if err != nil || result.Delivered == 0 {
			t.Fatalf("recovery worker: result=%+v err=%v", result, err)
		}
		current, err := recoveredService.GetInteraction(ctx, owner, interaction.ID)
		if err != nil || current.ClosureManifest == nil || current.ClosureManifest.AnswerArtifactPersistence == nil || current.ClosureManifest.AnswerArtifactPersistence.State != sessionvo.AnswerArtifactSettled {
			t.Fatalf("answer did not settle after restart: err=%v", err)
		}
		var deliveredPayload, deliveredEvent, deliveredAggregate, deliveredStatus string
		if err := db.QueryRowContext(ctx, "SELECT payload,event_id,aggregate_id,status FROM bkn_trace_projection_outbox WHERE outbox_id=?", old.ID).Scan(&deliveredPayload, &deliveredEvent, &deliveredAggregate, &deliveredStatus); err != nil {
			t.Fatal(err)
		}
		if deliveredPayload != "{}" || deliveredEvent != old.EventID || deliveredAggregate != old.AggregateID || deliveredStatus != "delivered" {
			t.Fatal("answer ACK must clear only body while preserving delivered metadata")
		}
		saved, ok := artifacts.values[artifact.ArtifactID]
		if !ok || saved.ContentHash != artifact.ContentHash || saved.Content != artifact.Content || len(artifacts.values) != 1 {
			t.Fatal("recovered artifact does not equal committed answer")
		}
		if err = handler.HandleAnswerArtifact(ctx, old); !errors.Is(err, iprojectionoutbox.ErrLeaseLost) {
			t.Fatalf("stale lease must not settle the interaction: %v", err)
		}
		if err = source.Purge(ctx, observabilityvo.ArchiveKindTrace, []archivesvc.Candidate{stale}); !errors.Is(err, errArchiveSQLChanged) {
			t.Fatalf("pre-settlement snapshot must be fenced: %v", err)
		}
		fresh, err := source.FreezeBatch(ctx, observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{To: time.Now().UTC().Add(time.Second)})
		if err != nil || len(fresh.Candidates) != 1 {
			t.Fatalf("settled freeze count=%d err=%v", len(fresh.Candidates), err)
		}
		if err = source.Purge(ctx, observabilityvo.ArchiveKindTrace, fresh.Candidates); err != nil {
			t.Fatalf("settled fresh snapshot cleanup: %v", err)
		}
		if durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_interactions WHERE interaction_id=?", interaction.ID) != 0 {
			t.Fatal("settled interaction was not archived")
		}
	})
	t.Run("dead_answer_is_retained", func(t *testing.T) {
		db := durableAnswerDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		service, owner, interaction, command, _ := durableAnswerSession(t, ctx, db)
		if _, err := service.TerminateInteraction(ctx, command); err != nil {
			t.Fatal(err)
		}
		var pendingHash string
		if err := db.QueryRowContext(ctx, "SELECT SHA2(payload,256) FROM bkn_trace_projection_outbox WHERE event_type='artifact.persist_requested'").Scan(&pendingHash); err != nil {
			t.Fatal(err)
		}
		artifacts := &durableAnswerArtifacts{fail: iartifactstore.ErrArtifactIDConflict}
		worker := projectorsvc.NewWorker(New(db), durableAnswerProjectionSink{}, projectorsvc.WorkerOptions{AnswerArtifactHandler: sessionsvc.NewAnswerArtifactHandler(service, artifacts), BatchSize: 100})
		result, err := worker.RunOnce(ctx)
		if err != nil || result.Dead != 1 {
			t.Fatalf("dead worker result=%+v err=%v", result, err)
		}
		current, err := service.GetInteraction(ctx, owner, interaction.ID)
		if err != nil || current.ClosureManifest == nil || current.ClosureManifest.AnswerArtifactPersistence == nil || current.ClosureManifest.AnswerArtifactPersistence.State != sessionvo.AnswerArtifactDead {
			t.Fatalf("dead answer marker missing: %v", err)
		}
		durableAnswerAssertArchiveBlocked(t, ctx, db, NewTraceArchiveSource(New(db)), durableAnswerFrozenFence(t, db, interaction.ID))
		if durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE event_type='artifact.persist_requested' AND status='dead'") != 1 {
			t.Fatal("dead answer SQL job was lost")
		}
		var deadHash, dlqHash string
		if err := db.QueryRowContext(ctx, "SELECT SHA2(o.payload,256),SHA2(d.payload,256) FROM bkn_trace_projection_outbox o JOIN bkn_trace_dlq d ON d.source_kind='projection' AND d.source_id=o.event_id WHERE o.event_type='artifact.persist_requested'").Scan(&deadHash, &dlqHash); err != nil {
			t.Fatal(err)
		}
		if deadHash != pendingHash || dlqHash != pendingHash {
			t.Fatal("dead task and DLQ must retain exact pending answer body")
		}
		var payload string
		if err = db.QueryRowContext(ctx, "SELECT payload FROM bkn_trace_projection_outbox WHERE event_type='artifact.persist_requested'").Scan(&payload); err != nil || !strings.Contains(payload, "isolated durable answer") {
			t.Fatal("dead answer payload was lost")
		}
	})
}

func durableAnswerSession(t *testing.T, ctx context.Context, db *sql.DB) (*sessionsvc.Service, sessionvo.Owner, sessionvo.Interaction, sessionsvc.TerminateInteractionCommand, evidencevo.EvidenceArtifact) {
	t.Helper()
	service := sessionsvc.New(New(db), sessionsvc.Options{DurableAnswerEnabled: true})
	owner := sessionvo.Owner{ApplicationPrincipalID: "durable-answer-app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "durable-answer-owner"}
	conversation, err := service.EnsureCurrentConversation(ctx, sessionsvc.EnsureConversationCommand{Owner: owner, ExternalConversationKey: "durable-answer-conversation", IdempotencyKey: "ensure"})
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := service.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start"})
	if err != nil {
		t.Fatal(err)
	}
	raw, artifact := durableAnswerJSON(t, interaction.ID, owner.EffectiveSubjectID)
	command := sessionsvc.TerminateInteractionCommand{Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionCompleted, TerminalIdempotencyKey: "finish", DeriveManifest: true, AnswerArtifact: raw, Manifest: sessionvo.ClosureManifest{Version: "3.0.0", CompletionReason: "completed"}}
	return service, owner, interaction, command, artifact
}
func durableAnswerCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// Construct just the preverified SQL fence used by Purge, independent of Freeze's
// new pending gate. This is not claimed to be a full archive payload.
func durableAnswerFrozenFence(t *testing.T, db *sql.DB, id string) archivesvc.Candidate {
	t.Helper()
	var version uint64
	var integrity sql.NullString
	if err := db.QueryRow("SELECT record_integrity_version,record_integrity_json FROM bkn_trace_interactions WHERE interaction_id=?", id).Scan(&version, &integrity); err != nil {
		t.Fatal(err)
	}
	value := map[string]any{"interaction_id": id, "record_integrity_version": version}
	var raw []byte
	if integrity.Valid {
		raw = []byte(integrity.String)
		value["stored_record_integrity"] = json.RawMessage(raw)
	}
	payload, err := json.Marshal(map[string]any{"interaction": value})
	if err != nil {
		t.Fatal(err)
	}
	candidate := archivesvc.Candidate{ID: id, Payload: payload}
	if err = verifyFrozenSQLSource(candidate, version, raw); err != nil {
		t.Fatalf("fixture SQL fence invalid: %v", err)
	}
	return candidate
}
func durableAnswerAssertArchiveBlocked(t *testing.T, ctx context.Context, db *sql.DB, source *TraceArchiveSource, candidate archivesvc.Candidate) {
	t.Helper()
	var before string
	if err := db.QueryRowContext(ctx, "SELECT SHA2(payload,256) FROM bkn_trace_projection_outbox WHERE event_type='artifact.persist_requested' AND aggregate_id=?", candidate.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err := source.FreezeBatch(ctx, observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{To: time.Now().UTC().Add(time.Second)})
	if !errors.Is(err, archivesvc.ErrIncompleteCandidates) || !strings.Contains(err.Error(), "answer artifact persistence") {
		t.Fatalf("pending/dead answer freeze must fail specifically at persistence gate: %v", err)
	}
	err = source.Purge(ctx, observabilityvo.ArchiveKindTrace, []archivesvc.Candidate{candidate})
	if !errors.Is(err, archivesvc.ErrIncompleteCandidates) || !strings.Contains(err.Error(), "answer artifact persistence") {
		t.Fatalf("pending/dead answer purge must fail specifically at persistence gate: %v", err)
	}
	if durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_interactions WHERE interaction_id=?", candidate.ID) != 1 || durableAnswerCount(t, db, "SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE event_type='artifact.persist_requested' AND aggregate_id=?", candidate.ID) != 1 {
		t.Fatal("blocked archive deleted interaction or answer job")
	}
	var after string
	if err := db.QueryRowContext(ctx, "SELECT SHA2(payload,256) FROM bkn_trace_projection_outbox WHERE event_type='artifact.persist_requested' AND aggregate_id=?", candidate.ID).Scan(&after); err != nil || after != before {
		t.Fatal("blocked archive altered durable answer bytes")
	}

}
