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

func TestUnshippedAgentManagementAuditIsNotAdmitted(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range validator.registry.Events {
		if rule.Name == "agent.config.changed" {
			t.Fatal("unshipped Agent management event must not be admitted")
		}
	}
	if observabilityvo.IsRegisteredLogEvent(observabilityvo.CategoryAuditAdmin, "agent.config.changed") {
		t.Fatal("unshipped Agent management event must not appear in public logs")
	}
}

func TestCanonicalAuditFixturesHavePinnedDigestsAndExecutionFactoryIsAdmitted(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for file, expected := range map[string]string{
		"schema.json":                   "d8c7d5e9cdc9ff31c49fcec21b5866ad159bb852d5987a759b24730bda1a6177",
		"registry-runtime-v1.json":      "6c79a89faf36b179fb7fe065a5d718508b9dfb273e65a7ac780b424851f2a816",
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

func TestExecutionFactoryManagementSkillDenialIsAdmitted(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile("assets/execution-factory-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(value, &payload); err != nil {
		t.Fatal(err)
	}
	payload["target"] = map[string]any{"type": "skill", "id": "skill-1", "name": "Skill One"}
	payload["outcome"] = "denied"
	payload["http_status"] = 403
	payload["failure_code"] = "HTTP_403"
	payload["facts"] = map[string]any{"action": "create"}
	payload["summary"] = "execution_factory.operation.observed create skill"
	value, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{
		Topic: auditconsumer.Topic, Key: []byte("execution-factory\x1fskill\x1fskill-1"), Value: value,
		Headers:    []auditconsumer.Header{{Key: "bkn-audit-schema-version", Value: []byte("1.0")}},
		BrokerTime: time.Date(2026, 9, 24, 8, 31, 0, 0, time.UTC),
	}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("execution factory management denial rejected: %v", err)
	}
}

func TestExecutionFactoryCompoundManagementAttemptsAreAdmitted(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile("assets/execution-factory-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		targetType, action, outcome string
		status                      int
	}{
		{"import_batch", "import", "success", 201},
		{"capability_bundle", "create", "unknown", 200},
	} {
		t.Run(scenario.targetType, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal(value, &payload); err != nil {
				t.Fatal(err)
			}
			id := scenario.targetType + ":req-1"
			payload["target"] = map[string]any{"type": scenario.targetType, "id": id}
			payload["facts"] = map[string]any{"action": scenario.action}
			payload["summary"] = "execution_factory.operation.observed " + scenario.action + " " + scenario.targetType
			payload["outcome"] = scenario.outcome
			payload["http_status"] = scenario.status
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			record := auditconsumer.Record{
				Topic: auditconsumer.Topic, Key: []byte("execution-factory\x1f" + scenario.targetType + "\x1f" + id),
				Value: encoded, Headers: []auditconsumer.Header{{Key: "bkn-audit-schema-version", Value: []byte("1.0")}},
				BrokerTime: time.Date(2026, 9, 24, 8, 31, 0, 0, time.UTC),
			}
			if _, err := validator.Validate(context.Background(), record); err != nil {
				t.Fatalf("compound management attempt rejected: %v", err)
			}
		})
	}
}

func TestExecutionFactoryPrivateCategoryAttemptIsAdmittedWithoutClaimedIdentity(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile("assets/execution-factory-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(value, &payload); err != nil {
		t.Fatal(err)
	}
	payload["actor"] = map[string]any{"id": "anonymous", "type": "anonymous", "auth_method": "unknown", "effective_subject": "anonymous"}
	payload["target"] = map[string]any{"type": "operator_category", "id": "operator_category:req-private-1"}
	payload["facts"] = map[string]any{"action": "update"}
	payload["summary"] = "execution_factory.operation.observed update operator_category"
	payload["request_context"].(map[string]any)["source_channel"] = "unknown"
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{
		Topic:      auditconsumer.Topic,
		Key:        []byte("execution-factory\x1foperator_category\x1foperator_category:req-private-1"),
		Value:      encoded,
		Headers:    []auditconsumer.Header{{Key: "bkn-audit-schema-version", Value: []byte("1.0")}},
		BrokerTime: time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC),
	}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("private category attempt rejected: %v", err)
	}
}

func TestValidatorAcceptsRegisteredKafkaAuditSource(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bkn-safe-access", "execution-factory", "agent-observability", "vega"} {
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

func TestSafeAccessKafkaPayloadIsAdmitted(t *testing.T) {
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
	value["event_id"] = "0199196f-e7f3-7c7a-91f6-c4ad242d0db7"
	value["source_id"] = "bkn-safe-access"
	value["category"] = "access.user"
	value["event_name"] = "login.failed"
	value["occurred_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	value["actor"] = map[string]any{"id": "anonymous", "effective_subject": "anonymous", "type": "anonymous", "auth_method": "password"}
	value["target"] = map[string]any{"type": "session", "id": "session:0199196f-e7f3-7c7a-91f6-c4ad242d0db7"}
	value["outcome"] = "failure"
	value["scope"] = map[string]any{"business_module": "system_management", "environment": "test", "platform_scope": true, "knowledge_network_ids": []string{}}
	value["request_context"] = map[string]any{"source_channel": "unknown", "transport": "http", "method": "POST"}
	value["correlation"] = map[string]any{}
	value["summary"] = "safe access login.failed"
	value["facts"] = map[string]any{"action": "login", "result": "failure"}
	value["failure_code"] = "INVALID_CREDENTIALS"
	content, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{
		Topic:      auditconsumer.Topic,
		Key:        []byte("bkn-safe-access\x1fsession\x1fsession:0199196f-e7f3-7c7a-91f6-c4ad242d0db7"),
		Value:      content,
		Headers:    []auditconsumer.Header{{Key: SchemaHeader, Value: []byte(SchemaVersion)}},
		BrokerTime: time.Now().UTC(),
	}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("Safe Access Kafka audit payload rejected: %v", err)
	}
}

func TestSafeSecurityDecisionWithoutUnobservablePolicyRevisionIsAdmitted(t *testing.T) {
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
	value["event_id"] = "0199196f-e7f3-7c7a-91f6-c4ad242d0db8"
	value["source_id"] = "bkn-safe-security"
	value["category"] = "audit.security"
	value["event_name"] = "authorization.decided"
	value["occurred_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	value["actor"] = map[string]any{"id": "anonymous", "effective_subject": "anonymous", "type": "anonymous", "auth_method": "unknown"}
	value["target"] = map[string]any{"type": "authorization_decision", "id": "decision:0199196f-e7f3-7c7a-91f6-c4ad242d0db8"}
	value["outcome"] = "denied"
	delete(value, "http_status")
	value["scope"] = map[string]any{"business_module": "system_management", "environment": "test", "platform_scope": true, "knowledge_network_ids": []string{}}
	value["request_context"] = map[string]any{"source_channel": "api", "transport": "http", "method": "POST"}
	value["correlation"] = map[string]any{"request_id": "req-safe-security-test"}
	value["summary"] = "safe authorization decision"
	value["facts"] = map[string]any{"action": "check", "decision": "deny", "resource_scope": "knowledge_network:kn-1"}
	value["failure_code"] = "AUTHZ_DENIED"
	content, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{Topic: auditconsumer.Topic, Key: []byte("bkn-safe-security\x1fauthorization_decision\x1fdecision:0199196f-e7f3-7c7a-91f6-c4ad242d0db8"), Value: content, BrokerTime: time.Now().UTC(), Headers: []auditconsumer.Header{{Key: SchemaHeader, Value: []byte(SchemaVersion)}}}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("Safe Security decision rejected: %v", err)
	}
}

func TestModelManagerManagementAttemptIsAdmitted(t *testing.T) {
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
	value["source_id"] = "model-manager"
	value["event_name"] = "model_manager.operation.observed"
	value["occurred_at"] = time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	value["target"] = map[string]any{"type": "llm_model", "id": "model-audit-e2e"}
	value["scope"] = map[string]any{
		"business_module": "model_management", "environment": "test",
		"platform_scope": true, "knowledge_network_ids": []string{},
	}
	value["request_context"] = map[string]any{"source_channel": "api", "transport": "http", "method": "POST"}
	value["facts"] = map[string]any{"action": "update", "decision": "allowed"}
	content, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{
		Topic: auditconsumer.Topic, Key: []byte("model-manager\x1fllm_model\x1fmodel-audit-e2e"),
		Value: content, BrokerTime: time.Now().UTC(),
		Headers: []auditconsumer.Header{{Key: SchemaHeader, Value: []byte(SchemaVersion)}},
	}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("registered Model Manager Audit must pass Writer validation: %v", err)
	}
}

func TestVegaManagementAuditRecordIsAdmittedAfterCutover(t *testing.T) {
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
	value["source_id"] = "vega"
	value["event_name"] = "vega.operation.observed"
	value["occurred_at"] = time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	value["target"] = map[string]any{"type": "catalog", "id": "catalog-audit-e2e"}
	value["scope"] = map[string]any{
		"business_module": "data_resource_knowledge_network", "environment": "test",
		"platform_scope": true, "knowledge_network_ids": []string{},
	}
	value["request_context"] = map[string]any{"source_channel": "api", "transport": "http", "method": "PUT"}
	value["facts"] = map[string]any{"action": "create", "decision": "allowed", "changed_fields": []string{"name"}}
	content, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{
		Topic: auditconsumer.Topic, Key: []byte("vega\x1fcatalog\x1fcatalog-audit-e2e"),
		Value: content, BrokerTime: time.Now().UTC(),
		Headers: []auditconsumer.Header{{Key: SchemaHeader, Value: []byte(SchemaVersion)}},
	}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("registered Vega management Audit must pass Writer validation: %v", err)
	}
	value["target"] = map[string]any{"type": "catalog", "id": "bak_abcdefghijkl", "name": "bak_abcdefghijkl"}
	value["facts"] = map[string]any{"action": "create", "decision": "allowed", "changed_fields": []string{"bak_abcdefghijkl"}}
	record.Value, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	record.Key = []byte("vega\x1fcatalog\x1fbak_abcdefghijkl")
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("Writer must preserve a valid target identifier regardless of prefix: %v", err)
	}
	digest := sha256.Sum256([]byte("bak_abcdefghijkl"))
	alias := "sha256:" + hex.EncodeToString(digest[:])
	value["target"] = map[string]any{"type": "catalog", "id": alias, "name": alias}
	value["facts"] = map[string]any{"action": "create", "decision": "allowed", "changed_fields": []string{}}
	record.Value, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	record.Key = []byte("vega\x1fcatalog\x1f" + alias)
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("sanitized Vega target must pass Writer validation: %v", err)
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

func TestSafeAdminObservedOperationRequiresKafkaSource(t *testing.T) {
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
	value["source_id"] = "bkn-safe-admin"
	value["event_name"] = "safe.admin.operation.observed"
	value["target"] = map[string]any{"type": "role", "id": "role-safe-e2e"}
	value["scope"] = map[string]any{"business_module": "system_management", "environment": "test", "platform_scope": true, "knowledge_network_ids": []string{}}
	value["facts"] = map[string]any{"action": "create", "decision": "allowed"}
	value["summary"] = "safe.admin.operation.observed create role"
	value["occurred_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	content, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{Topic: auditconsumer.Topic, Key: []byte("bkn-safe-admin\x1frole\x1frole-safe-e2e"), Value: content, BrokerTime: time.Now().UTC(), Headers: []auditconsumer.Header{{Key: SchemaHeader, Value: []byte(SchemaVersion)}}}
	for i := range validator.registry.Sources {
		if validator.registry.Sources[i].ID == "bkn-safe-admin" {
			validator.registry.Sources[i].CollectionMethod = "source_adapter"
		}
	}
	if _, err := validator.Validate(context.Background(), record); !IsPermanentReason(err, "source_collection_method_rejected") {
		t.Fatalf("unmigrated Safe source must be rejected: %v", err)
	}
	for i := range validator.registry.Sources {
		if validator.registry.Sources[i].ID == "bkn-safe-admin" {
			validator.registry.Sources[i].CollectionMethod = "kafka_audit"
		}
	}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("Safe admin event should be admitted after truthful cutover: %v", err)
	}
}

func TestSafeNonHTTPLicenseFailureIsAdmittedWithoutInventedRequest(t *testing.T) {
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
	value["source_id"] = "bkn-safe-admin"
	value["event_name"] = "safe.admin.operation.observed"
	value["actor"] = map[string]any{"id": "system:license", "effective_subject": "system:license", "type": "service_account", "auth_method": "unknown"}
	value["target"] = map[string]any{"type": "license", "id": "license:cluster"}
	value["scope"] = map[string]any{"business_module": "system_management", "environment": "test", "platform_scope": true, "knowledge_network_ids": []string{}}
	value["outcome"] = "failure"
	value["failure_code"] = "LICENSE_RENEW_FAILED"
	delete(value, "http_status")
	value["request_context"] = map[string]any{"source_channel": "unknown", "transport": "non_http", "method": "SYSTEM"}
	value["correlation"] = map[string]any{}
	value["facts"] = map[string]any{"action": "license_renew_failed", "decision": "failed"}
	value["summary"] = "safe.admin.operation.observed license.renew-failed license"
	value["occurred_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	content, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	record := auditconsumer.Record{Topic: auditconsumer.Topic, Key: []byte("bkn-safe-admin\x1flicense\x1flicense:cluster"), Value: content, BrokerTime: time.Now().UTC(), Headers: []auditconsumer.Header{{Key: SchemaHeader, Value: []byte(SchemaVersion)}}}
	if _, err := validator.Validate(context.Background(), record); err != nil {
		t.Fatalf("truthful Safe system failure should reach Ledger: %v", err)
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
