// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package boot

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/evidenceconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/kafkaruntime"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/coremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceadmission"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencemigration"
	kafka "github.com/segmentio/kafka-go"
)

func TestNewEvidenceLedgerServiceWrapsCoreStoreForKafka(t *testing.T) {
	store := ledgerstore.New()
	service := newEvidenceLedgerService(store, coremetrics.New())
	if service == nil {
		t.Fatal("Evidence ledger service is nil")
	}
	var consumerLedger evidenceconsumer.Ledger = service
	if consumerLedger == nil {
		t.Fatal("Evidence ledger service cannot serve the Kafka consumer")
	}
}

type bootstrapEvidenceAdmission struct{}

func (bootstrapEvidenceAdmission) Lookup(context.Context, uint64, string) (ievidenceadmission.Snapshot, error) {
	return ievidenceadmission.Snapshot{}, nil
}

type bootstrapEvidenceMigration struct {
	result ievidencemigration.ConsumerResult
	called bool
	err    error
}

func (m *bootstrapEvidenceMigration) LookupAdmission(context.Context, string, string) (ievidencemigration.Admission, bool, error) {
	return ievidencemigration.Admission{
		ManifestID: "manifest-1", State: ievidencemigration.ManifestActive, EntryID: "entry-1",
		EventID: "event-1", PayloadHash: "hash-1", Classification: "publish",
	}, true, nil
}

func (m *bootstrapEvidenceMigration) RecordConsumerResult(_ context.Context, result ievidencemigration.ConsumerResult) error {
	m.result, m.called = result, true
	return m.err
}

type bootstrapEvidenceLedger struct{}

func (bootstrapEvidenceLedger) IngestKafka(context.Context, ledgervo.Event, ievidenceledger.KafkaCoordinate) (ievidenceledger.KafkaResult, error) {
	return ievidenceledger.KafkaResult{Decision: ievidenceledger.KafkaConflict, ReasonCode: "event_id_conflict"}, nil
}

type bootstrapEvidenceRejections struct{}

func (bootstrapEvidenceRejections) RecordKafkaRejection(context.Context, ievidenceadmission.Record, ievidenceadmission.RejectionDetails) error {
	return nil
}

type bootstrapEvidenceReader struct {
	messages chan kafka.Message
	commits  chan kafka.Message
	closed   chan struct{}
	once     sync.Once
}

func newBootstrapEvidenceReader() *bootstrapEvidenceReader {
	return &bootstrapEvidenceReader{messages: make(chan kafka.Message, 1), commits: make(chan kafka.Message, 1), closed: make(chan struct{})}
}

func (r *bootstrapEvidenceReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	select {
	case message := <-r.messages:
		return message, nil
	case <-r.closed:
		return kafka.Message{}, errors.New("reader closed")
	case <-ctx.Done():
		return kafka.Message{}, ctx.Err()
	}
}

func (r *bootstrapEvidenceReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	for _, message := range messages {
		r.commits <- message
	}
	return nil
}

func (r *bootstrapEvidenceReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func TestEvidenceKafkaProcessorUsesMigrationStoreForTerminalResult(t *testing.T) {
	migration := &bootstrapEvidenceMigration{}
	reader := newBootstrapEvidenceReader()
	startEvidenceKafkaRuntime(t, migration, reader)
	now := time.Date(2026, 9, 25, 8, 30, 0, 0, time.UTC)
	message := migrationMessage(now)
	reader.messages <- message
	select {
	case committed := <-reader.commits:
		if committed.Offset != message.Offset || !migration.called {
			t.Fatalf("offset committed before durable manifest result: offset=%d result=%+v", committed.Offset, migration.result)
		}
	case <-time.After(time.Second):
		t.Fatal("Evidence record was not committed after terminal adjudication")
	}
	if !migration.called || migration.result.Adjudication != ievidencemigration.AdjudicationConflict ||
		migration.result.ManifestID != "manifest-1" || migration.result.EntryID != "entry-1" ||
		migration.result.Topic != message.Topic || migration.result.Partition != message.Partition || migration.result.Offset != message.Offset {
		t.Fatalf("migration terminal result was not persisted through bootstrap dependencies: %+v", migration.result)
	}
}

func TestEvidenceKafkaRuntimeDoesNotCommitWhenMigrationResultIsNotDurable(t *testing.T) {
	migration := &bootstrapEvidenceMigration{err: errors.New("result store unavailable")}
	reader := newBootstrapEvidenceReader()
	runtime := startEvidenceKafkaRuntime(t, migration, reader)
	reader.messages <- migrationMessage(time.Date(2026, 9, 25, 8, 30, 0, 0, time.UTC))
	deadline := time.Now().Add(time.Second)
	for runtime.State().Reason != "ledger_decision_pending" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := runtime.State(); got.Ready || got.Reason != "ledger_decision_pending" {
		t.Fatalf("runtime state = %+v, want terminal-decision pending", got)
	}
	select {
	case message := <-reader.commits:
		t.Fatalf("offset %d committed without durable migration result", message.Offset)
	default:
	}
}

func startEvidenceKafkaRuntime(t *testing.T, migration *bootstrapEvidenceMigration, reader *bootstrapEvidenceReader) *kafkaruntime.Runtime {
	t.Helper()
	runtime, err := newEvidenceKafkaRuntime(
		conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{Enabled: true, Topic: evidenceconsumer.Topic, Group: "evidence-test"},
		bootstrapEvidenceAdmission{}, migration, migration, bootstrapEvidenceLedger{}, bootstrapEvidenceRejections{},
		func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (kafkaruntime.Reader, error) {
			return reader, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	return runtime
}

func migrationMessage(now time.Time) kafka.Message {
	streamID := "bridge:boot-1"
	value := []byte(`{"event_id":"event-1","payload_hash":"hash-1","producer_stream_id":"bridge:boot-1","producer_sequence":1,"envelope":{"owner":{"application_principal_id":"bridge","effective_subject_type":"service","effective_subject_id":"bridge"}}}`)
	return kafka.Message{
		Topic: evidenceconsumer.Topic, Key: []byte(streamID), Value: value, Partition: 2, Offset: 20, Time: now,
		Headers: []kafka.Header{
			{Key: "content-type", Value: []byte("application/json")},
			{Key: "bkn-trace-schema-version", Value: []byte("3.0.0")},
			{Key: "capture_policy_revision", Value: []byte("0")},
			{Key: "producer_instance_id", Value: []byte("bridge#boot-1")},
			{Key: "bkn-evidence-record-class", Value: []byte("migration")},
			{Key: "bkn-evidence-migration-id", Value: []byte("manifest-1")},
		},
	}
}
