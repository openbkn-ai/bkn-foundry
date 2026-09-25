// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidenceconsumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceadmission"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencemigration"
)

type Ledger interface {
	IngestKafka(context.Context, ledgervo.Event, ievidenceledger.KafkaCoordinate) (ievidenceledger.KafkaResult, error)
}

type RejectionWriter interface {
	RecordKafkaRejection(context.Context, ievidenceadmission.Record, ievidenceadmission.RejectionDetails) error
}

type Processor struct {
	admission        ievidenceadmission.ReadOnlySource
	migration        ievidencemigration.AdmissionReader
	migrationResults ievidencemigration.ConsumerResultWriter
	ledger           Ledger
	rejections       RejectionWriter
}

func NewProcessor(admission ievidenceadmission.ReadOnlySource, ledger Ledger, rejections RejectionWriter) (*Processor, error) {
	if admission == nil || ledger == nil || rejections == nil {
		return nil, errors.New("evidence Kafka processor dependencies are required")
	}
	return &Processor{admission: admission, ledger: ledger, rejections: rejections}, nil
}

// NewProcessorWithMigration adds the one-time manifest authority without
// giving the live-capture reader write access to migration results.
func NewProcessorWithMigration(admission ievidenceadmission.ReadOnlySource, migration ievidencemigration.AdmissionReader, results ievidencemigration.ConsumerResultWriter, ledger Ledger, rejections RejectionWriter) (*Processor, error) {
	if migration == nil || results == nil {
		return nil, errors.New("evidence migration admission and result dependencies are required")
	}
	processor, err := NewProcessor(admission, ledger, rejections)
	if err != nil {
		return nil, err
	}
	processor.migration, processor.migrationResults = migration, results
	return processor, nil
}

// Process returns nil only when an Evidence Ledger terminal decision or a
// durable rejection exists. Transport offset commit is owned by kafkaruntime.
func (p *Processor) Process(ctx context.Context, record Record) error {
	if record.Topic != Topic {
		return errors.New("evidence record topic mismatch")
	}
	if record.BrokerTime.IsZero() || record.BrokerTimestamp == "" {
		return errors.New("kafka Broker LogAppendTime is unavailable; Evidence offset remains uncommitted")
	}
	if record.BrokerTimestamp != record.BrokerTime.UTC().Format(time.RFC3339Nano) {
		return errors.New("kafka broker timestamp representations disagree; Evidence offset remains uncommitted")
	}
	headerValues, err := ParseHeaders(record.Headers)
	if err != nil {
		return p.reject(ctx, record, "header_contract_invalid", ledgervo.Event{})
	}
	switch headerValues["bkn-evidence-record-class"] {
	case "live":
		return p.processLive(ctx, record)
	case "migration":
		return p.processMigration(ctx, record, headerValues)
	default:
		return p.reject(ctx, record, "header_contract_invalid", ledgervo.Event{})
	}
}

func (p *Processor) processLive(ctx context.Context, record Record) error {
	if err := ValidateLiveRecordContract(record); err != nil {
		return p.reject(ctx, record, "header_contract_invalid", ledgervo.Event{})
	}
	var snapshot PolicySnapshot
	headerValues, _ := ParseHeaders(record.Headers)
	revisionText := headerValues["capture_policy_revision"]
	revision, parseErr := strconv.ParseUint(revisionText, 10, 64)
	if parseErr == nil && revision > 0 && revisionText != "" && (len(revisionText) == 1 || revisionText[0] != '0') {
		persisted, err := p.admission.Lookup(ctx, revision, headerValues["producer_instance_id"])
		if err != nil {
			return fmt.Errorf("read persisted Evidence admission state: %w", err)
		}
		snapshot = PolicySnapshot{
			Revision: persisted.Revision, Enabled: persisted.Enabled, InstanceID: persisted.InstanceID,
			RegisteredRevision: persisted.RegisteredRevision, ProcessBootID: persisted.ProcessBootID,
			Closure: ClosureWatermark{InstanceID: persisted.Closure.InstanceID, Revision: persisted.Closure.Revision,
				LastAcceptedSequence: persisted.Closure.LastAcceptedSequence, ClosedAt: persisted.Closure.ClosedAt},
		}
	}
	decision := AdmitLive(record, snapshot)
	if decision.Decision != "accept" {
		return p.reject(ctx, record, decision.Reason, ledgervo.Event{})
	}
	event, err := parseLiveEvent(record.Value)
	if err != nil {
		return p.reject(ctx, record, "json_invalid", ledgervo.Event{})
	}
	if event.ProducerStreamID != record.ProducerStreamID || event.ProducerSequence != record.ProducerSequence {
		return p.reject(ctx, record, "record_value_transport_mismatch", event)
	}
	result, err := p.ledger.IngestKafka(ctx, event, ievidenceledger.KafkaCoordinate{Topic: record.Topic, Partition: record.Partition, Offset: record.Offset})
	if err == nil {
		switch result.Decision {
		case ievidenceledger.KafkaAccepted, ievidenceledger.KafkaDeduplicated:
			if !result.Ack.Durable {
				return errors.New("evidence Ledger returned a non-durable acknowledgement")
			}
			return nil
		case ievidenceledger.KafkaConflict:
			return nil // conflict and coordinate were durably adjudicated in the same transaction
		default:
			return fmt.Errorf("unknown Evidence Kafka terminal decision %q", result.Decision)
		}
	}
	switch {
	case ledgersvc.IsCode(err, ledgersvc.CodeInvalidEvent):
		return p.reject(ctx, record, "invalid_evidence_event", event)
	default:
		return fmt.Errorf("evidence Ledger decision is temporary or uncertain: %w", err)
	}
}

func (p *Processor) processMigration(ctx context.Context, record Record, headers map[string]string) error {
	if err := ValidateMigrationRecordContract(record); err != nil {
		return p.reject(ctx, record, "header_contract_invalid", ledgervo.Event{})
	}
	event, err := parseLiveEvent(record.Value)
	if err != nil {
		return p.reject(ctx, record, "json_invalid", ledgervo.Event{})
	}
	if p.migration == nil {
		return p.reject(ctx, record, "migration_manifest_unknown", event)
	}
	admission, found, err := p.migration.LookupAdmission(ctx, headers["bkn-evidence-migration-id"], event.EventID)
	if err != nil {
		return fmt.Errorf("read Evidence migration admission: %w", err)
	}
	if !found {
		return p.reject(ctx, record, "migration_manifest_unknown", event)
	}
	if admission.State != ievidencemigration.ManifestActive {
		return p.reject(ctx, record, "migration_manifest_not_active", event)
	}
	if admission.ManifestID != headers["bkn-evidence-migration-id"] || admission.EventID != event.EventID || admission.PayloadHash != event.PayloadHash || admission.EntryID == "" {
		return p.rejectMigration(ctx, record, admission, "migration_manifest_entry_mismatch", event)
	}
	if event.ProducerStreamID != record.ProducerStreamID || event.ProducerSequence != record.ProducerSequence {
		return p.rejectMigration(ctx, record, admission, "record_value_transport_mismatch", event)
	}
	switch admission.Classification {
	case "publish":
		// The only manifest classification allowed to enter the Ledger.
	case "verify_delivered", "coverage_gap":
		// These are reconciler conclusions, not Consumer authorization. The
		// unexpected Record gets the bounded durable rejection that makes its
		// offset terminal; it must not write this entry's result, because the
		// reconciler remains the sole authority for those classifications.
		return p.reject(ctx, record, "migration_manifest_entry_mismatch", event)
	default:
		return p.rejectMigration(ctx, record, admission, "migration_manifest_entry_mismatch", event)
	}
	result, err := p.ledger.IngestKafka(ctx, event, ievidenceledger.KafkaCoordinate{Topic: record.Topic, Partition: record.Partition, Offset: record.Offset})
	if err != nil {
		switch {
		case ledgersvc.IsCode(err, ledgersvc.CodeInvalidEvent):
			return p.rejectMigration(ctx, record, admission, "invalid_evidence_event", event)
		default:
			return fmt.Errorf("Evidence migration Ledger decision is temporary or uncertain: %w", err)
		}
	}
	consumerResult := ievidencemigration.ConsumerResult{ManifestID: admission.ManifestID, EntryID: admission.EntryID, Topic: record.Topic, Partition: record.Partition, Offset: record.Offset, IngestSequence: result.Ack.IngestSequence}
	switch result.Decision {
	case ievidenceledger.KafkaAccepted:
		consumerResult.Adjudication, consumerResult.Observation = ievidencemigration.AdjudicationLedgerCommitted, "accepted"
	case ievidenceledger.KafkaDeduplicated:
		consumerResult.Adjudication, consumerResult.Observation = ievidencemigration.AdjudicationLedgerCommitted, "deduplicated"
	case ievidenceledger.KafkaConflict:
		consumerResult.Adjudication, consumerResult.Observation, consumerResult.ReasonCode = ievidencemigration.AdjudicationConflict, "conflict", result.ReasonCode
	default:
		return fmt.Errorf("unknown Evidence migration Kafka terminal decision %q", result.Decision)
	}
	if err := p.persistMigrationResult(ctx, consumerResult); err != nil {
		return err
	}
	if (result.Decision == ievidenceledger.KafkaAccepted || result.Decision == ievidenceledger.KafkaDeduplicated) && !result.Ack.Durable {
		return errors.New("Evidence Ledger returned a non-durable acknowledgement")
	}
	return nil
}

// rejectMigration establishes the migration terminal fact before recording
// the bounded global rejection. If either durable write fails, Process returns
// an error and the runtime must not mark the Kafka offset.
func (p *Processor) rejectMigration(ctx context.Context, record Record, admission ievidencemigration.Admission, reason string, event ledgervo.Event) error {
	if err := p.recordMigrationTerminal(ctx, record, admission, ievidencemigration.AdjudicationRejected, "rejected", reason, 0); err != nil {
		return err
	}
	return p.reject(ctx, record, reason, event)
}

func (p *Processor) recordMigrationTerminal(ctx context.Context, record Record, admission ievidencemigration.Admission, adjudication ievidencemigration.Adjudication, observation, reason string, ingestSequence uint64) error {
	return p.persistMigrationResult(ctx, ievidencemigration.ConsumerResult{ManifestID: admission.ManifestID, EntryID: admission.EntryID, Adjudication: adjudication, Observation: observation, ReasonCode: reason, Topic: record.Topic, Partition: record.Partition, Offset: record.Offset, IngestSequence: ingestSequence})
}

func (p *Processor) persistMigrationResult(ctx context.Context, result ievidencemigration.ConsumerResult) error {
	if p.migrationResults == nil {
		return errors.New("Evidence migration result writer is unavailable; offset remains uncommitted")
	}
	if err := p.migrationResults.RecordConsumerResult(ctx, result); err != nil {
		return fmt.Errorf("durable Evidence migration result failed: %w", err)
	}
	return nil
}

func (p *Processor) reject(ctx context.Context, record Record, reason string, event ledgervo.Event) error {
	details := ievidenceadmission.RejectionDetails{
		EventID: event.EventID, PayloadHash: event.PayloadHash, ProducerID: event.ProducerID,
		ReasonCode: reason,
	}
	if values, err := ParseHeaders(record.Headers); err == nil {
		details.MigrationID = values["bkn-evidence-migration-id"]
	}
	neutral := ievidenceadmission.Record{Topic: record.Topic, Key: record.Key, Value: record.Value, Partition: record.Partition,
		Offset: record.Offset, BrokerTime: record.BrokerTime, ProducerStreamID: record.ProducerStreamID,
		ProducerSequence: record.ProducerSequence}
	for _, header := range record.Headers {
		neutral.Headers = append(neutral.Headers, ievidenceadmission.Header{Key: header.Key, Value: header.Value})
	}
	if err := p.rejections.RecordKafkaRejection(ctx, neutral, details); err != nil {
		return fmt.Errorf("durable Evidence rejection decision failed: %w", err)
	}
	return nil
}

func parseLiveEvent(value []byte) (ledgervo.Event, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return ledgervo.Event{}, err
	}
	if _, exists := fields["owner"]; exists {
		return ledgervo.Event{}, errors.New("owner must be carried only in envelope")
	}
	var event ledgervo.Event
	if err := json.Unmarshal(value, &event); err != nil {
		return ledgervo.Event{}, err
	}
	var envelope struct {
		Owner sessionvo.Owner `json:"owner"`
	}
	if err := json.Unmarshal(event.Envelope, &envelope); err != nil {
		return ledgervo.Event{}, err
	}
	event.Owner = envelope.Owner
	return event, nil
}
