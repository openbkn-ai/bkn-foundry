// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package auditvalidator

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditconsumer"
)

func TestPublicLogEventAllowlistMatchesEmbeddedAuditRegistry(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range validator.registry.Events {
		if !observabilityvo.IsRegisteredLogEvent(rule.Category, rule.Name) {
			t.Errorf("Audit event %s is admitted by Kafka but hidden from public logs", rule.Name)
		}
	}
}

func TestCanonicalAuditFixturesHavePinnedDigestsAndExecutionFactoryIsAdmitted(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for file, expected := range map[string]string{
		"schema.json":                   "4b1db1b116485e1b0432635406bcdffdc111be1b7cc583714a6a2c867efee69b",
		"registry-runtime-v1.json":      "0a07a364556ec51dcec6cc98c793a728a52fb4e9b8f3b228f2a54f6c6521ab65",
		"audit-record-golden.json":      "2976cc4822bc9a9248b1aa66de29916a35fcb9988b61a313d6e86fc68c17ce40",
		"audit-kafka-golden.json":       "6ca65bf73f3345964d6a70eb95c3405e7145ebc64848aceabf16472057538dd4",
		"execution-factory-golden.json": "2f39af3735b13f96b8d3205dfd584974ed5c2ce5d53e7458039a9e4234d757d0",
	} {
		content, err := os.ReadFile("assets/" + file)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != expected {
			t.Fatalf("%s digest mismatch", file)
		}
	}
	value, err := os.ReadFile("assets/execution-factory-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{
		Topic: auditconsumer.Topic, Key: []byte("execution-factory\x1foperator\x1foperator-123"), Value: value,
		Headers:    []auditconsumer.Header{{Key: "bkn-audit-schema-version", Value: []byte("1.0")}},
		BrokerTime: time.Date(2026, 9, 24, 8, 31, 0, 0, time.UTC),
	}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("registered execution-factory Audit source must be admitted; got %v", err)
	}
}

func TestValidatorAcceptsRegisteredKafkaAuditSource(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"execution-factory", "agent-observability"} {
		source, found := findSource(validator.registry.Sources, id)
		if !found || source.CollectionMethod != "kafka_audit" {
			t.Fatalf("%s must be registered as kafka_audit, got %+v, found=%v", id, source, found)
		}
	}
	value, err := os.ReadFile("assets/execution-factory-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{
		Topic: auditconsumer.Topic,
		Key:   []byte("execution-factory\x1foperator\x1foperator-123"),
		Value: value,
		Headers: []auditconsumer.Header{{
			Key: "bkn-audit-schema-version", Value: []byte("1.0"),
		}},
		BrokerTime: time.Date(2026, 9, 24, 8, 30, 0, 0, time.UTC).Add(auditstore.MaxAcceptedOccurredAtAge),
	}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("registered kafka_audit event at the maximum accepted age must be accepted: %v", err)
	}
	record.BrokerTime = record.BrokerTime.Add(time.Nanosecond)
	if _, err := validator.Validate(context.Background(), record); !IsPermanentReason(err, "retention_expired") {
		t.Fatalf("event older than the maximum accepted age must be rejected: %v", err)
	}
}

func TestValidatorRejectsNonKafkaAuditCollectionMethods(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile("assets/audit-record-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	if err := validateRegistry(value, validator.registry); err != nil {
		t.Fatalf("registered Backend Kafka source must be accepted, got %v", err)
	}

	for _, tc := range []struct {
		method string
		want   string
	}{
		{method: "source_adapter", want: "source_collection_method_rejected"},
		{method: "direct_otlp", want: "source_collection_method_rejected"},
		{method: "container_stdout", want: "source_collection_method_rejected"},
		{method: "not_integrated", want: "source_not_integrated"},
		{method: "", want: "source_collection_method_rejected"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			rules := validator.registry
			for i := range rules.Sources {
				if rules.Sources[i].ID == "bkn-backend" {
					rules.Sources[i].CollectionMethod = tc.method
				}
			}
			if err := validateRegistry(value, rules); !IsPermanentReason(err, tc.want) {
				t.Fatalf("collection method %q must be rejected as %s, got %v", tc.method, tc.want, err)
			}
		})
	}
}

func TestBackendObservedOperationIsAdmittedOnlyAfterSourceCutover(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile("assets/audit-record-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	value["event_name"] = "backend.operation.observed"
	value["occurred_at"] = time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	value["target"] = map[string]any{"type": "kn_capability_binding", "id": "binding-1"}
	value["facts"] = map[string]any{"action": "attach", "decision": "allowed", "changed_fields": []string{"entries"}}
	content, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{
		Topic: auditconsumer.Topic, Key: []byte("bkn-backend\x1fkn_capability_binding\x1fbinding-1"),
		Value: content, BrokerTime: time.Now().UTC(),
		Headers: []auditconsumer.Header{{Key: SchemaHeader, Value: []byte(SchemaVersion)}},
	}
	for i := range validator.registry.Sources {
		if validator.registry.Sources[i].ID == "bkn-backend" {
			validator.registry.Sources[i].CollectionMethod = "source_adapter"
		}
	}
	if _, err := validator.Validate(context.Background(), record); !IsPermanentReason(err, "source_collection_method_rejected") {
		t.Fatalf("unmigrated Backend source must remain blocked: %v", err)
	}
	for i := range validator.registry.Sources {
		if validator.registry.Sources[i].ID == "bkn-backend" {
			validator.registry.Sources[i].CollectionMethod = "kafka_audit"
		}
	}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("registered Backend operation must pass Writer validation after cutover: %v", err)
	}
}

func TestValidatorRejectsOversizedAndWrongHeaderPermanently(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []auditconsumer.Record{
		{Topic: auditconsumer.Topic, Value: make([]byte, 32*1024+1), BrokerTime: time.Now()},
		{Topic: auditconsumer.Topic, Value: []byte(`{}`), Headers: []auditconsumer.Header{{Key: "wrong", Value: []byte("1.0")}}, BrokerTime: time.Now()},
	} {
		if _, err := validator.Validate(context.Background(), record); err == nil || !IsPermanent(err) {
			t.Fatalf("expected permanent validation rejection, got %v", err)
		}
	}
}

func TestValidatorTreatsMissingBrokerTimestampAsTemporary(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{Topic: auditconsumer.Topic}
	if _, err := validator.Validate(context.Background(), record); err == nil || IsPermanent(err) {
		t.Fatalf("missing Kafka broker timestamp must remain temporary: %v", err)
	}
}

func TestRegistryRejectsEnvironmentOutsideSourceAllowlist(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile("assets/audit-record-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	source, found := findSource(validator.registry.Sources, "bkn-backend")
	if !found {
		t.Fatal("golden fixture source is absent from runtime registry")
	}
	source.CollectionMethod = "kafka_audit"
	source.AllowedEnvironments = []string{"staging"}
	for i := range validator.registry.Sources {
		if validator.registry.Sources[i].ID == source.ID {
			validator.registry.Sources[i] = source
			break
		}
	}
	if err := validateRegistry(value, validator.registry); !IsPermanentReason(err, "source_environment_rejected") {
		t.Fatalf("source not allowing production must be permanently rejected, got %v", err)
	}
}

func TestValidatorCarriesKafkaCoordinateIntoAuthoritativeLedgerEvent(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for i := range validator.registry.Sources {
		if validator.registry.Sources[i].ID == "bkn-backend" {
			validator.registry.Sources[i].CollectionMethod = "kafka_audit"
		}
	}
	value, err := os.ReadFile("assets/audit-record-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	kafkaFixture, err := os.ReadFile("assets/audit-kafka-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Key string `json:"key_base64"`
	}
	if err := json.Unmarshal(kafkaFixture, &fixture); err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(fixture.Key)
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{
		Topic: auditconsumer.Topic, Key: key, Value: value, Partition: 3, Offset: 9,
		Headers:    []auditconsumer.Header{{Key: "bkn-audit-schema-version", Value: []byte("1.0")}},
		BrokerTime: time.Date(2026, 9, 24, 8, 31, 0, 0, time.UTC),
	}
	event, err := validator.Validate(context.Background(), record)
	if err != nil {
		t.Fatalf("validate canonical Audit fixture: %v", err)
	}
	want := auditstore.KafkaCoordinate{Topic: auditconsumer.Topic, Partition: 3, Offset: 9}
	if event.Kafka != want {
		t.Fatalf("ledger Kafka coordinate = %+v, want %+v", event.Kafka, want)
	}
}
