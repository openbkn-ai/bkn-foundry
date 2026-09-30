package accesslog

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

const accessSourceID = "bkn-safe-access"

// BuildKafkaRecord projects one existing Safe access fact into the bounded
// Audit v1 transport. An access fact is a user-facing business fact only when
// its subject was resolved by Safe; unknown credential attempts stay out of
// this stream.
func BuildKafkaRecord(entry Entry, environment string) ([]byte, error) {
	if environment != "development" && environment != "test" && environment != "staging" && environment != "production" {
		return nil, errors.New("invalid Safe access environment")
	}
	eventName, err := accessEventName(entry.Action, entry.Outcome)
	if err != nil {
		return nil, err
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	actorID := strings.TrimSpace(entry.ActorID)
	if actorID == "" {
		return nil, errors.New("safe access actor is missing")
	}
	actorName := strings.TrimSpace(entry.ActorNameSnapshot)
	if actorName == "" {
		return nil, errors.New("safe access actor name is missing")
	}
	correlation := map[string]any{}
	if requestID := strings.TrimSpace(entry.RequestID); requestID != "" {
		correlation["request_id"] = requestID
	}
	authMethod := strings.TrimSpace(entry.AuthMethod)
	if authMethod == "" {
		authMethod = "unknown"
	}
	channel := strings.TrimSpace(entry.SourceChannel)
	if channel != "api" && channel != "cli" && channel != "mcp" && channel != "sdk" && channel != "studio" {
		channel = "unknown"
	}
	record := map[string]any{
		"schema_version":  auditpublisher.SchemaVersion,
		"event_id":        eventID.String(),
		"source_id":       accessSourceID,
		"category":        "access.user",
		"event_name":      eventName,
		"occurred_at":     time.Now().UTC().Format(time.RFC3339Nano),
		"actor":           map[string]any{"id": actorID, "effective_subject": actorID, "display_name_snapshot": actorName, "type": "user", "auth_method": authMethod},
		"target":          map[string]any{"type": "user", "id": actorID, "name": actorName},
		"outcome":         entry.Outcome,
		"scope":           map[string]any{"business_module": "system_management", "environment": environment, "platform_scope": true, "knowledge_network_ids": []string{}},
		"request_context": map[string]any{"source_channel": channel, "transport": "http", "method": "POST"},
		"correlation":     correlation,
		"summary":         "safe access " + eventName,
		"facts":           map[string]any{"action": entry.Action, "result": entry.Outcome},
	}
	if entry.Outcome != "success" {
		failureCode := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(entry.FailureCode), "-", "_"))
		if failureCode != "" {
			record["failure_code"] = failureCode
		}
	}
	value, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func accessEventName(action, outcome string) (string, error) {
	switch action {
	case "login":
		switch outcome {
		case "success":
			return "login.succeeded", nil
		case "failure", "denied":
			return "login.failed", nil
		}
	case "logout":
		if outcome == "success" {
			return "logout.succeeded", nil
		}
	}
	return "", fmt.Errorf("unregistered Safe access fact %q/%q", action, outcome)
}
