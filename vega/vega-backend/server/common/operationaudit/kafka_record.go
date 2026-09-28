// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

var vegaAuditTargetTypes = map[string]string{
	"catalog": "catalog", "resource": "resource",
	"catalog_health_check_schedule": "catalog",
	"discover_schedule":             "discovery_schedule", "index_task": "build_task",
	"connector_type": "connector_type", "discover_task": "discovery_task",
	"semantic_understanding_task": "semantic_understanding_task",
}

var vegaAuditActions = map[string]struct{}{
	"create": {}, "update": {}, "delete": {}, "enable": {}, "disable": {}, "start": {}, "stop": {},
}

var vegaAuditOutcomes = map[string]struct{}{"success": {}, "failure": {}, "denied": {}}

// InvalidAuditFieldError names a dropped field without exposing its value.
type InvalidAuditFieldError struct {
	Field  string
	Reason string
}

func (e *InvalidAuditFieldError) Error() string {
	return fmt.Sprintf("Audit field=%s %s", e.Field, e.Reason)
}

// BuildKafkaAuditRecord maps one Vega management attempt to the Audit v1
// observation event. It excludes request/response bodies and connector credentials.
func BuildKafkaAuditRecord(entry Entry, environment string) ([]byte, error) {
	if !validAuditEnvironment(environment) {
		return nil, errors.New("invalid Audit environment")
	}
	targetType, ok := vegaAuditTargetTypes[entry.TargetType]
	if !ok {
		return nil, &InvalidAuditFieldError{Field: "target_type", Reason: "unregistered"}
	}
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"event_id", entry.EventID, 128},
		{"actor_id", entry.ActorID, 128},
		{"target_id", entry.TargetID, 256},
		{"request_id", entry.RequestID, 128},
	} {
		if strings.TrimSpace(field.value) == "" {
			return nil, &InvalidAuditFieldError{Field: field.name, Reason: "missing"}
		}
		if len(field.value) > field.limit {
			return nil, &InvalidAuditFieldError{Field: field.name, Reason: "oversized"}
		}
	}
	if entry.EventTime.IsZero() {
		return nil, &InvalidAuditFieldError{Field: "event_time", Reason: "missing"}
	}
	if _, ok := vegaAuditActions[entry.Action]; !ok {
		return nil, &InvalidAuditFieldError{Field: "action", Reason: "unregistered"}
	}
	if _, ok := vegaAuditOutcomes[entry.Outcome]; !ok {
		return nil, &InvalidAuditFieldError{Field: "outcome", Reason: "invalid"}
	}
	if entry.HTTPStatus < 100 || entry.HTTPStatus > 599 {
		return nil, &InvalidAuditFieldError{Field: "http_status", Reason: "invalid"}
	}
	changedFields := entry.ChangedFields
	if changedFields == nil {
		changedFields = []string{}
	}
	if len(changedFields) > 100 {
		return nil, &InvalidAuditFieldError{Field: "changed_fields", Reason: "oversized"}
	}
	seen := make(map[string]struct{}, len(changedFields))
	for _, field := range changedFields {
		if field == "" || len(field) > 128 {
			return nil, &InvalidAuditFieldError{Field: "changed_fields", Reason: "invalid"}
		}
		if _, duplicate := seen[field]; duplicate {
			return nil, &InvalidAuditFieldError{Field: "changed_fields", Reason: "duplicate"}
		}
		seen[field] = struct{}{}
	}
	actorType := "service_account"
	if entry.ActorType == "user" {
		actorType = "user"
	}
	decision := "allowed"
	if entry.Outcome == "denied" {
		decision = "denied"
	}
	method := strings.ToUpper(strings.TrimSpace(entry.Method))
	if method == "" || len(method) > 16 {
		method = "UNKNOWN"
	}
	authMethod := entry.AuthMethod
	if authMethod == "" || len(authMethod) > 64 {
		authMethod = "unknown"
	}
	// The frozen Audit contract classifies both public and internal HTTP routes
	// as API. Internal identity provenance is carried by actor.auth_method.
	sourceChannel := "api"
	record := map[string]any{
		"schema_version": auditpublisher.SchemaVersion,
		"event_id":       uuid.NewSHA1(uuid.NameSpaceURL, []byte(entry.EventID)).String(),
		"source_id":      "vega",
		"category":       "audit.admin",
		"event_name":     "vega.operation.observed",
		"occurred_at":    entry.EventTime.UTC().Format(time.RFC3339Nano),
		"actor": map[string]any{
			"id": entry.ActorID, "effective_subject": entry.ActorID,
			"type": actorType, "auth_method": authMethod,
		},
		"target":  map[string]any{"type": targetType, "id": entry.TargetID},
		"outcome": entry.Outcome, "http_status": entry.HTTPStatus,
		"scope": map[string]any{
			"business_module": "data_resource_knowledge_network", "environment": environment,
			"platform_scope": true, "knowledge_network_ids": []string{},
		},
		"request_context": map[string]any{
			"source_channel": sourceChannel, "transport": "http", "method": method,
		},
		"correlation": map[string]any{"request_id": entry.RequestID},
		"summary":     entry.Action + " " + targetType,
		"facts": map[string]any{
			"action": entry.Action, "decision": decision, "changed_fields": changedFields,
		},
	}
	if entry.Outcome == "failure" || entry.Outcome == "denied" {
		record["failure_code"] = fmt.Sprintf("HTTP_%d", entry.HTTPStatus)
	}
	if name := strings.TrimSpace(entry.ActorName); name != "" && len(name) <= 256 {
		record["actor"].(map[string]any)["display_name_snapshot"] = name
	}
	if name := strings.TrimSpace(entry.TargetName); name != "" && len(name) <= 512 {
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

func validAuditEnvironment(value string) bool {
	switch value {
	case "development", "test", "staging", "production":
		return true
	default:
		return false
	}
}
