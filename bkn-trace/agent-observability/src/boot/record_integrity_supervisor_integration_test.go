// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package boot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	sqlstore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// Only the external inspection dependency is failed. Scheduling, claims,
// persisted history, source invalidation and verdict CAS use real MariaDB.
type retryInspectionProbe struct {
	*sqlstore.Store
	failure error
	fail    bool
	reads   int
}

func (p *retryInspectionProbe) ReadEvidenceSnapshot(ctx context.Context, id string) (sessionvo.EvidenceSnapshot, bool, error) {
	p.reads++
	if p.failure != nil {
		return sessionvo.EvidenceSnapshot{}, false, p.failure
	}
	if p.fail {
		return sessionvo.EvidenceSnapshot{}, false, errors.New("synthetic inspection dependency unavailable")
	}
	return p.Store.ReadEvidenceSnapshot(ctx, id)
}

type singleIntegrityPass struct {
	service *evidencesvc.Service
	cancel  context.CancelFunc
	err     error
}

func (p *singleIntegrityPass) PersistRecordIntegrityBatch(ctx context.Context, cursor string, limit int) (string, error) {
	next, err := p.service.PersistRecordIntegrityBatch(ctx, cursor, limit)
	p.err = err
	p.cancel()
	return next, err
}
func TestRecordIntegritySupervisorDurableState(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("isolated MariaDB DSN required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sqlstore.New(db)
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("integrity-%d", time.Now().UnixNano())
	owner := sessionvo.Owner{ApplicationPrincipalID: "integrity-test", EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: "integrity-test"}
	now := time.Now().UTC()
	if err = store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: id, Owner: owner, ExternalConversationKey: id, Generation: 1, Status: sessionvo.ConversationActive, CreatedAt: now, UpdatedAt: now})
		tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: id, Ordinal: 1, ExecutionStatus: sessionvo.InteractionCompleted, StartIdempotencyKey: id, CreatedAt: now, UpdatedAt: now, LeaseExpiresAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Delete only this test's identities, never truncate or reuse business rows.
		_, _ = db.Exec("DELETE FROM bkn_trace_record_integrity_retries WHERE interaction_id=?", id)
		_, _ = db.Exec("DELETE FROM bkn_trace_interactions WHERE interaction_id=?", id)
		_, _ = db.Exec("DELETE FROM bkn_trace_conversations WHERE conversation_id=?", id)
	})
	var version uint64
	if err = db.QueryRowContext(ctx, "SELECT record_integrity_version FROM bkn_trace_interactions WHERE interaction_id=?", id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	probe := &retryInspectionProbe{Store: store, fail: true}
	run := func(p *retryInspectionProbe) error {
		svc := evidencesvc.New(evidencestore.New(), evidencesvc.WithSessionStore(p), evidencesvc.WithCurrentRecordIntegrity())
		passCtx, stop := context.WithCancel(ctx)
		defer stop()
		pass := &singleIntegrityPass{service: svc, cancel: stop}
		runRecordIntegritySupervisor(passCtx, pass, time.Millisecond)
		return pass.err
	}
	if err = run(probe); err == nil || probe.reads != 1 {
		t.Fatalf("first failure: reads=%d err=%v", probe.reads, err)
	}
	state, found, err := store.GetRecordIntegrityRetry(ctx, id, version)
	if err != nil || !found || state.Status != isessionstore.RecordIntegrityRetryWait || state.Attempts != 1 || state.LastFailureReason != "inspection_failed" {
		t.Fatalf("durable failure=%+v found=%v err=%v", state, found, err)
	}
	// A new Store/Service represents another instance or process restart. Its
	// supervisor must honor the database deadline, with no local retry map.
	restarted := &retryInspectionProbe{Store: sqlstore.New(db), fail: true}
	if err = run(restarted); err != nil || restarted.reads != 0 {
		t.Fatalf("restart bypassed backoff: reads=%d err=%v", restarted.reads, err)
	}
	// Make only this fixture due; no sleep and no change to product policy.
	if _, err = db.ExecContext(ctx, "UPDATE bkn_trace_record_integrity_retries SET next_attempt_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 1 SECOND) WHERE interaction_id=? AND source_version=?", id, version); err != nil {
		t.Fatal(err)
	}
	restarted.fail = false
	if err = run(restarted); err != nil || restarted.reads != 1 {
		t.Fatalf("recovery: reads=%d err=%v", restarted.reads, err)
	}
	state, found, err = store.GetRecordIntegrityRetry(ctx, id, version)
	if err != nil || !found || state.Status != isessionstore.RecordIntegritySucceeded || state.Attempts != 2 {
		t.Fatalf("durable recovery=%+v err=%v", state, err)
	}
	var recovered sessionvo.Interaction
	err = store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { recovered, found = tx.PeekInteraction(id); return nil })
	if err != nil || !found || recovered.StoredRecordIntegrity == nil || recovered.StoredRecordIntegrity.Applicable {
		t.Fatalf("no-call verdict must remain non-applicable: %+v found=%v err=%v", recovered.StoredRecordIntegrity, found, err)
	}
	// Source changes start a fresh generation without deleting prior history.
	if err = store.InvalidateRecordIntegrity(ctx, id, owner); err != nil {
		t.Fatal(err)
	}
	if err = run(restarted); err != nil {
		t.Fatal(err)
	}
	old, _, err := store.GetRecordIntegrityRetry(ctx, id, version)
	current, found, readErr := store.GetRecordIntegrityRetry(ctx, id, version+1)
	if err != nil || readErr != nil || !found || old.Status != isessionstore.RecordIntegritySucceeded || old.Attempts != 2 || current.Status != isessionstore.RecordIntegritySucceeded || current.Attempts != 1 {
		t.Fatalf("generation history: old=%+v new=%+v err=%v/%v", old, current, err, readErr)
	}
	// Fixed inspection budgets stop once; a restarted worker must not requeue
	// that generation or turn the unknown result into a saved verdict.
	if err = store.InvalidateRecordIntegrity(ctx, id, owner); err != nil {
		t.Fatal(err)
	}
	restarted.failure = fmt.Errorf("bounded artifact read: %w", iartifactstore.ErrCaptureContentReadBudget)
	if err = run(restarted); !errors.Is(err, evidencesvc.ErrRecordIntegrityInspectionDeferred) {
		t.Fatalf("budget inspection error: %v", err)
	}
	budgetVersion := version + 2
	state, found, err = store.GetRecordIntegrityRetry(ctx, id, budgetVersion)
	if err != nil || !found || state.Status != isessionstore.RecordIntegrityExhausted || state.Attempts != 1 || state.Reason != isessionstore.RecordIntegrityBudgetExceeded || state.LastFailureReason != isessionstore.RecordIntegrityBudgetExceeded {
		t.Fatalf("budget state=%+v err=%v", state, err)
	}
	if err = store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { recovered, found = tx.PeekInteraction(id); return nil }); err != nil || !found || recovered.StoredRecordIntegrity != nil {
		t.Fatalf("budget verdict must remain unknown: %+v %v", recovered.StoredRecordIntegrity, err)
	}
	afterBudget := &retryInspectionProbe{Store: sqlstore.New(db), failure: iartifactstore.ErrCaptureContentReadBudget}
	if err = run(afterBudget); err != nil || afterBudget.reads != 0 {
		t.Fatalf("restart retried fixed budget: reads=%d err=%v", afterBudget.reads, err)
	}
	if err = store.InvalidateRecordIntegrity(ctx, id, owner); err != nil {
		t.Fatal(err)
	}
	afterBudget.failure = nil
	if err = run(afterBudget); err != nil || afterBudget.reads != 1 {
		t.Fatalf("new generation did not resume: reads=%d err=%v", afterBudget.reads, err)
	}
	current, found, err = store.GetRecordIntegrityRetry(ctx, id, budgetVersion+1)
	if err != nil || !found || current.Status != isessionstore.RecordIntegritySucceeded || current.Attempts != 1 {
		t.Fatalf("budget recovery=%+v err=%v", current, err)
	}

	t.Log("supervisor → MariaDB: failure persisted; restart made 0 premature inspections; retry succeeded; source requeue retained old generation")
}
