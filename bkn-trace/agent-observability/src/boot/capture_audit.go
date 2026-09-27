// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN

package boot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturecontrollersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type captureAuditSink struct {
	publisher   *auditpublisher.Publisher
	producer    interface{ Close() error }
	environment string
}

func newCaptureAuditSink() *captureAuditSink {
	if os.Getenv("BKN_AUDIT_KAFKA_ENABLED") != "true" {
		return nil
	}
	get := func(key string) string { return strings.TrimSpace(os.Getenv(key)) }
	var brokers []string
	for _, broker := range strings.Split(get("BKN_AUDIT_KAFKA_BROKERS"), ",") {
		if broker = strings.TrimSpace(broker); broker != "" {
			brokers = append(brokers, broker)
		}
	}
	environment := get("BKN_AUDIT_ENVIRONMENT")
	if len(brokers) == 0 || get("BKN_AUDIT_KAFKA_SASL_MECHANISM") != "PLAIN" || get("BKN_AUDIT_KAFKA_USERNAME") == "" || os.Getenv("BKN_AUDIT_KAFKA_PASSWORD") == "" || environment == "" {
		log.Print("audit coverage_gap: control publisher configuration incomplete")
		return nil
	}
	config := sarama.NewConfig()
	config.Net.SASL.Enable = true
	config.Net.SASL.Mechanism = sarama.SASLTypePlaintext
	config.Net.SASL.User = get("BKN_AUDIT_KAFKA_USERNAME")
	config.Net.SASL.Password = os.Getenv("BKN_AUDIT_KAFKA_PASSWORD")
	config.Net.DialTimeout, config.Net.ReadTimeout, config.Net.WriteTimeout = 3*time.Second, 3*time.Second, 3*time.Second
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Return.Successes = true
	config.Producer.Retry.Max = 0
	config.Producer.Timeout = 3 * time.Second
	config.Net.MaxOpenRequests = 1
	producer, err := sarama.NewSyncProducer(brokers, config)
	if err != nil {
		log.Printf("audit coverage_gap: control publisher initialization failed: %v", err)
		return nil
	}
	publisher, err := auditpublisher.New(captureAuditSender{producer: producer}, captureAuditObserver{})
	if err != nil {
		_ = producer.Close()
		log.Printf("audit coverage_gap: control publisher initialization failed: %v", err)
		return nil
	}
	return &captureAuditSink{publisher: publisher, producer: producer, environment: environment}
}

type captureAuditObserver struct{}

type captureAuditSender struct{ producer sarama.SyncProducer }

func (s captureAuditSender) Send(_ context.Context, record auditpublisher.Record) error {
	if record.Topic != auditpublisher.Topic {
		return errors.New("invalid control Audit topic")
	}
	headers := make([]sarama.RecordHeader, 0, len(record.Headers))
	for _, header := range record.Headers {
		headers = append(headers, sarama.RecordHeader{Key: []byte(header.Key), Value: append([]byte(nil), header.Value...)})
	}
	_, _, err := s.producer.SendMessage(&sarama.ProducerMessage{Topic: record.Topic, Key: sarama.ByteEncoder(record.Key), Value: sarama.ByteEncoder(record.Value), Headers: headers})
	return err
}

func (captureAuditObserver) ObserveDelivery(delivery auditpublisher.Delivery) {
	if delivery.Outcome != auditpublisher.Delivered {
		log.Printf("audit delivery coverage_gap: outcome=%s attempts=%d", delivery.Outcome, delivery.Attempts)
	}
}

func (s *captureAuditSink) close() error {
	if s == nil {
		return nil
	}
	s.publisher.Close()
	return s.producer.Close()
}

func (s *captureAuditSink) emit(in captureAuditInput) {
	if s == nil {
		log.Print("audit coverage_gap: control publisher is disabled")
		return
	}
	in.Environment = s.environment
	value, err := buildCaptureControlAudit(in)
	if err != nil {
		log.Printf("audit coverage_gap: invalid control event: %v", err)
		return
	}
	if disposition := s.publisher.TryPublish(value); disposition != auditpublisher.Accepted {
		log.Printf("audit coverage_gap: control event not accepted: %s", disposition)
	}
}

func (s *captureAuditSink) requested(actorID, actorType string, before, after capturepolicysvc.Snapshot) {
	if before.Revision == 0 {
		log.Print("audit coverage_gap: prior capture state unavailable")
		return
	}
	phase := "enabling"
	if after.DesiredState == capturepolicysvc.StateDisabled {
		phase = "disabling"
	}
	s.emit(captureAuditInput{EventName: "trace_evidence.configuration_change_requested", Phase: phase,
		Action: "update", Outcome: "success", OperationID: after.Operation.ID, PolicyRevision: after.Revision,
		DesiredState: string(after.DesiredState), EffectiveState: string(after.EffectiveState),
		ActorID: actorID, ActorType: actorType, BeforeHash: captureStateHash(before), AfterHash: captureStateHash(after), OccurredAt: time.Now().UTC()})
}

func (s *captureAuditSink) terminal(event capturecontrollersvc.TerminalEvent) {
	name, action, outcome := "", "apply", "success"
	switch event.Phase {
	case "succeeded":
		name = "trace_evidence.operation_succeeded"
	case "failed":
		name, outcome = "trace_evidence.operation_failed", "failure"
	case "rollback_failed":
		name, outcome = "trace_evidence.operation_failed", "failure"
	case "rollback_completed":
		name, action = "trace_evidence.rollback_completed", "rollback"
	default:
		return
	}
	phase := event.Phase
	if phase == "rollback_failed" {
		phase = "failed"
	} // Audit v1 normalizes terminal failures to failed.
	s.emit(captureAuditInput{EventName: name, Phase: phase, Action: action, Outcome: outcome,
		OperationID: event.OperationID, PolicyRevision: event.PolicyRevision,
		DesiredState: event.DesiredState, EffectiveState: event.EffectiveState,
		ActorID: "agent-observability-control-controller", ActorType: "service_account", FailureCode: event.FailureCode, OccurredAt: event.At})
}

func captureStateHash(snapshot capturepolicysvc.Snapshot) string {
	value, _ := json.Marshal(struct {
		Revision       uint64                 `json:"revision"`
		DesiredState   capturepolicysvc.State `json:"desired_state"`
		EffectiveState capturepolicysvc.State `json:"effective_state"`
	}{snapshot.Revision, snapshot.DesiredState, snapshot.EffectiveState})
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest)
}

type captureAuditInput struct {
	EventName, Phase, Action, Outcome         string
	OperationID, DesiredState, EffectiveState string
	PolicyRevision                            uint64
	ActorID, ActorType, Environment           string
	BeforeHash, AfterHash                     string
	FailureCode                               string
	OccurredAt                                time.Time
}

func buildCaptureControlAudit(in captureAuditInput) ([]byte, error) {
	if in.OperationID == "" || in.PolicyRevision == 0 || in.ActorID == "" ||
		in.Environment == "" || in.OccurredAt.IsZero() {
		return nil, errors.New("capture Audit identity and time are required")
	}
	id, err := captureAuditEventID()
	if err != nil {
		return nil, err
	}
	targetType, targetID := "trace_evidence_operation", in.OperationID
	facts := map[string]any{
		"action": in.Action, "decision": "allowed", "operation_id": in.OperationID,
		"policy_revision": strconv.FormatUint(in.PolicyRevision, 10),
		"desired_state":   in.DesiredState, "effective_state": in.EffectiveState,
		"operation_phase": in.Phase,
	}
	requestContext := map[string]any{"source_channel": "api", "transport": "non_http", "method": "RECONCILE"}
	switch in.EventName {
	case "trace_evidence.configuration_change_requested":
		targetType, targetID = "trace_evidence_configuration", "global"
		facts["changed_fields"] = []string{"desired_state"}
		facts["before_hash"], facts["after_hash"] = in.BeforeHash, in.AfterHash
		requestContext = map[string]any{"source_channel": "api", "transport": "http", "method": "PUT"}
	case "trace_evidence.operation_succeeded", "trace_evidence.rollback_completed":
	case "trace_evidence.operation_failed":
		facts["decision"] = "failed"
	default:
		return nil, fmt.Errorf("unsupported capture Audit event %q", in.EventName)
	}
	actorType := "user"
	if in.ActorType == "service_account" {
		actorType = "service_account"
	}
	event := map[string]any{
		"schema_version": "1.0", "event_id": id, "source_id": "agent-observability",
		"category": "audit.admin", "event_name": in.EventName,
		"occurred_at": in.OccurredAt.UTC().Format(time.RFC3339Nano),
		"actor":       map[string]any{"id": in.ActorID, "type": actorType, "auth_method": "oauth", "effective_subject": in.ActorID},
		"target":      map[string]any{"type": targetType, "id": targetID},
		"outcome":     in.Outcome,
		"scope": map[string]any{"business_module": "observability", "environment": in.Environment,
			"platform_scope": true, "knowledge_network_ids": []string{}},
		"request_context": requestContext,
		"summary":         "trace/evidence capture configuration " + in.Phase,
		"facts":           facts,
	}
	if in.EventName != "trace_evidence.configuration_change_requested" {
		event["actor"] = map[string]any{"id": in.ActorID, "type": "service_account", "auth_method": "internal", "effective_subject": in.ActorID}
	} else {
		event["http_status"] = 202
	}
	if in.EventName == "trace_evidence.operation_failed" {
		failureCode := in.FailureCode
		if failureCode == "" {
			failureCode = "TRACE_EVIDENCE_OPERATION_FAILED"
		}
		event["failure_code"] = failureCode
	}
	return json.Marshal(event)
}

func captureAuditEventID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
}
