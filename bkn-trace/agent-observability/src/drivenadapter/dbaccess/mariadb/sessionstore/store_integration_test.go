//go:build integration

package sessionstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectionrebuildsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func TestSourceCoverageSurvivesStoreRestart(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	coverage := observabilityvo.SourceCoverage{
		SourceID: "otel-runtime-test", DeploymentID: "cluster-test",
		State: observabilityvo.SourceCoverageDegraded, Reason: "telemetry_dropped",
		DroppedRecords: 3, FirstObservedAt: time.Now().UTC(), LastObservedAt: time.Now().UTC(),
	}
	if err := store.UpsertDegraded(context.Background(), coverage); err != nil {
		t.Fatalf("upsert coverage: %v", err)
	}
	restarted := sessionstore.New(db)
	stored, found, err := restarted.Get(context.Background(), coverage.SourceID, coverage.DeploymentID)
	if err != nil || !found || stored.State != observabilityvo.SourceCoverageDegraded || stored.DroppedRecords != 3 {
		t.Fatalf("coverage was not durable after restart: coverage=%+v found=%t err=%v", stored, found, err)
	}
	if err := restarted.MarkHealthyAfterCatchUp(context.Background(), coverage.SourceID, coverage.DeploymentID, stored.Version); err != nil {
		t.Fatalf("recover coverage: %v", err)
	}
	recovered, found, err := restarted.Get(context.Background(), coverage.SourceID, coverage.DeploymentID)
	if err != nil || !found || recovered.State != observabilityvo.SourceCoverageHealthy || recovered.RecoveredAt == nil || recovered.DroppedRecords != 3 {
		t.Fatalf("coverage recovery lost audit state: coverage=%+v found=%t err=%v", recovered, found, err)
	}
}

func TestConcurrentEnsureCurrentHasOneGeneration(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := sessionvo.Owner{
		TenantID: "tenant-race", BusinessDomainID: "domain-race",
		ApplicationPrincipalID: "app-race", EffectiveSubjectType: sessionvo.SubjectService,
		EffectiveSubjectID: "subject-race",
	}

	const workers = 12
	results := make(chan sessionvo.Conversation, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conversation, ensureErr := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
				Owner: owner, ExternalConversationKey: "concurrent-key", IdempotencyKey: "ensure",
			})
			results <- conversation
			errs <- ensureErr
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for ensureErr := range errs {
		if ensureErr != nil {
			t.Fatalf("concurrent ensure: %v", ensureErr)
		}
	}
	var expected string
	for conversation := range results {
		if expected == "" {
			expected = conversation.ID
		}
		if conversation.ID != expected || conversation.Generation != 1 {
			t.Fatalf("expected one generation, got %#v", conversation)
		}
	}
}

func TestIdleOneShotExpirationAndInteractionStartCommitOneWinner(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := sessionvo.Owner{
		TenantID: "tenant-one-shot", BusinessDomainID: "domain-one-shot",
		ApplicationPrincipalID: "app-one-shot", EffectiveSubjectType: sessionvo.SubjectService,
		EffectiveSubjectID: "subject-one-shot",
	}

	for iteration := 0; iteration < 10; iteration++ {
		suffix := fmt.Sprintf("%d-%d", time.Now().UnixNano(), iteration)
		conversation, ensureErr := service.EnsureCurrentConversation(
			context.Background(),
			sessionsvc.EnsureConversationCommand{
				Owner: owner, ExternalConversationKey: "idle-race-" + suffix, OneShot: true,
			},
		)
		if ensureErr != nil {
			t.Fatalf("ensure one-shot conversation: %v", ensureErr)
		}

		started := make(chan struct{})
		var startErr, expireErr error
		var expired []sessionvo.Conversation
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-started
			_, startErr = service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
				Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start-" + suffix,
			})
		}()
		go func() {
			defer wg.Done()
			<-started
			expired, expireErr = service.ExpireIdleOneShotConversations(
				context.Background(), time.Nanosecond, 1000,
			)
		}()
		close(started)
		wg.Wait()
		if expireErr != nil {
			t.Fatalf("expire one-shot conversation: %v", expireErr)
		}

		current, getErr := service.GetConversation(context.Background(), owner, conversation.ID)
		if getErr != nil {
			t.Fatalf("get one-shot conversation: %v", getErr)
		}
		targetExpired := false
		for _, item := range expired {
			if item.ID == conversation.ID {
				targetExpired = true
				break
			}
		}
		switch current.Status {
		case sessionvo.ConversationActive:
			if startErr != nil || targetExpired {
				t.Fatalf("active winner was inconsistent: start=%v expired=%t", startErr, targetExpired)
			}
		case sessionvo.ConversationExpired:
			if !sessionsvc.IsCode(startErr, sessionsvc.CodeConversationExpired) || !targetExpired {
				t.Fatalf("expiry winner was inconsistent: start=%v expired=%t", startErr, targetExpired)
			}
		default:
			t.Fatalf("unexpected conversation status after race: %s", current.Status)
		}
	}
}

func TestEvidenceLedgerAndProjectionOutboxCommitAtomically(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sessions := sessionsvc.New(store, sessionsvc.Options{})
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := sessionvo.Owner{
		TenantID: "tenant-ledger", BusinessDomainID: "domain-ledger",
		ApplicationPrincipalID: "app-ledger", EffectiveSubjectType: sessionvo.SubjectService,
		EffectiveSubjectID: "subject-ledger",
	}
	conversation, err := sessions.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "ledger-" + suffix, IdempotencyKey: "ensure",
	})
	if err != nil {
		t.Fatalf("ensure conversation: %v", err)
	}
	interaction, err := sessions.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, _, err := sessions.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationKey: "query", ToolName: "ontology-query",
		NormalizedInputHash: "sha256:input", Required: true,
		LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
	})
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	envelope := json.RawMessage(`{"result":"durable"}`)
	event := ledgervo.Event{
		EventID: "evt-" + suffix, EventType: "operation.output.observed", SchemaVersion: "3.0.0",
		PayloadHash: ledgervo.CanonicalPayloadHash(envelope), Owner: owner,
		ConversationID: conversation.ID, InteractionID: interaction.ID,
		OperationID: operation.ID, Attempt: 1, ProducerID: "integration-test",
		ProducerStreamID: "stream-" + suffix, ProducerEpoch: 1, ProducerSequence: 1,
		StartedAt: time.Now().UTC(), ObservedAt: time.Now().UTC(),
		EmittedAt: time.Now().UTC(), Envelope: envelope,
	}
	ledger := ledgersvc.New(store)
	ack, err := ledger.Ingest(context.Background(), event)
	if err != nil || !ack.Durable {
		t.Fatalf("ingest durable event: %#v, %v", ack, err)
	}
	replay, err := ledger.Ingest(context.Background(), event)
	if err != nil || !replay.Replayed || replay.IngestSequence != ack.IngestSequence {
		t.Fatalf("replay durable event: %#v, %v", replay, err)
	}
	var ledgerCount, outboxCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM bkn_trace_evidence_event_ledger WHERE event_id=?", event.EventID).Scan(&ledgerCount); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE event_id=? AND event_type='evidence.project'", event.EventID).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if ledgerCount != 1 || outboxCount != 1 {
		t.Fatalf("expected one ledger and outbox row, got %d and %d", ledgerCount, outboxCount)
	}

	firstCycleEvent := event
	firstCycleEvent.EventID = "evt-cycle-a-" + suffix
	firstCycleEvent.ProducerSequence = 2
	firstCycleEvent.CausationEventIDs = []string{"evt-cycle-b-" + suffix}
	firstCycleEvent.Envelope = json.RawMessage(`{"result":"cycle-a"}`)
	firstCycleEvent.PayloadHash = ledgervo.CanonicalPayloadHash(firstCycleEvent.Envelope)
	if _, err := ledger.Ingest(context.Background(), firstCycleEvent); err != nil {
		t.Fatalf("ingest event with late cause: %v", err)
	}
	secondCycleEvent := event
	secondCycleEvent.EventID = "evt-cycle-b-" + suffix
	secondCycleEvent.ProducerSequence = 3
	secondCycleEvent.CausationEventIDs = []string{firstCycleEvent.EventID}
	secondCycleEvent.Envelope = json.RawMessage(`{"result":"cycle-b"}`)
	secondCycleEvent.PayloadHash = ledgervo.CanonicalPayloadHash(secondCycleEvent.Envelope)
	if _, err := ledger.Ingest(context.Background(), secondCycleEvent); !ledgersvc.IsCode(err, ledgersvc.CodeInvalidEvent) {
		t.Fatalf("MariaDB ledger accepted a causation cycle: %v", err)
	}
}

func TestTerminalRaceCommitsOneWinner(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	service := sessionsvc.New(store, sessionsvc.Options{})
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := sessionvo.Owner{
		TenantID: "tenant-terminal", BusinessDomainID: "domain-terminal",
		ApplicationPrincipalID: "app-terminal", EffectiveSubjectType: sessionvo.SubjectService,
		EffectiveSubjectID: "subject-terminal",
	}
	conversation, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "terminal-" + suffix, IdempotencyKey: "ensure",
	})
	if err != nil {
		t.Fatalf("ensure conversation: %v", err)
	}
	interaction, err := service.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	statuses := []sessionvo.InteractionStatus{
		sessionvo.InteractionCompleted, sessionvo.InteractionFailed,
		sessionvo.InteractionCanceled, sessionvo.InteractionHandedOff,
	}
	errs := make(chan error, len(statuses))
	var wg sync.WaitGroup
	for index, status := range statuses {
		wg.Add(1)
		go func(index int, status sessionvo.InteractionStatus) {
			defer wg.Done()
			_, terminateErr := service.TerminateInteraction(context.Background(), sessionsvc.TerminateInteractionCommand{
				Owner: owner, InteractionID: interaction.ID, Status: status,
				TerminalIdempotencyKey: fmt.Sprintf("terminal-%d", index),
				LeaseToken:             interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
				Manifest: sessionvo.ClosureManifest{
					Version: "1", CompletionReason: "race",
				},
			})
			errs <- terminateErr
		}(index, status)
	}
	wg.Wait()
	close(errs)
	var successes, conflicts int
	for terminateErr := range errs {
		switch {
		case terminateErr == nil:
			successes++
		case sessionsvc.IsCode(terminateErr, sessionsvc.CodeTerminalConflict):
			conflicts++
		default:
			t.Fatalf("unexpected terminal race error: %v", terminateErr)
		}
	}
	if successes != 1 || conflicts != len(statuses)-1 {
		t.Fatalf("expected one terminal winner, successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestExpiredLeaseReaperWinsAgainstTerminalRequest(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	service := sessionsvc.New(store, sessionsvc.Options{})
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := sessionvo.Owner{
		TenantID: "tenant-reaper", BusinessDomainID: "domain-reaper",
		ApplicationPrincipalID: "app-reaper", EffectiveSubjectType: sessionvo.SubjectService,
		EffectiveSubjectID: "subject-reaper",
	}
	conversation, err := service.EnsureCurrentConversation(
		context.Background(),
		sessionsvc.EnsureConversationCommand{
			Owner: owner, ExternalConversationKey: "reaper-" + suffix,
			IdempotencyKey: "ensure",
		},
	)
	if err != nil {
		t.Fatalf("ensure conversation: %v", err)
	}
	interaction, err := service.StartInteraction(
		context.Background(),
		sessionsvc.StartInteractionCommand{
			Owner: owner, ConversationID: conversation.ID,
			IdempotencyKey: "start", LeaseDuration: time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE bkn_trace_interactions
		SET lease_expires_at = UTC_TIMESTAMP(6) - INTERVAL 1 SECOND
		WHERE interaction_id = ?`, interaction.ID); err != nil {
		t.Fatalf("expire interaction lease: %v", err)
	}

	var wg sync.WaitGroup
	terminalErr := make(chan error, 1)
	reaperResult := make(chan struct {
		interactions []sessionvo.Interaction
		err          error
	}, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := service.TerminateInteraction(
			context.Background(),
			sessionsvc.TerminateInteractionCommand{
				Owner: owner, InteractionID: interaction.ID,
				Status:                 sessionvo.InteractionCompleted,
				TerminalIdempotencyKey: "terminal",
				LeaseToken:             interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
				Manifest: sessionvo.ClosureManifest{
					Version: "1", CompletionReason: "race",
				},
			},
		)
		terminalErr <- err
	}()
	go func() {
		defer wg.Done()
		interactions, err := service.AbandonExpiredInteractions(context.Background(), 100)
		reaperResult <- struct {
			interactions []sessionvo.Interaction
			err          error
		}{interactions: interactions, err: err}
	}()
	wg.Wait()
	result := <-reaperResult
	if result.err != nil {
		t.Fatalf("lease reaper: %v", result.err)
	}
	if len(result.interactions) != 1 || result.interactions[0].ID != interaction.ID ||
		result.interactions[0].ExecutionStatus != sessionvo.InteractionAbandoned {
		t.Fatalf("lease reaper did not abandon the expired interaction: %#v", result.interactions)
	}
	if err := <-terminalErr; !sessionsvc.IsCode(err, sessionsvc.CodeTerminalConflict) {
		t.Fatalf("terminal request against expired lease = %v, want terminal conflict", err)
	}

	current, err := service.GetInteraction(context.Background(), owner, interaction.ID)
	if err != nil {
		t.Fatalf("get interaction: %v", err)
	}
	if current.ExecutionStatus != sessionvo.InteractionAbandoned {
		t.Fatalf("expired lease was not abandoned: %#v", current)
	}
	var terminalProjectionCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM bkn_trace_projection_outbox
		WHERE aggregate_type='interaction' AND aggregate_id=?
		  AND event_type IN ('interaction.completed', 'interaction.abandoned')`,
		interaction.ID,
	).Scan(&terminalProjectionCount); err != nil {
		t.Fatalf("count terminal projection: %v", err)
	}
	if terminalProjectionCount != 1 {
		t.Fatalf("expected one abandoned projection, got %d", terminalProjectionCount)
	}
}

func TestProjectionOutboxRejectsStaleLeaseCompletion(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	service := sessionsvc.New(store, sessionsvc.Options{})
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := sessionvo.Owner{
		TenantID: "tenant-outbox", BusinessDomainID: "domain-outbox",
		ApplicationPrincipalID: "app-outbox", EffectiveSubjectType: sessionvo.SubjectService,
		EffectiveSubjectID: "subject-outbox",
	}
	if _, err := service.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "outbox-" + suffix, IdempotencyKey: "ensure",
	}); err != nil {
		t.Fatalf("ensure conversation: %v", err)
	}
	firstLease, err := store.Lease(context.Background(), 1, time.Millisecond)
	if err != nil || len(firstLease) != 1 {
		t.Fatalf("first lease: %#v, %v", firstLease, err)
	}
	time.Sleep(5 * time.Millisecond)
	secondLease, err := store.Lease(context.Background(), 1, time.Minute)
	if err != nil || len(secondLease) != 1 || firstLease[0].ID != secondLease[0].ID {
		t.Fatalf("second lease: %#v, %v", secondLease, err)
	}
	if err := store.MarkDelivered(context.Background(), firstLease[0]); !errors.Is(err, iprojectionoutbox.ErrLeaseLost) {
		t.Fatalf("stale lease unexpectedly completed outbox: %v", err)
	}
	if err := store.MarkDelivered(context.Background(), secondLease[0]); err != nil {
		t.Fatalf("current lease could not complete outbox: %v", err)
	}
	if err := store.MoveToDLQ(context.Background(), firstLease[0], "late_failure"); !errors.Is(err, iprojectionoutbox.ErrLeaseLost) {
		t.Fatalf("stale lease unexpectedly created DLQ: %v", err)
	}
	var dlqCount int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM bkn_trace_dlq WHERE source_kind='projection' AND source_id=?",
		firstLease[0].EventID,
	).Scan(&dlqCount); err != nil {
		t.Fatalf("count DLQ: %v", err)
	}
	if dlqCount != 0 {
		t.Fatalf("stale lease created %d false DLQ rows", dlqCount)
	}
}

func TestProjectionDLQReplayIsAuditedAndReturnsEventToOutbox(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	eventID := "evt-dlq-" + suffix
	result, err := db.Exec(`
		INSERT INTO bkn_trace_projection_outbox (
			aggregate_type, aggregate_id, event_type, event_id, payload,
			status, attempts, available_at, created_at
		) VALUES ('evidence', ?, 'evidence.project', ?, '{}',
			'pending', 0, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`,
		eventID, eventID,
	)
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}
	outboxID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("outbox ID: %v", err)
	}
	leaseToken := "lease-" + suffix
	if _, err := db.Exec(`
		UPDATE bkn_trace_projection_outbox
		SET status='processing', lease_token=?,
			locked_until=DATE_ADD(UTC_TIMESTAMP(6), INTERVAL 1 MINUTE)
		WHERE outbox_id=?`,
		leaseToken, outboxID,
	); err != nil {
		t.Fatalf("lease inserted outbox: %v", err)
	}
	item := iprojectionoutbox.Item{
		ID: uint64(outboxID), EventID: eventID, Payload: []byte(`{}`),
		LeaseToken: leaseToken,
	}
	if err := store.MoveToDLQ(context.Background(), item, "mapping_invalid"); err != nil {
		t.Fatalf("move to DLQ: %v", err)
	}

	var dlqID uint64
	if err := db.QueryRow(`
		SELECT dlq_id FROM bkn_trace_dlq
		WHERE source_kind='projection' AND source_id=?`, eventID,
	).Scan(&dlqID); err != nil {
		t.Fatalf("find DLQ row: %v", err)
	}
	if err := store.ReplayProjectionDLQ(
		context.Background(),
		iprojectionoutbox.ReplayRequest{
			DLQID: dlqID, Operator: "sre@example.com",
			Reason: "mapping fixed", RepairVersion: "bkn-trace-core-v20260730.2",
		},
	); err != nil {
		t.Fatalf("replay DLQ: %v", err)
	}

	var status string
	var attempts uint32
	if err := db.QueryRow(`
		SELECT status, attempts FROM bkn_trace_projection_outbox
		WHERE outbox_id=?`, outboxID,
	).Scan(&status, &attempts); err != nil {
		t.Fatalf("query replayed outbox: %v", err)
	}
	if status != "pending" || attempts != 0 {
		t.Fatalf("replayed outbox state = %q attempts=%d", status, attempts)
	}
	var auditCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM bkn_trace_dlq_replay_audit
		WHERE dlq_id=? AND replayed_by='sre@example.com'
		  AND reason='mapping fixed'
		  AND repair_version='bkn-trace-core-v20260730.2'`, dlqID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("query replay audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("expected one DLQ replay audit row, got %d", auditCount)
	}
}

func TestProjectionRebuildFromMariaDBAuthorityAfterOutboxCleanup(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := sessionvo.Owner{
		TenantID: "tenant-rebuild-" + suffix, BusinessDomainID: "domain-rebuild",
		ApplicationPrincipalID: "app-rebuild",
		EffectiveSubjectType:   sessionvo.SubjectService,
		EffectiveSubjectID:     "subject-rebuild",
	}
	conversation, err := sessionsvc.New(store, sessionsvc.Options{}).
		EnsureCurrentConversation(
			context.Background(),
			sessionsvc.EnsureConversationCommand{
				Owner: owner, ExternalConversationKey: "external-" + suffix,
				IdempotencyKey: "ensure-" + suffix,
			},
		)
	if err != nil {
		t.Fatalf("create authoritative conversation: %v", err)
	}
	if _, err := db.Exec(
		"DELETE FROM bkn_trace_projection_outbox WHERE aggregate_type='conversation' AND aggregate_id=?",
		conversation.ID,
	); err != nil {
		t.Fatalf("simulate delivered Outbox retention cleanup: %v", err)
	}

	target := &integrationRebuildTarget{documents: make(map[string][]byte)}
	version := "bkn-trace-core-" + suffix
	result, err := projectionrebuildsvc.New(
		store, target, projectionrebuildsvc.Options{BatchSize: 7},
	).Rebuild(context.Background(), "core", "bkn-trace-core", version)
	if err != nil {
		t.Fatalf("rebuild projection: %v", err)
	}
	if result.LastOutboxID == 0 || result.ProjectedCount == 0 ||
		target.alias != "bkn-trace-core" || target.version != version {
		t.Fatalf("unexpected rebuild result: %#v target=%#v", result, target)
	}
	documentID := "conversation:" + conversation.ID
	if _, found := target.documents[documentID]; !found {
		t.Fatalf("rebuild omitted authority after Outbox cleanup: %s", documentID)
	}
}

func TestAuthorityRebuildMatchesLiveOperationAndReceiptProjectionModels(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := sessionvo.Owner{
		TenantID: "tenant-model-" + suffix, BusinessDomainID: "domain-model",
		ApplicationPrincipalID: "app-model",
		EffectiveSubjectType:   sessionvo.SubjectService, EffectiveSubjectID: "subject-model",
	}
	service := sessionsvc.New(store, sessionsvc.Options{})
	conversation, err := service.EnsureCurrentConversation(
		context.Background(),
		sessionsvc.EnsureConversationCommand{
			Owner: owner, ExternalConversationKey: "model-" + suffix,
			IdempotencyKey: "ensure",
		},
	)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	interaction, err := service.StartInteraction(
		context.Background(),
		sessionsvc.StartInteractionCommand{
			Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start",
		},
	)
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(
		context.Background(),
		sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationKey: "query", ToolName: "ontology-query",
			NormalizedInputHash: "sha256:model", Required: true,
			LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		},
	)
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	live := make(map[string]iprojectionoutbox.Item)
	through, err := store.ProjectionHighWatermark(context.Background())
	if err != nil {
		t.Fatalf("read projection watermark: %v", err)
	}
	history, err := store.ScanProjectionHistory(context.Background(), 0, through, 10000)
	if err != nil {
		t.Fatalf("scan live projection history: %v", err)
	}
	for _, item := range history {
		if item.AggregateID == operation.ID || item.AggregateID == receipt.ID {
			live[iprojectionoutbox.DocumentID(item)] = item
		}
	}
	authority := make(map[string]iprojectionoutbox.Item)
	afterType, afterID := "", ""
	for {
		items, scanErr := store.ScanAuthoritativeProjection(
			context.Background(), afterType, afterID, 100,
		)
		if scanErr != nil {
			t.Fatalf("scan authoritative projection: %v", scanErr)
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			if item.AggregateID == operation.ID || item.AggregateID == receipt.ID {
				authority[iprojectionoutbox.DocumentID(item)] = item
			}
		}
		last := items[len(items)-1]
		afterType, afterID = last.AggregateType, last.AggregateID
	}
	for _, documentID := range []string{
		"operation:" + operation.ID,
		"receipt:" + receipt.ID,
	} {
		liveItem, liveFound := live[documentID]
		authorityItem, authorityFound := authority[documentID]
		if !liveFound || !authorityFound {
			t.Fatalf(
				"projection model missing for %s: live=%t authority=%t",
				documentID, liveFound, authorityFound,
			)
		}
		if liveItem.AggregateVersion != authorityItem.AggregateVersion ||
			!equivalentJSON(liveItem.Payload, authorityItem.Payload) {
			t.Fatalf(
				"live/rebuild projection mismatch for %s:\nlive=%s\nauthority=%s",
				documentID, liveItem.Payload, authorityItem.Payload,
			)
		}
	}
}

func TestMariaDBAuthorityRebuildsIntoOpenSearchAlias(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	endpoint := os.Getenv("BKN_TRACE_TEST_OPENSEARCH_ENDPOINT")
	if dsn == "" || endpoint == "" {
		t.Skip("MariaDB DSN and OpenSearch endpoint are required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MariaDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := sessionvo.Owner{
		TenantID: "tenant-combined-" + suffix, BusinessDomainID: "domain-combined",
		ApplicationPrincipalID: "app-combined",
		EffectiveSubjectType:   sessionvo.SubjectService, EffectiveSubjectID: "subject-combined",
	}
	service := sessionsvc.New(store, sessionsvc.Options{})
	conversation, err := service.EnsureCurrentConversation(
		context.Background(),
		sessionsvc.EnsureConversationCommand{
			Owner: owner, ExternalConversationKey: "combined-" + suffix,
			IdempotencyKey: "ensure",
		},
	)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	interaction, err := service.StartInteraction(
		context.Background(),
		sessionsvc.StartInteractionCommand{
			Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start",
		},
	)
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	operation, receipt, err := service.EnsureOperation(
		context.Background(),
		sessionsvc.EnsureOperationCommand{
			Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationKey: "combined-query", ToolName: "ontology-query",
			NormalizedInputHash: "sha256:combined", Required: true,
			LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch,
		},
	)
	if err != nil {
		t.Fatalf("ensure operation: %v", err)
	}
	username := os.Getenv("BKN_TRACE_TEST_OPENSEARCH_USERNAME")
	client := opensearch.New(endpoint, opensearch.AuthConfig{
		Enabled: username != "", Username: username,
		Password: os.Getenv("BKN_TRACE_TEST_OPENSEARCH_PASSWORD"),
	}, 10*time.Second)
	alias := "bkn-trace-combined-" + suffix
	version := alias + "-v1"
	target := opensearchprojection.New(client, alias)
	if _, err := projectionrebuildsvc.New(
		store, target, projectionrebuildsvc.Options{BatchSize: 100},
	).Rebuild(context.Background(), "combined", alias, version); err != nil {
		t.Fatalf("rebuild MariaDB authority into OpenSearch: %v", err)
	}
	for documentID, expected := range map[string]any{
		"operation:" + operation.ID: operation,
		"receipt:" + receipt.ID:     receipt,
	} {
		document, err := client.GetDocument(context.Background(), alias, documentID)
		if err != nil {
			t.Fatalf("read %s through alias: %v", documentID, err)
		}
		expectedJSON, _ := json.Marshal(expected)
		if !equivalentJSON(document.Source, expectedJSON) {
			t.Fatalf(
				"combined rebuild changed %s contract:\nexpected=%s\nactual=%s",
				documentID, expectedJSON, document.Source,
			)
		}
	}
}

func equivalentJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

type integrationRebuildTarget struct {
	documents map[string][]byte
	alias     string
	version   string
}

func (t *integrationRebuildTarget) PrepareVersion(context.Context, string) error {
	return nil
}

func BenchmarkMariaDBEnsureCurrent(b *testing.B) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		b.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		b.Fatalf("open MariaDB: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		b.Fatalf("migrate: %v", err)
	}
	service := sessionsvc.New(store, sessionsvc.Options{})
	owner := sessionvo.Owner{
		TenantID: "tenant-capacity", BusinessDomainID: "domain-capacity",
		ApplicationPrincipalID: "app-capacity", EffectiveSubjectType: sessionvo.SubjectService,
		EffectiveSubjectID: "subject-capacity",
	}
	prefix := fmt.Sprintf("capacity-%d-", time.Now().UnixNano())
	var sequence atomic.Uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			index := sequence.Add(1)
			if _, err := service.EnsureCurrentConversation(
				context.Background(),
				sessionsvc.EnsureConversationCommand{
					Owner:                   owner,
					ExternalConversationKey: fmt.Sprintf("%s%d", prefix, index),
					IdempotencyKey:          "ensure",
				},
			); err != nil {
				b.Errorf("ensure current: %v", err)
				return
			}
		}
	})
}

func BenchmarkMariaDBEvidenceIngest(b *testing.B) {
	dsn := os.Getenv("BKN_TRACE_TEST_MARIADB_DSN")
	if dsn == "" {
		b.Skip("BKN_TRACE_TEST_MARIADB_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		b.Fatalf("open MariaDB: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(context.Background()); err != nil {
		b.Fatalf("migrate: %v", err)
	}
	sessions := sessionsvc.New(store, sessionsvc.Options{})
	owner := sessionvo.Owner{
		TenantID: "tenant-evidence-capacity", BusinessDomainID: "domain-evidence-capacity",
		ApplicationPrincipalID: "app-evidence-capacity", EffectiveSubjectType: sessionvo.SubjectService,
		EffectiveSubjectID: "subject-evidence-capacity",
	}
	prefix := fmt.Sprintf("evidence-capacity-%d-", time.Now().UnixNano())
	conversation, err := sessions.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: prefix, IdempotencyKey: "ensure",
	})
	if err != nil {
		b.Fatalf("ensure conversation: %v", err)
	}
	interaction, err := sessions.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "start",
	})
	if err != nil {
		b.Fatalf("start interaction: %v", err)
	}
	ledger := ledgersvc.New(store)
	var sequence atomic.Uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			index := sequence.Add(1)
			envelope := json.RawMessage(fmt.Sprintf(`{"sequence":%d}`, index))
			now := time.Now().UTC()
			if _, err := ledger.Ingest(context.Background(), ledgervo.Event{
				EventID:   fmt.Sprintf("%sevent-%d", prefix, index),
				EventType: "data.query.observed", SchemaVersion: "3.0.0",
				PayloadHash: ledgervo.CanonicalPayloadHash(envelope), Owner: owner,
				ConversationID: conversation.ID, InteractionID: interaction.ID,
				ProducerID: "capacity", ProducerStreamID: fmt.Sprintf("%sstream-%d", prefix, index),
				ProducerEpoch: 1, ProducerSequence: 1,
				StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: envelope,
			}); err != nil {
				b.Errorf("ingest evidence: %v", err)
				return
			}
		}
	})
}

func (t *integrationRebuildTarget) ProjectVersion(
	_ context.Context,
	_ string,
	item iprojectionoutbox.Item,
) error {
	t.documents[iprojectionoutbox.DocumentID(item)] = append([]byte(nil), item.Payload...)
	return nil
}

func (t *integrationRebuildTarget) ValidateVersion(
	_ context.Context,
	_ string,
	items []iprojectionoutbox.Item,
) error {
	for _, item := range items {
		documentID := iprojectionoutbox.DocumentID(item)
		if !bytes.Equal(t.documents[documentID], item.Payload) {
			return fmt.Errorf("projection document %s does not match", documentID)
		}
	}
	return nil
}

func (t *integrationRebuildTarget) CountVersion(context.Context, string) (uint64, error) {
	return uint64(len(t.documents)), nil
}

func (t *integrationRebuildTarget) SwitchAlias(
	_ context.Context,
	alias string,
	version string,
) error {
	t.alias = alias
	t.version = version
	return nil
}
