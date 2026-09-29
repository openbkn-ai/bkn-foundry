// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

const kafkaAuditSourceID = "bkn-backend"

// InvalidAuditFieldError identifies a dropped field without exposing its value.
type InvalidAuditFieldError struct {
	Field  string
	Reason string
}

func (e *InvalidAuditFieldError) Error() string {
	return fmt.Sprintf("Audit field=%s %s", e.Field, e.Reason)
}

// BuildKafkaAuditRecord maps one bounded management fact to the frozen Audit
// v1 record. It does not publish or mutate the old local audit table.
func BuildKafkaAuditRecord(entry Entry, environment string) ([]byte, error) {
	if !validAuditEnvironment(environment) {
		return nil, fmt.Errorf("invalid Audit environment %q", environment)
	}
	if _, ok := allowedTargetTypes[entry.TargetType]; !ok {
		return nil, fmt.Errorf("unregistered Audit target type %q", entry.TargetType)
	}
	actorID := boundedAuditReference(entry.ActorID, 128)
	targetID := boundedAuditReference(entry.TargetID, 256)
	requestID := boundedAuditReference(entry.RequestID, 128)
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"event_id", entry.EventID, 0},
		{"actor_id", actorID, 128},
		{"target_id", targetID, 256},
		{"request_id", requestID, 128},
		{"knowledge_network_id", entry.KnowledgeNetworkID, 128},
	} {
		if field.value == "" && field.name != "knowledge_network_id" {
			return nil, &InvalidAuditFieldError{Field: field.name, Reason: "missing"}
		}
		if field.limit > 0 && len(field.value) > field.limit {
			return nil, &InvalidAuditFieldError{Field: field.name, Reason: "oversized"}
		}
	}
	if entry.EventTime.IsZero() {
		return nil, &InvalidAuditFieldError{Field: "event_time", Reason: "missing"}
	}
	if _, ok := allowedActions[entry.Action]; !ok {
		return nil, fmt.Errorf("unregistered Audit action %q", entry.Action)
	}
	if _, ok := allowedOutcomes[entry.Outcome]; !ok {
		return nil, fmt.Errorf("invalid Audit outcome %q", entry.Outcome)
	}
	if entry.HTTPStatus < 100 || entry.HTTPStatus > 599 {
		return nil, errors.New("Audit HTTP status is missing or invalid")
	}
	changedFields, ok := entry.ChangeSummary["changed_fields"].([]string)
	if !ok {
		return nil, errors.New("Audit fact has no bounded changed_fields")
	}
	if len(changedFields) > 100 {
		return nil, errors.New("Audit changed_fields exceeds schema limit")
	}
	seen := make(map[string]struct{}, len(changedFields))
	for _, field := range changedFields {
		if field == "" || len(field) > 128 {
			return nil, errors.New("Audit changed_fields contains an invalid field")
		}
		if _, duplicate := seen[field]; duplicate {
			return nil, errors.New("Audit changed_fields contains a duplicate")
		}
		seen[field] = struct{}{}
	}
	actorType := "service_account"
	if entry.ActorType == "user" {
		actorType = "user"
	} else if actorID == "unauthenticated" {
		actorType = "anonymous"
	}
	authMethod := entry.AuthMethod
	if authMethod == "" || len(authMethod) > 64 {
		authMethod = "unknown"
	}
	decision := "allowed"
	if entry.Outcome == "denied" {
		decision = "denied"
	}
	scopeIDs := []string{}
	if entry.KnowledgeNetworkID != "" {
		scopeIDs = append(scopeIDs, entry.KnowledgeNetworkID)
	}
	method := strings.ToUpper(strings.TrimSpace(entry.Method))
	if method == "" || len(method) > 16 {
		method = "UNKNOWN"
	}
	record := map[string]any{
		"schema_version": auditpublisher.SchemaVersion,
		"event_id":       uuid.NewSHA1(uuid.NameSpaceURL, []byte(entry.EventID)).String(),
		"source_id":      kafkaAuditSourceID,
		"category":       "audit.admin",
		"event_name":     "backend.operation.observed",
		"occurred_at":    entry.EventTime.UTC().Format(time.RFC3339Nano),
		"actor": map[string]any{
			"id": actorID, "effective_subject": actorID,
			"type": actorType, "auth_method": authMethod,
		},
		"target":      map[string]any{"type": entry.TargetType, "id": targetID},
		"outcome":     entry.Outcome,
		"http_status": entry.HTTPStatus,
		"scope": map[string]any{
			"business_module": "domain_knowledge_network", "environment": environment,
			"platform_scope": len(scopeIDs) == 0, "knowledge_network_ids": scopeIDs,
		},
		"request_context": map[string]any{
			"source_channel": "api", "transport": "http", "method": method,
		},
		"correlation": map[string]any{"request_id": requestID},
		"summary":     entry.Action + " " + entry.TargetType,
		"facts": map[string]any{
			"action": entry.Action, "decision": decision, "changed_fields": changedFields,
		},
	}
	if entry.Outcome == "failure" || entry.Outcome == "denied" {
		record["failure_code"] = fmt.Sprintf("HTTP_%d", entry.HTTPStatus)
	}
	if name := safeAuditDisplayName(entry.ActorName, 256); name != "" {
		record["actor"].(map[string]any)["display_name_snapshot"] = name
	}
	if name := safeAuditDisplayName(entry.TargetName, 512); name != "" {
		record["target"].(map[string]any)["name"] = name
	}
	value, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if _, err := auditpublisher.BuildRecord(value); err != nil {
		return nil, fmt.Errorf("invalid Kafka Audit record: %w", err)
	}
	return value, nil
}

func boundedAuditReference(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return "ref_" + hex.EncodeToString(sum[:])
}

func safeAuditDisplayName(value string, limit int) string {
	name := strings.TrimSpace(value)
	if len(name) > limit {
		return ""
	}
	return name
}

func validAuditEnvironment(value string) bool {
	switch value {
	case "development", "test", "staging", "production":
		return true
	default:
		return false
	}
}
