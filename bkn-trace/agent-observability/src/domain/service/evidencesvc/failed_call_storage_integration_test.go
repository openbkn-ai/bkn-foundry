//go:build integration

// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/evidenceconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/segmentio/kafka-go"
)

// Each call gets an isolated schema because the producer's fake Core assigns
// the same conversation, interaction and receipt IDs to independent cases.
func verifyFailedCallMariaDB(t *testing.T, call failedCallContractCall, records []failedCallContractRecord) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := mysql.ParseDSN(os.Getenv("BKN_TRACE_TEST_MARIADB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("failed_call_2072_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE DATABASE `"+name+"`"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Errorf("cleanup database: %v", err)
		}
	})
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sessionstore.New(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := sessionvo.Owner{ApplicationPrincipalID: "client-1", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user-1"}
	service := sessionsvc.New(store, sessionsvc.Options{NewID: func(prefix string) string {
		switch prefix {
		case "conv":
			return "conv-1"
		case "int":
			return "int-1"
		case "op":
			return call.OperationID
		case "rcpt":
			return call.Finish.ReceiptID
		default:
			return prefix + "-2072"
		}
	}})
	conv, err := service.EnsureCurrentConversation(ctx, sessionsvc.EnsureConversationCommand{Owner: owner, ExternalConversationKey: "failed-call", IdempotencyKey: "ensure"})
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := service.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conv.ID, IdempotencyKey: "start"})
	if err != nil {
		t.Fatal(err)
	}
	op, receipt, err := service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{Owner: owner, ConversationID: conv.ID, InteractionID: interaction.ID, OperationKey: call.OperationID, ToolName: call.ToolName, Protocol: sessionvo.ProtocolMCP, SourceModule: "context-loader", Input: call.Input, CapabilityProfile: &call.Profile, Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch})
	if err != nil {
		t.Fatal(err)
	}
	command := sessionsvc.FinishAttemptCommand{Owner: owner, OperationID: op.ID, Attempt: op.Attempt, ReceiptID: receipt.ID, Error: call.Finish.Error, EvidenceDurability: call.Finish.Durability, Retryable: call.Finish.Retryable, RequestID: call.Finish.RequestID, TraceID: call.Finish.TraceID, SpanID: call.Finish.SpanID, BusinessRefs: call.Finish.BusinessRefs, EvidenceExpectation: call.Finish.Expectation, PartialReasons: call.Finish.PartialReasons, ObservedEvidenceRefs: call.Finish.ObservedEvidenceRefs, ArtifactRefs: call.Finish.ArtifactRefs}
	if command.EvidenceDurability == "" || command.TraceID == "" || command.SpanID == "" {
		t.Fatal("producer contract omitted receipt metadata")
	}
	_, failed, err := service.FailOperationAttempt(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if failed.EvidenceDurability != command.EvidenceDurability || failed.RequestID != command.RequestID || failed.TraceID != command.TraceID || !slices.Equal(failed.BusinessRefs, command.BusinessRefs) {
		t.Fatalf("persisted receipt changed producer metadata: durability=%s/%s request=%s/%s trace=%s/%s refs=%+v/%+v", failed.EvidenceDurability, command.EvidenceDurability, failed.RequestID, command.RequestID, failed.TraceID, command.TraceID, failed.BusinessRefs, command.BusinessRefs)
	}
	for _, reason := range command.PartialReasons {
		if !slices.Contains(failed.PartialReasons, reason) {
			t.Fatal("persisted receipt dropped producer partial reason")
		}
	}
	_, replay, err := service.FailOperationAttempt(ctx, command)
	if err != nil || !reflect.DeepEqual(failed, replay) {
		t.Fatalf("persisted failure replay changed: %v", err)
	}
	_, err = service.TerminateInteraction(ctx, sessionsvc.TerminateInteractionCommand{Owner: owner, InteractionID: interaction.ID, Status: sessionvo.InteractionFailed, TerminalIdempotencyKey: "fail", LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch, DeriveManifest: true})
	if err != nil {
		t.Fatal(err)
	}
	check := func() sessionvo.EvidenceSnapshot {
		t.Helper()
		snapshot, found, err := store.ReadEvidenceSnapshot(ctx, interaction.ID)
		if err != nil || !found {
			t.Fatalf("stored snapshot unavailable: %v", err)
		}
		report, err := evaluateRecordIntegrity(snapshot, owner, time.Now(), nil)
		if call.Name == "SQL policy without target" {
			if err == nil || !strings.Contains(err.Error(), "failed-call target context unavailable") {
				t.Fatalf("targetless policy classification relaxed: %+v %v", report, err)
			}
		} else if err != nil || report == nil || report.Status != "complete" {
			t.Fatalf("persisted call integrity: %+v %v", report, err)
		}
		return snapshot
	}
	before := check()
	if len(before.CallFacts) != 1 || before.CallFacts[0].SpanID != command.SpanID || before.CallFacts[0].Error == nil || !reflect.DeepEqual(*before.CallFacts[0].Error, command.Error) {
		t.Fatal("persisted call fact changed original error or span")
	}
	if len(before.Ledger.Events) != 0 {
		t.Fatal("pre-delivery snapshot already has evidence")
	}
	verifyProjection := failedCallProjectionCheck(t, ctx, db, store, name)
	verifyProjection(before)
	processor, err := evidenceconsumer.NewProcessor(failedCallAdmission{}, ledgersvc.New(store), failedCallRejections{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for index, record := range records {
		var wire ledgervo.Event
		if err := json.Unmarshal(record.Value, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.OperationID != call.OperationID {
			continue
		}
		count++
		now := time.Now().UTC()
		original := evidenceconsumer.Record{Topic: evidenceconsumer.Topic, Key: record.Key, Value: record.Value, Headers: record.Headers, Partition: 0, Offset: int64(index), BrokerTime: now, BrokerTimestamp: now.Format(time.RFC3339Nano), ProducerStreamID: wire.ProducerStreamID, ProducerSequence: wire.ProducerSequence}
		if broker := os.Getenv("BKN_TRACE_TEST_KAFKA_BROKERS"); broker != "" {
			for _, message := range failedCallBrokerDeliveries(t, ctx, broker, record) {
				if err := evidenceconsumer.KafkaProcessor(processor)(ctx, message); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			for delivery := 0; delivery < 2; delivery++ {
				if err := processor.Process(ctx, original); err != nil {
					t.Fatal(err)
				}
			}
		}

		var ledgerRows, outboxRows int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_evidence_event_ledger WHERE event_id=?", wire.EventID).Scan(&ledgerRows); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE event_id=? AND event_type='evidence.project'", wire.EventID).Scan(&outboxRows); err != nil {
			t.Fatal(err)
		}
		if ledgerRows != 1 || outboxRows != 1 {
			t.Fatalf("duplicate delivery wrote ledger=%d outbox=%d", ledgerRows, outboxRows)
		}
	}
	after := check()
	verifyProjection(after)
	if len(after.Ledger.Events) != count {
		t.Fatalf("expected %d durable events, got %d", count, len(after.Ledger.Events))
	}
	if !reflect.DeepEqual(before.Receipts, after.Receipts) || !reflect.DeepEqual(before.CallFacts, after.CallFacts) {
		t.Fatal("asynchronous delivery rewrote frozen call or receipt")
	}
	t.Logf("MariaDB: original failure persisted and replayed; %d evidence records delivered twice", count)
}

// The broker must be isolated and its Evidence topic use LogAppendTime, as in
// deployment, and have exactly one partition. Two physical writes exercise duplicate delivery at new offsets.
func failedCallBrokerDeliveries(t *testing.T, ctx context.Context, broker string, record failedCallContractRecord) []kafka.Message {
	t.Helper()
	conn, err := kafka.DialLeader(ctx, "tcp", broker, evidenceconsumer.Topic, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	partitions, err := conn.ReadPartitions(evidenceconsumer.Topic)
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 1 || partitions[0].ID != 0 {
		t.Fatal("failed-call integration topic must have exactly one partition")
	}
	offset, err := conn.ReadLastOffset()
	if err != nil {
		t.Fatal(err)
	}
	writer := &kafka.Writer{Addr: kafka.TCP(broker), Topic: evidenceconsumer.Topic, RequiredAcks: kafka.RequireAll, Balancer: &kafka.LeastBytes{}}
	defer func() { _ = writer.Close() }()
	message := kafka.Message{Key: []byte(record.Key), Value: record.Value}
	for _, header := range record.Headers {
		message.Headers = append(message.Headers, kafka.Header{Key: header.Key, Value: []byte(header.Value)})
	}
	if err := writer.WriteMessages(ctx, message, message); err != nil {
		t.Fatal(err)
	}
	reader := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{broker}, Topic: evidenceconsumer.Topic, Partition: 0, MinBytes: 1, MaxBytes: 10e6})
	defer func() { _ = reader.Close() }()
	if err := reader.SetOffset(offset); err != nil {
		t.Fatal(err)
	}
	result := make([]kafka.Message, 0, 2)
	for i := 0; i < 2; i++ {
		received, err := reader.ReadMessage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if string(received.Value) != string(record.Value) || string(received.Key) != record.Key {
			t.Fatal("Kafka changed original producer bytes")
		}
		if received.Offset != offset+int64(i) || received.Time.IsZero() {
			t.Fatal("missing broker delivery coordinate")
		}
		result = append(result, received)
	}
	return result
}

// Exercise the production outbox worker and verify projected bytes through the
// alias before and after event arrival. The schema and index belong to this case.
func failedCallProjectionCheck(t *testing.T, ctx context.Context, db *sql.DB, store *sessionstore.Store, name string) func(sessionvo.EvidenceSnapshot) {
	t.Helper()
	endpoint := os.Getenv("BKN_TRACE_TEST_OPENSEARCH_ENDPOINT")
	if endpoint == "" {
		return func(sessionvo.EvidenceSnapshot) {}
	}
	client := opensearch.New(endpoint, opensearch.AuthConfig{Enabled: os.Getenv("BKN_TRACE_TEST_OPENSEARCH_USERNAME") != "", Username: os.Getenv("BKN_TRACE_TEST_OPENSEARCH_USERNAME"), Password: os.Getenv("BKN_TRACE_TEST_OPENSEARCH_PASSWORD")}, 10*time.Second)
	alias := "failed-call-" + strings.ReplaceAll(name, "_", "-")
	version := alias + "-v1"
	sink := opensearchprojection.New(client, alias)
	if err := sink.EnsureBootstrap(ctx, version); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		req, err := http.NewRequest(http.MethodDelete, strings.TrimRight(endpoint, "/")+"/"+version, nil)
		if err != nil {
			t.Error(err)
			return
		}
		if user := os.Getenv("BKN_TRACE_TEST_OPENSEARCH_USERNAME"); user != "" {
			req.SetBasicAuth(user, os.Getenv("BKN_TRACE_TEST_OPENSEARCH_PASSWORD"))
		}
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("cleanup OpenSearch index: %d", response.StatusCode)
		}
	})
	worker := projectorsvc.NewWorker(store, sink, projectorsvc.WorkerOptions{})
	return func(snapshot sessionvo.EvidenceSnapshot) {
		t.Helper()
		result, err := worker.Drain(ctx)
		if err != nil || result.Dead != 0 || result.Retried != 0 {
			t.Fatalf("projection worker failed: %+v %v", result, err)
		}
		var pending int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_projection_outbox WHERE status <> 'delivered'").Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending != 0 {
			t.Fatalf("undelivered outbox records: %d", pending)
		}
		again, err := worker.RunOnce(ctx)
		if err != nil || again.Leased != 0 {
			t.Fatalf("delivered projection leased again: %+v %v", again, err)
		}
		compare := func(id string, expected any) {
			t.Helper()
			document, err := client.GetDocument(ctx, alias, id)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			var actualValue, expectedValue any
			if err := json.Unmarshal(document.Source, &actualValue); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &expectedValue); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actualValue, expectedValue) {
				t.Fatalf("projected %s differs from MariaDB snapshot", id)
			}
		}
		for _, receipt := range snapshot.Receipts {
			compare("receipt:"+receipt.ID, receipt)
		}
		for _, row := range snapshot.Ledger.Events {
			var event ledgervo.Event
			if err := json.Unmarshal(row.Envelope, &event); err != nil {
				t.Fatal(err)
			}
			compare("evidence_event:"+event.EventID, event)
		}
		t.Logf("OpenSearch: drained %d outbox records; receipt and %d events match MariaDB", result.Delivered, len(snapshot.Ledger.Events))
	}
}
