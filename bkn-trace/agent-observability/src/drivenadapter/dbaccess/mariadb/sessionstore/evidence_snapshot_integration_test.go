// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// The barrier wraps the actual MySQL driver only in this test. It commits a
// separate writer after the first snapshot SELECT; no timing sleeps or product
// test hooks are needed to reproduce a torn read under READ COMMITTED.
type snapshotBarrierConnector struct {
	driver.Connector
	afterInteraction func()
	once             sync.Once
	options          driver.TxOptions
}

func (c *snapshotBarrierConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &snapshotBarrierConn{Conn: conn, owner: c}, nil
}

type snapshotBarrierConn struct {
	driver.Conn
	owner *snapshotBarrierConnector
}

func (c *snapshotBarrierConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.owner.options = opts
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *snapshotBarrierConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	if strings.Contains(query, "FROM bkn_trace_interactions") {
		return &snapshotBarrierRows{Rows: rows, after: func() { c.owner.once.Do(c.owner.afterInteraction) }}, nil
	}
	return rows, nil
}

type snapshotBarrierRows struct {
	driver.Rows
	after func()
}

func (r *snapshotBarrierRows) Close() error { err := r.Rows.Close(); r.after(); return err }

func TestMariaDBEvidenceSnapshotConcurrentFinalize(t *testing.T) {
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
	defer db.Close()
	store := sessionstore.New(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := sessionvo.Owner{ApplicationPrincipalID: "snapshot-test", EffectiveSubjectType: sessionvo.SubjectService, EffectiveSubjectID: "snapshot-test"}
	service := sessionsvc.New(store, sessionsvc.Options{})
	conv, err := service.EnsureCurrentConversation(ctx, sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: fmt.Sprintf("snapshot-%d", time.Now().UnixNano()), IdempotencyKey: "ensure"})
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := service.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conv.ID, IdempotencyKey: "start"})
	if err != nil {
		t.Fatal(err)
	}
	op, receipt, err := service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{
		Owner: owner, ConversationID: conv.ID, InteractionID: interaction.ID, OperationKey: "query", ToolName: "query",
		Input:    sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, MediaType: "application/json", Inline: []byte(`{"value":1}`)},
		Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch})
	if err != nil {
		t.Fatal(err)
	}
	empty, found, err := store.ReadEvidenceSnapshot(ctx, interaction.ID)
	if err != nil || !found || empty.Ledger == nil || len(empty.Ledger.Events) != 0 {
		t.Fatalf("empty ledger must be distinguished from not read: found=%v err=%v", found, err)
	}
	payload := json.RawMessage(`{"quantity":9007199254740993,"decimal":1.2300,"result_artifact_ref":"body-not-collected"}`)
	now := time.Now().UTC()
	event := ledgervo.Event{EventID: "event-" + interaction.ID, EventType: "operation.output.observed", SchemaVersion: "3.0.0",
		PayloadHash: ledgervo.CanonicalPayloadHash(payload), Owner: owner, ConversationID: conv.ID, InteractionID: interaction.ID,
		OperationID: op.ID, Attempt: 1, ProducerID: "snapshot-test", ProducerStreamID: "stream-" + interaction.ID,
		ProducerEpoch: 1, ProducerSequence: 1, StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: payload,
		ArtifactRefs: []string{"body-not-collected"}}
	ledger := ledgersvc.New(store)
	ack, err := ledger.Ingest(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	var storedEnvelope []byte
	if err := db.QueryRowContext(ctx, "SELECT envelope FROM bkn_trace_evidence_event_ledger WHERE event_id=?", event.EventID).Scan(&storedEnvelope); err != nil {
		t.Fatal(err)
	}
	before, found, err := store.ReadEvidenceSnapshot(ctx, interaction.ID)
	if err != nil || !found {
		t.Fatalf("initial read found=%v err=%v", found, err)
	}
	if before.Ledger == nil || len(before.Ledger.Events) != 1 {
		t.Fatal("recorded ledger event missing")
	}
	captured := before.Ledger.Events[0]
	if captured.EventID != event.EventID || captured.IngestSequence != ack.IngestSequence || !bytes.Equal(captured.Envelope, storedEnvelope) ||
		!bytes.Contains(captured.Envelope, []byte("9007199254740993")) || !bytes.Contains(captured.Envelope, []byte("1.2300")) {
		t.Fatal("ledger identity, raw precision or byte representation changed")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Keep parameterized reads on QueryContext so the barrier also observes the
	// first SELECT instead of the driver's prepared-statement fallback.
	config.InterpolateParams = true
	base, err := mysql.NewConnector(config)
	if err != nil {
		t.Fatal(err)
	}
	var writerErr error
	writerRan := false
	barrier := &snapshotBarrierConnector{Connector: base}
	barrier.afterInteraction = func() {
		writerRan = true
		writeCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		writerErr = store.WithinTransaction(writeCtx, func(tx isessionstore.Transaction) error {
			current, _ := tx.FindInteraction(interaction.ID)
			current.RowVersion++
			tx.SaveInteraction(current)
			currentOp, _ := tx.FindOperation(op.ID)
			currentOp.Attempt = 2
			currentOp.RowVersion++
			tx.SaveOperation(currentOp)
			later := receipt
			later.ID = "late-" + receipt.ID
			later.Attempt = 2
			tx.SaveReceipt(later)
			return nil
		})
	}
	writeSession := barrier.afterInteraction
	barrier.afterInteraction = func() {
		writeSession()
		if writerErr != nil {
			return
		}
		late := event
		late.EventID = "late-" + event.EventID
		late.ProducerSequence = 2
		late.Attempt = 2
		writeCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		_, writerErr = ledger.Ingest(writeCtx, late)
	}
	readDB := sql.OpenDB(barrier)
	defer readDB.Close()
	during, found, err := sessionstore.New(readDB).ReadEvidenceSnapshot(ctx, interaction.ID)
	if !writerRan {
		t.Fatal("concurrent writer barrier did not run")
	}
	if writerErr != nil {
		t.Fatalf("writer blocked or failed: %v", writerErr)
	}
	if err != nil || !found {
		t.Fatalf("capture found=%v err=%v", found, err)
	}
	if during.Interaction.RowVersion != before.Interaction.RowVersion || during.Operations[0].Attempt != 1 || len(during.Receipts) != 1 {
		t.Fatalf("torn snapshot: interaction=%d operation attempt=%d receipts=%d", during.Interaction.RowVersion, during.Operations[0].Attempt, len(during.Receipts))
	}
	if during.Ledger == nil || len(during.Ledger.Events) != 1 || during.Ledger.Events[0].EventID != event.EventID {
		t.Fatal("late event leaked into earlier session snapshot")
	}
	if !barrier.options.ReadOnly || barrier.options.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) {
		t.Fatal("incorrect read transaction options")
	}
	after, _, err := store.ReadEvidenceSnapshot(ctx, interaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Interaction.RowVersion != before.Interaction.RowVersion+1 || after.Operations[0].Attempt != 2 || len(after.Receipts) != 2 || len(after.CallFacts) != len(before.CallFacts) {
		t.Fatal("new snapshot did not retain the independent retry receipt")
	}
	if after.Ledger == nil || len(after.Ledger.Events) != 2 || after.Ledger.Events[1].EventID != "late-"+event.EventID || after.Ledger.Events[1].IngestSequence <= ack.IngestSequence {
		t.Fatal("later snapshot must contain late event in ingest order")
	}
	before.Ledger.Events[0].Envelope[0] = '!'
	detached, _, err := store.ReadEvidenceSnapshot(ctx, interaction.ID)
	if err != nil || !bytes.Equal(detached.Ledger.Events[0].Envelope, storedEnvelope) {
		t.Fatal("caller mutation changed a later ledger read")
	}
	_, found, err = store.ReadEvidenceSnapshot(ctx, "not-present")
	if err != nil || found {
		t.Fatal("missing interaction synthesized")
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	_, found, err = store.ReadEvidenceSnapshot(stopped, interaction.ID)
	if found || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled read returned a snapshot")
	}
}
