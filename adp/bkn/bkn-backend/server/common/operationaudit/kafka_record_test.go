// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

func TestBuildKafkaAuditRecordMapsRealManagementFact(t *testing.T) {
	entry := Entry{
		EventID: "evt_stable_request_fact", EventTime: time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC),
		KnowledgeNetworkID: "kn-123", ActorID: "user-a", ActorName: "User A", ActorType: "user",
		AuthMethod: "oauth", RequestID: "req-123", SourceChannel: "api", Method: "PUT",
		HTTPStatus: 200,
		Action:     "update", TargetType: "knowledge_network", TargetID: "kn-123", TargetName: "Customer",
		Outcome: "success", ChangeSummary: map[string]any{"changed_fields": []string{"name"}},
	}
	value, err := BuildKafkaAuditRecord(entry, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auditpublisher.BuildRecord(value); err != nil {
		t.Fatalf("Kafka Audit SDK rejected record: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(record["event_id"].(string)); err != nil {
		t.Fatalf("event_id is not a UUID: %v", err)
	}
	if record["source_id"] != "bkn-backend" || record["event_name"] != "backend.operation.observed" || record["outcome"] != "success" || record["http_status"] != float64(200) {
		t.Fatalf("wrong Kafka Audit identity: %#v", record)
	}
	scope := record["scope"].(map[string]any)
	if scope["business_module"] != "domain_knowledge_network" || scope["platform_scope"] != false {
		t.Fatalf("wrong business scope: %#v", scope)
	}
	actor := record["actor"].(map[string]any)
	if actor["effective_subject"] != "user-a" || actor["type"] != "user" {
		t.Fatalf("wrong actor: %#v", actor)
	}
	facts := record["facts"].(map[string]any)
	if facts["action"] != "update" || facts["decision"] != "allowed" {
		t.Fatalf("wrong facts: %#v", facts)
	}
	if _, exists := facts["result"]; exists {
		t.Fatalf("facts.result is not allowed by Audit v1 schema: %#v", facts)
	}
}

func TestBuildKafkaAuditRecordRejectsUnknownEnvironmentAndBindingTarget(t *testing.T) {
	entry := Entry{EventID: "evt-1", EventTime: time.Now().UTC(), KnowledgeNetworkID: "kn-1",
		ActorID: "user-a", ActorName: "User A", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-1", SourceChannel: "api", Method: "POST", Action: "attach",
		TargetType: "kn_capability_binding", TargetID: "binding-1", Outcome: "success",
		ChangeSummary: map[string]any{"changed_fields": []string{"entries"}},
	}
	entry.HTTPStatus = 201
	if _, err := BuildKafkaAuditRecord(entry, "test"); err != nil {
		t.Fatalf("registered binding operation must be publishable: %v", err)
	}
	entry.TargetType = "knowledge_network"
	if _, err := BuildKafkaAuditRecord(entry, "unknown-environment"); err == nil {
		t.Fatal("unknown deployment environment must not produce an invalid record")
	}
	entry.ActorID = strings.Repeat("a", 129)
	if _, err := BuildKafkaAuditRecord(entry, "test"); err != nil {
		t.Fatalf("oversized identity should be aliased without dropping the Audit: %v", err)
	}
}

func TestBuildKafkaAuditRecordIncludesControlledFailureCode(t *testing.T) {
	entry := Entry{EventID: "evt-failure", EventTime: time.Now().UTC(),
		ActorID: "user-a", RequestID: "req-failure", Method: "DELETE", HTTPStatus: 503,
		Action: "delete", TargetType: "knowledge_network", TargetID: "kn-1", Outcome: "failure",
		FailureCode: "Bad Gateway", ChangeSummary: map[string]any{"changed_fields": []string{}},
	}
	value, err := BuildKafkaAuditRecord(entry, "test")
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	if record["failure_code"] != "HTTP_503" || record["http_status"] != float64(503) {
		t.Fatalf("failure facts are not bounded: %#v", record)
	}
}

func TestBuildKafkaAuditRecordNamesInvalidIdentityFieldWithoutValue(t *testing.T) {
	base := Entry{EventID: "evt-1", EventTime: time.Now().UTC(), ActorID: "user-a",
		RequestID: "req-1", Method: "POST", HTTPStatus: 201, Action: "create",
		TargetType: "knowledge_network", TargetID: "kn-1", Outcome: "success",
		ChangeSummary: map[string]any{"changed_fields": []string{}},
	}
	for _, tc := range []struct {
		name, field string
		change      func(*Entry)
	}{
		{"missing event", "event_id", func(e *Entry) { e.EventID = "" }},
		{"missing time", "event_time", func(e *Entry) { e.EventTime = time.Time{} }},
		{"missing actor", "actor_id", func(e *Entry) { e.ActorID = "" }},
		{"missing target", "target_id", func(e *Entry) { e.TargetID = "" }},
		{"missing request", "request_id", func(e *Entry) { e.RequestID = "" }},
		{"oversized network", "knowledge_network_id", func(e *Entry) { e.KnowledgeNetworkID = strings.Repeat("s", 129) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := base
			tc.change(&entry)
			_, err := BuildKafkaAuditRecord(entry, "test")
			var fieldErr *InvalidAuditFieldError
			if !errors.As(err, &fieldErr) || fieldErr.Field != tc.field {
				t.Fatalf("got %v, want field %s", err, tc.field)
			}
			if strings.Contains(err.Error(), "ssss") {
				t.Fatalf("invalid value leaked in error: %v", err)
			}
		})
	}
}

func TestBuildKafkaAuditRecordAliasesOversizedUntrustedReferences(t *testing.T) {
	longID := strings.Repeat("x", 300)
	entry := Entry{EventID: "evt-long-ref", EventTime: time.Now().UTC(), ActorID: longID,
		RequestID: longID, Method: "POST", HTTPStatus: 201, Action: "create",
		TargetType: "knowledge_network", TargetID: longID, Outcome: "success",
		ChangeSummary: map[string]any{"changed_fields": []string{}},
	}
	value, err := BuildKafkaAuditRecord(entry, "test")
	if err != nil {
		t.Fatalf("valid operation must not lose its whole Audit due to long IDs: %v", err)
	}
	if strings.Contains(string(value), longID) {
		t.Fatal("oversized caller-controlled ID leaked instead of a bounded alias")
	}
	if _, err := auditpublisher.BuildRecord(value); err != nil {
		t.Fatal(err)
	}
}

func TestBuildKafkaAuditRecordPreservesCallerTextWithoutGuessingSecrets(t *testing.T) {
	entry := Entry{EventID: "evt-source-redaction", EventTime: time.Now().UTC(),
		ActorID: "user-a", ActorName: "Bearer abcdefghijklmnop", RequestID: "req-1",
		Method: "POST", HTTPStatus: 201, Action: "create", TargetType: "knowledge_network",
		TargetID: "kn-1", TargetName: "bak_123456789012_abcdefghijklmnopqrstuvwxyz1",
		Outcome: "success", ChangeSummary: map[string]any{"changed_fields": []string{}},
	}
	value, err := BuildKafkaAuditRecord(entry, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(value), "Bearer abcdefghijklmnop") || !strings.Contains(string(value), "bak_123456789012_abcdefghijklmnopqrstuvwxyz1") {
		t.Fatal("source rewrote valid audit display text based on content")
	}
}
