//go:build integration

// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/evidenceconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceadmission"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
)

type failedCallContract struct {
	Calls   []failedCallContractCall   `json:"calls"`
	Records []failedCallContractRecord `json:"records"`
}

type failedCallContractCall struct {
	Name        string                      `json:"name"`
	ToolName    string                      `json:"tool_name"`
	OperationID string                      `json:"operation_id"`
	Input       sessionvo.PayloadEnvelope   `json:"input"`
	Profile     sessionvo.CapabilityProfile `json:"capability_profile"`
	Finish      struct {
		ReceiptID            string                         `json:"receipt_id"`
		Error                sessionvo.PayloadEnvelope      `json:"error"`
		RequestID            string                         `json:"request_id"`
		TraceID              string                         `json:"trace_id"`
		SpanID               string                         `json:"span_id"`
		Retryable            bool                           `json:"retryable"`
		Durability           sessionvo.EvidenceDurability   `json:"evidence_durability"`
		BusinessRefs         []sessionvo.BusinessRef        `json:"business_refs"`
		Expectation          *sessionvo.EvidenceExpectation `json:"evidence_expectation"`
		PartialReasons       []string                       `json:"partial_reasons"`
		ObservedEvidenceRefs []string                       `json:"observed_evidence_refs"`
		ArtifactRefs         []string                       `json:"artifact_refs"`
	} `json:"finish"`
}

type failedCallContractRecord struct {
	Key     string
	Value   []byte
	Headers []evidenceconsumer.Header
}

// This test consumes the actual handler/adapter/publisher wire output generated
// by TestLifecycleMiddlewareFinalizesRealAdapterFailures in agent-retrieval.
// It runs the real Kafka consumer and Ledger validation service. The test store
// adapts memory commits to Kafka decisions; broker offsets and MariaDB atomic
// persistence remain separate integration suites.
func TestFailedCallProducerToLedgerIntegrity(t *testing.T) {
	path := os.Getenv("BKN_TRACE_FAILED_CALL_CONTRACT")
	if path == "" {
		t.Skip("run scripts/test_failed_call_contract.sh to generate the producer contract")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var contract failedCallContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Records) == 0 {
		t.Fatal("producer did not export any original Kafka records")
	}
	ledger := &failedCallKafkaStore{Store: ledgerstore.New()}
	service := ledgersvc.New(ledger)
	processor, err := evidenceconsumer.NewProcessor(failedCallAdmission{}, service, failedCallRejections{})
	if err != nil {
		t.Fatal(err)
	}
	brokerTime := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	for index, original := range contract.Records {
		var wire ledgervo.Event
		if err := json.Unmarshal(original.Value, &wire); err != nil {
			t.Fatal(err)
		}
		record := evidenceconsumer.Record{Topic: evidenceconsumer.Topic, Key: original.Key, Value: original.Value, Headers: original.Headers, Partition: 0, Offset: int64(index), BrokerTime: brokerTime, BrokerTimestamp: brokerTime.Format(time.RFC3339Nano), ProducerStreamID: wire.ProducerStreamID, ProducerSequence: wire.ProducerSequence}
		for _, decision := range []ievidenceledger.KafkaDecision{ievidenceledger.KafkaAccepted, ievidenceledger.KafkaDeduplicated} {
			if err := processor.Process(context.Background(), record); err != nil {
				t.Fatalf("actual Kafka consumer rejected producer record: %v", err)
			}
			if ledger.result.Decision != decision || !ledger.result.Ack.Durable {
				t.Fatalf("Ledger decision mismatch: %+v", ledger.result)
			}
		}
	}

	checked := 0
	for _, call := range contract.Calls {
		if call.Name != "validation failure" && call.Name != "empty SQL" && call.Name != "missing SQL resource" && call.Name != "SQL backend failure" && call.Name != "SQL policy without target" {
			continue
		}
		checked++
		t.Run(call.Name, func(t *testing.T) {
			if os.Getenv("BKN_TRACE_TEST_MARIADB_DSN") != "" {
				verifyFailedCallMariaDB(t, call, contract.Records)
			}
			snapshot, _, now := integrityFixture()
			owner := sessionvo.Owner{ApplicationPrincipalID: "client-1", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user-1"}
			snapshot.Interaction.ID = "int-1"
			snapshot.Interaction.ConversationID = "conv-1"
			snapshot.Interaction.ExecutionStatus = sessionvo.InteractionFailed
			snapshot.Operations[0].ID = call.OperationID
			snapshot.Operations[0].InteractionID = "int-1"
			snapshot.Operations[0].ConversationID = "conv-1"
			fact := &snapshot.CallFacts[0]
			fact.OperationID = call.OperationID
			fact.InteractionID = "int-1"
			fact.ConversationID = "conv-1"
			fact.ToolName = call.ToolName
			fact.Input = call.Input
			fact.Output = nil
			fact.Error = &call.Finish.Error
			fact.CapabilityProfile = &call.Profile
			fact.Status = sessionvo.AttemptFailed
			fact.RequestID = call.Finish.RequestID
			fact.TraceID = call.Finish.TraceID
			receipt := &snapshot.Receipts[0]
			receipt.OperationID = call.OperationID
			receipt.InteractionID = "int-1"
			receipt.ConversationID = "conv-1"
			receipt.Owner = owner
			receipt.Status = sessionvo.ReceiptFailed
			receipt.BusinessRefs = call.Finish.BusinessRefs
			receipt.RequestID = fact.RequestID
			receipt.TraceID = fact.TraceID
			report, err := evaluateRecordIntegrity(snapshot, owner, now, nil)
			if call.Name == "SQL policy without target" {
				if err == nil || !strings.Contains(err.Error(), "failed-call target context unavailable") {
					t.Fatalf("targetless policy rejection mislabeled as input rejection: %+v %v", report, err)
				}
				return
			}
			if err != nil || report == nil || report.Status != "complete" {
				t.Fatalf("producer receipt failed integrity before event arrives: %+v %v", report, err)
			}
			if call.Name != "SQL backend failure" {
				return
			}
			// A known SQL target must also be recoverable from its matching Ledger event.
			receipt.BusinessRefs = nil
			if _, err := evaluateRecordIntegrity(snapshot, owner, now, nil); err == nil || !strings.Contains(err.Error(), "failed-call target context unavailable") {
				t.Fatalf("unknown target negative relaxed: %v", err)
			}
			events, err := ledger.ListInteractionEvents(context.Background(), owner, "int-1")
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.OperationID != call.OperationID {
					continue
				}
				raw, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				snapshot.Ledger.Events = append(snapshot.Ledger.Events, sessionvo.EvidenceLedgerRecord{EventID: event.EventID, Envelope: raw})
			}
			report, err = evaluateRecordIntegrity(snapshot, owner, now, nil)
			if err != nil || report == nil || report.Status != "complete" {
				t.Fatalf("matching Ledger target failed integrity: %+v %v", report, err)
			}
		})
	}
	if checked != 5 {
		t.Fatalf("expected five real failure contracts, got %d", checked)
	}
}

// Only the storage boundary is adapted. Admission, wire parsing, owner mapping,
// event validation and duplicate decisions run through production code.
type failedCallKafkaStore struct {
	*ledgerstore.Store
	result ievidenceledger.KafkaResult
}

func (store *failedCallKafkaStore) CommitKafka(ctx context.Context, event ledgervo.Event, _ ievidenceledger.KafkaCoordinate) (ievidenceledger.KafkaResult, error) {
	ack, err := store.Commit(ctx, event)
	if err != nil {
		return ievidenceledger.KafkaResult{}, err
	}
	decision := ievidenceledger.KafkaAccepted
	if ack.Replayed {
		decision = ievidenceledger.KafkaDeduplicated
	}
	store.result = ievidenceledger.KafkaResult{Decision: decision, Ack: ack}
	return store.result, nil
}

type failedCallAdmission struct{}

func (failedCallAdmission) Lookup(context.Context, uint64, string) (ievidenceadmission.Snapshot, error) {
	return ievidenceadmission.Snapshot{Revision: 1, Enabled: true, InstanceID: "test#2072", RegisteredRevision: 1, ProcessBootID: "2072"}, nil
}

type failedCallRejections struct{}

func (failedCallRejections) RecordKafkaRejection(_ context.Context, _ ievidenceadmission.Record, details ievidenceadmission.RejectionDetails) error {
	return fmt.Errorf("unexpected Kafka rejection: %s", details.ReasonCode)
}
