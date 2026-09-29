package decisionlog

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

// BuildKafkaRecord projects an actual Safe authorization decision. Safe has
// no trustworthy policy revision today, so none is claimed by this event.
func BuildKafkaRecord(entry Entry, environment string) ([]byte, error) {
	switch environment {
	case "development", "test", "staging", "production":
	default:
		return nil, errors.New("invalid Safe security environment")
	}
	if entry.Decision != DecisionAllow && entry.Decision != DecisionDeny && entry.Decision != DecisionNone {
		return nil, errors.New("unrecognized Safe security decision")
	}
	action := ""
	switch entry.Source {
	case "check", "admin":
		action = entry.Source
	case "resource-filter":
		action = "resource_filter"
	default:
		return nil, errors.New("unrecognized Safe security decision source")
	}
	resourceScope := strings.TrimSpace(entry.ResourceType)
	if resourceScope == "" {
		return nil, errors.New("missing Safe security resource type")
	}
	if id := safeReference(entry.ResourceID); id != "" {
		resourceScope += ":" + id
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	actorID, actorType, authMethod := "anonymous", "anonymous", "unknown"
	if verified := strings.TrimSpace(entry.VerifiedActorID); verified != "" {
		actorID, actorType, authMethod = verified, "user", "oauth"
	}
	effectiveSubject := actorID
	if evaluated := safeReference(entry.AccessorID); evaluated != "" {
		// The accessor is the evaluated subject, not proof of the caller's identity.
		effectiveSubject = evaluated
	}
	outcome := "success"
	if entry.Decision == DecisionDeny {
		outcome = "denied"
	}
	correlation := map[string]any{}
	if requestID := strings.TrimSpace(entry.RequestID); requestID != "" {
		correlation["request_id"] = requestID
	}
	if traceID := strings.TrimSpace(entry.TraceID); traceID != "" {
		correlation["trace_id"] = traceID
	}
	record := map[string]any{
		"schema_version":  auditpublisher.SchemaVersion,
		"event_id":        eventID.String(),
		"source_id":       "bkn-safe-security",
		"category":        "audit.security",
		"event_name":      "authorization.decided",
		"occurred_at":     time.Now().UTC().Format(time.RFC3339Nano),
		"actor":           map[string]any{"id": actorID, "effective_subject": effectiveSubject, "type": actorType, "auth_method": authMethod},
		"target":          map[string]any{"type": "authorization_decision", "id": "decision:" + eventID.String()},
		"outcome":         outcome,
		"scope":           map[string]any{"business_module": "system_management", "environment": environment, "platform_scope": true, "knowledge_network_ids": []string{}},
		"request_context": map[string]any{"source_channel": "api", "transport": "http", "method": strings.ToUpper(entry.Method)},
		"correlation":     correlation,
		"summary":         "safe authorization decision",
		"facts":           map[string]any{"action": action, "decision": entry.Decision, "resource_scope": resourceScope},
	}
	if outcome == "denied" {
		record["failure_code"] = "AUTHZ_DENIED"
	}
	value, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if _, err := auditpublisher.BuildRecord(value); err != nil {
		return nil, err
	}
	return value, nil
}

// safeReference keeps normal OpenBKN identifiers, including bkn_ prefixes,
// while omitting caller-supplied credential strings from Safe's Audit facts.
func safeReference(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" {
		return ""
	}
	if len(id) > 256 || strings.ContainsAny(id, " \t\r\n") {
		return "redacted"
	}
	parts := strings.Split(id, "_")
	if len(parts) == 3 && parts[0] == "bak" && len(parts[1]) == 12 && len(parts[2]) == 27 {
		return "redacted"
	}
	return id
}
