// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidenceconsumer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceadmission"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencemigration"
)

type processorAdmission struct {
	snapshot ievidenceadmission.Snapshot
	err      error
}

func (a processorAdmission) Lookup(context.Context, uint64, string) (ievidenceadmission.Snapshot, error) {
	return a.snapshot, a.err
}

type processorLedger struct{ called bool }

func (l *processorLedger) IngestKafka(context.Context, ledgervo.Event, ievidenceledger.KafkaCoordinate) (ievidenceledger.KafkaResult, error) {
	l.called = true
	return ievidenceledger.KafkaResult{}, errors.New("unexpected ledger call")
}

type acceptedProcessorLedger struct{ called bool }

func (l *acceptedProcessorLedger) IngestKafka(context.Context, ledgervo.Event, ievidenceledger.KafkaCoordinate) (ievidenceledger.KafkaResult, error) {
	l.called = true
	return ievidenceledger.KafkaResult{Decision: ievidenceledger.KafkaAccepted, Ack: ledgervo.DurableAck{Durable: true, IngestSequence: 7}}, nil
}

type invalidProcessorLedger struct{ called bool }

func (l *invalidProcessorLedger) IngestKafka(context.Context, ledgervo.Event, ievidenceledger.KafkaCoordinate) (ievidenceledger.KafkaResult, error) {
	l.called = true
	return ievidenceledger.KafkaResult{}, &ledgersvc.DomainError{Code: ledgersvc.CodeInvalidEvent, Message: "invalid test event"}
}

type processorRejections struct {
	called  bool
	record  ievidenceadmission.Record
	details ievidenceadmission.RejectionDetails
	err     error
}

type processorMigration struct {
	admission ievidencemigration.Admission
	found     bool
	err       error
	result    ievidencemigration.ConsumerResult
}

func (m *processorMigration) LookupAdmission(context.Context, string, string) (ievidencemigration.Admission, bool, error) {
	return m.admission, m.found, m.err
}

func (m *processorMigration) RecordConsumerResult(_ context.Context, result ievidencemigration.ConsumerResult) error {
	m.result = result
	return nil
}

func (r *processorRejections) RecordKafkaRejection(_ context.Context, record ievidenceadmission.Record, details ievidenceadmission.RejectionDetails) error {
	r.called, r.record, r.details = true, record, details
	return r.err
}

func TestProcessorPersistsAdmissionRejectionBeforeReturningTerminal(t *testing.T) {
	instance := "spiffe://cluster.local/ns/openbkn/sa/bkn-backend#aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	record := Record{Topic: Topic, Key: "bkn-backend:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Value: []byte(`{}`), Partition: 2, Offset: 19,
		BrokerTime: time.Date(2026, 9, 24, 8, 30, 0, 0, time.UTC), ProducerStreamID: "bkn-backend:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ProducerSequence: 1,
		BrokerTimestamp: "2026-09-24T08:30:00Z",
		Headers:         []Header{{Key: "content-type", Value: "application/json"}, {Key: "bkn-trace-schema-version", Value: "3.0.0"}, {Key: "capture_policy_revision", Value: "41"}, {Key: "producer_instance_id", Value: instance}, {Key: "bkn-evidence-record-class", Value: "live"}}}
	ledger, rejections := &processorLedger{}, &processorRejections{}
	processor, err := NewProcessor(processorAdmission{snapshot: ievidenceadmission.Snapshot{Revision: 41, Enabled: false}}, ledger, rejections)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if !rejections.called || rejections.details.ReasonCode != "capture_policy_revision_disabled" || rejections.record.Offset != record.Offset {
		t.Fatalf("rejection not durably requested for the Kafka coordinate: %+v", rejections)
	}
	if ledger.called {
		t.Fatal("unauthorized Evidence event reached the ledger")
	}
}

func TestProcessorDoesNotMakeAdmissionRejectionOnTemporaryHistoryFailure(t *testing.T) {
	stream := "bkn-backend:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	now := time.Now().UTC()
	record := Record{Topic: Topic, Key: stream, ProducerStreamID: stream, BrokerTime: now, BrokerTimestamp: now.Format(time.RFC3339Nano), Headers: []Header{{Key: "content-type", Value: "application/json"}, {Key: "bkn-trace-schema-version", Value: "3.0.0"}, {Key: "capture_policy_revision", Value: "41"}, {Key: "producer_instance_id", Value: "spiffe://cluster.local/ns/openbkn/sa/bkn-backend#aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, {Key: "bkn-evidence-record-class", Value: "live"}}}
	rejections := &processorRejections{}
	processor, err := NewProcessor(processorAdmission{err: errors.New("db unavailable")}, &processorLedger{}, rejections)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), record); err == nil {
		t.Fatal("temporary admission read failure was treated as terminal")
	}
	if rejections.called {
		t.Fatal("temporary failure was persisted as a permanent rejection")
	}
}

func TestMigrationRecordRejectsManifestHashMismatchBeforeLedger(t *testing.T) {
	stream := "bkn-backend"
	now := time.Date(2026, 9, 25, 8, 30, 0, 0, time.UTC)
	record := Record{Topic: Topic, Key: stream, ProducerStreamID: stream, ProducerSequence: 1, BrokerTime: now, BrokerTimestamp: now.Format(time.RFC3339Nano), Partition: 2, Offset: 20,
		Value:   []byte(`{"event_id":"evt-1","payload_hash":"actual","producer_stream_id":"bkn-backend","producer_sequence":1,"envelope":{"owner":{"application_principal_id":"bkn-backend","effective_subject_type":"service","effective_subject_id":"svc"}}}`),
		Headers: []Header{{Key: "content-type", Value: "application/json"}, {Key: "bkn-trace-schema-version", Value: "3.0.0"}, {Key: "capture_policy_revision", Value: "0"}, {Key: "producer_instance_id", Value: "bridge#boot"}, {Key: "bkn-evidence-record-class", Value: "migration"}, {Key: "bkn-evidence-migration-id", Value: "m-1"}}}
	ledger, rejections := &processorLedger{}, &processorRejections{}
	migration := &processorMigration{found: true, admission: ievidencemigration.Admission{ManifestID: "m-1", State: ievidencemigration.ManifestActive, EntryID: "entry-1", EventID: "evt-1", PayloadHash: "other", Classification: "publish"}}
	processor, err := NewProcessorWithMigration(processorAdmission{}, migration, migration, ledger, rejections)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if !rejections.called || rejections.details.ReasonCode != "migration_manifest_entry_mismatch" {
		t.Fatalf("expected durable mismatch rejection: %+v", rejections)
	}
	if ledger.called {
		t.Fatal("mismatched migration record reached Ledger")
	}
}

func TestMigrationNonPublishClassificationsNeverReachLedger(t *testing.T) {
	for _, tc := range []struct {
		classification string
		want           ievidencemigration.Adjudication
	}{
		{classification: "verify_delivered", want: ievidencemigration.AdjudicationVerifiedDelivered},
		{classification: "coverage_gap", want: ievidencemigration.AdjudicationCoverageGap},
	} {
		t.Run(tc.classification, func(t *testing.T) {
			stream := "bkn-backend"
			now := time.Date(2026, 9, 25, 8, 30, 0, 0, time.UTC)
			record := Record{Topic: Topic, Key: stream, ProducerStreamID: stream, ProducerSequence: 1, BrokerTime: now, BrokerTimestamp: now.Format(time.RFC3339Nano), Partition: 2, Offset: 21,
				Value:   []byte(`{"event_id":"evt-1","payload_hash":"hash-1","producer_stream_id":"bkn-backend","producer_sequence":1,"envelope":{"owner":{"application_principal_id":"bkn-backend","effective_subject_type":"service","effective_subject_id":"svc"}}}`),
				Headers: []Header{{Key: "content-type", Value: "application/json"}, {Key: "bkn-trace-schema-version", Value: "3.0.0"}, {Key: "capture_policy_revision", Value: "0"}, {Key: "producer_instance_id", Value: "bridge#boot"}, {Key: "bkn-evidence-record-class", Value: "migration"}, {Key: "bkn-evidence-migration-id", Value: "m-1"}}}
			ledger, rejections := &processorLedger{}, &processorRejections{}
			migration := &processorMigration{found: true, admission: ievidencemigration.Admission{ManifestID: "m-1", State: ievidencemigration.ManifestActive, EntryID: "entry-1", EventID: "evt-1", PayloadHash: "hash-1", Classification: tc.classification, ClassificationReason: "historical"}}
			processor, err := NewProcessorWithMigration(processorAdmission{}, migration, migration, ledger, rejections)
			if err != nil {
				t.Fatal(err)
			}
			if err := processor.Process(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			if ledger.called {
				t.Fatalf("%s must not reach Ledger", tc.classification)
			}
			if rejections.called {
				t.Fatalf("%s is a canonical migration terminal result, not a global rejection", tc.classification)
			}
			if migration.result.Adjudication != tc.want || migration.result.EntryID != "entry-1" {
				t.Fatalf("unexpected migration terminal result: %+v", migration.result)
			}
		})
	}
}

func TestMigrationPublishClassificationIsTheOnlyClassThatReachesLedger(t *testing.T) {
	stream := "bkn-backend"
	now := time.Date(2026, 9, 25, 8, 30, 0, 0, time.UTC)
	record := Record{Topic: Topic, Key: stream, ProducerStreamID: stream, ProducerSequence: 1, BrokerTime: now, BrokerTimestamp: now.Format(time.RFC3339Nano), Partition: 2, Offset: 23,
		Value:   []byte(`{"event_id":"evt-1","payload_hash":"hash-1","producer_stream_id":"bkn-backend","producer_sequence":1,"envelope":{"owner":{"application_principal_id":"bkn-backend","effective_subject_type":"service","effective_subject_id":"svc"}}}`),
		Headers: []Header{{Key: "content-type", Value: "application/json"}, {Key: "bkn-trace-schema-version", Value: "3.0.0"}, {Key: "capture_policy_revision", Value: "0"}, {Key: "producer_instance_id", Value: "bridge#boot"}, {Key: "bkn-evidence-record-class", Value: "migration"}, {Key: "bkn-evidence-migration-id", Value: "m-1"}}}
	ledger, rejections := &acceptedProcessorLedger{}, &processorRejections{}
	migration := &processorMigration{found: true, admission: ievidencemigration.Admission{ManifestID: "m-1", State: ievidencemigration.ManifestActive, EntryID: "entry-1", EventID: "evt-1", PayloadHash: "hash-1", Classification: "publish"}}
	processor, err := NewProcessorWithMigration(processorAdmission{}, migration, migration, ledger, rejections)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if !ledger.called {
		t.Fatal("publish migration entry did not reach Ledger")
	}
	if migration.result.Adjudication != ievidencemigration.AdjudicationLedgerCommitted || migration.result.Observation != "accepted" {
		t.Fatalf("unexpected publish terminal result: %+v", migration.result)
	}
}

func TestMigrationActivePermanentRejectionWritesResultBeforeGlobalRejection(t *testing.T) {
	stream := "bkn-backend"
	now := time.Date(2026, 9, 25, 8, 30, 0, 0, time.UTC)
	record := Record{Topic: Topic, Key: stream, ProducerStreamID: stream, ProducerSequence: 2, BrokerTime: now, BrokerTimestamp: now.Format(time.RFC3339Nano), Partition: 2, Offset: 22,
		Value:   []byte(`{"event_id":"evt-1","payload_hash":"hash-1","producer_stream_id":"bkn-backend","producer_sequence":1,"envelope":{"owner":{"application_principal_id":"bkn-backend","effective_subject_type":"service","effective_subject_id":"svc"}}}`),
		Headers: []Header{{Key: "content-type", Value: "application/json"}, {Key: "bkn-trace-schema-version", Value: "3.0.0"}, {Key: "capture_policy_revision", Value: "0"}, {Key: "producer_instance_id", Value: "bridge#boot"}, {Key: "bkn-evidence-record-class", Value: "migration"}, {Key: "bkn-evidence-migration-id", Value: "m-1"}}}
	ledger, rejections := &processorLedger{}, &processorRejections{}
	migration := &processorMigration{found: true, admission: ievidencemigration.Admission{ManifestID: "m-1", State: ievidencemigration.ManifestActive, EntryID: "entry-1", EventID: "evt-1", PayloadHash: "hash-1", Classification: "publish"}}
	processor, err := NewProcessorWithMigration(processorAdmission{}, migration, migration, ledger, rejections)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if ledger.called {
		t.Fatal("transport mismatch must not reach Ledger")
	}
	if migration.result.Adjudication != ievidencemigration.AdjudicationRejected || migration.result.ReasonCode != "record_value_transport_mismatch" {
		t.Fatalf("missing canonical rejected result: %+v", migration.result)
	}
	if !rejections.called || rejections.details.ReasonCode != "record_value_transport_mismatch" {
		t.Fatalf("missing durable global rejection: %+v", rejections)
	}
}

func TestMigrationLedgerInvalidEventWritesResultBeforeGlobalRejection(t *testing.T) {
	stream := "bkn-backend"
	now := time.Date(2026, 9, 25, 8, 30, 0, 0, time.UTC)
	record := Record{Topic: Topic, Key: stream, ProducerStreamID: stream, ProducerSequence: 1, BrokerTime: now, BrokerTimestamp: now.Format(time.RFC3339Nano), Partition: 2, Offset: 24,
		Value:   []byte(`{"event_id":"evt-1","payload_hash":"hash-1","producer_stream_id":"bkn-backend","producer_sequence":1,"envelope":{"owner":{"application_principal_id":"bkn-backend","effective_subject_type":"service","effective_subject_id":"svc"}}}`),
		Headers: []Header{{Key: "content-type", Value: "application/json"}, {Key: "bkn-trace-schema-version", Value: "3.0.0"}, {Key: "capture_policy_revision", Value: "0"}, {Key: "producer_instance_id", Value: "bridge#boot"}, {Key: "bkn-evidence-record-class", Value: "migration"}, {Key: "bkn-evidence-migration-id", Value: "m-1"}}}
	ledger, rejections := &invalidProcessorLedger{}, &processorRejections{}
	migration := &processorMigration{found: true, admission: ievidencemigration.Admission{ManifestID: "m-1", State: ievidencemigration.ManifestActive, EntryID: "entry-1", EventID: "evt-1", PayloadHash: "hash-1", Classification: "publish"}}
	processor, err := NewProcessorWithMigration(processorAdmission{}, migration, migration, ledger, rejections)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if !ledger.called {
		t.Fatal("publish entry must call Ledger")
	}
	if migration.result.Adjudication != ievidencemigration.AdjudicationRejected || migration.result.ReasonCode != "invalid_evidence_event" {
		t.Fatalf("missing rejected migration result: %+v", migration.result)
	}
	if !rejections.called || rejections.details.ReasonCode != "invalid_evidence_event" {
		t.Fatalf("missing global rejection: %+v", rejections)
	}
}
