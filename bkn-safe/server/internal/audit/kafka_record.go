package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

var safeAdminResourceTypes = map[string]string{
	"users":                    "user",
	"roles":                    "role",
	"departments":              "department",
	"role-bindings":            "role_binding",
	"object-grants":            "object_grant",
	"enterprise-object-grants": "object_grant",
	"permission-requests":      "permission_request",
	"api-keys":                 "api_key",
	"oauth":                    "oauth_access_origin",
	"clients":                  "oauth_client",
	"license":                  "license",
	"policies":                 "authorization_policy",
	"resource-parents":         "resource_parent",
	"property-levels":          "property_level",
	"property-grants":          "property_grant",
	"row-filter-policies":      "row_filter_policy",
	"profile":                  "user",
}

// BuildKafkaAdminRecord projects a committed Safe administration fact into a
// bounded Audit v1 event. Request Detail is intentionally never serialized:
// the old redacted JSON was a local troubleshooting aid, not an approved
// changed-field or before/after snapshot.
func BuildKafkaAdminRecord(entry Entry, environment string) ([]byte, error) {
	if environment != "development" && environment != "test" && environment != "staging" && environment != "production" {
		return nil, errors.New("invalid Safe Audit environment")
	}
	targetType, ok := safeAdminResourceTypes[entry.Resource]
	if !ok {
		return nil, fmt.Errorf("unregistered Safe Audit resource %q", entry.Resource)
	}
	systemEvent := strings.EqualFold(entry.Method, "SYSTEM")
	if (!systemEvent && entry.RequestID == "") || entry.Action == "" || entry.Status < 100 || entry.Status > 599 {
		return nil, errors.New("incomplete Safe Audit request fact")
	}
	targetID := strings.TrimSpace(entry.TargetID)
	if targetID == "" {
		if systemEvent && entry.Resource == "license" {
			targetID = "license:cluster"
		} else if entry.RequestID != "" {
			targetID = targetType + ":" + entry.RequestID
		} else {
			return nil, errors.New("safe audit target is missing")
		}
	}
	actorID, actorType := strings.TrimSpace(entry.ActorID), "user"
	if actorID == "" {
		actorID, actorType = "anonymous", "anonymous"
	} else if strings.HasPrefix(actorID, "system:") || (entry.ActorType != "" && entry.ActorType != "user") {
		actorType = "service_account"
	}
	authMethod := entry.AuthMethod
	if authMethod == "" {
		authMethod = "unknown"
	}
	channel := entry.SourceChannel
	switch channel {
	case "api", "cli", "mcp", "sdk", "studio":
	default:
		channel = "unknown"
	}
	outcome, decision := "success", "allowed"
	failureCode := ""
	if systemEvent && (entry.Action == "license.renew-failed" || entry.Action == "license.clock-rollback") {
		outcome, decision = "failure", "failed"
		if entry.Action == "license.renew-failed" {
			failureCode = "LICENSE_RENEW_FAILED"
		} else {
			failureCode = "LICENSE_CLOCK_ROLLBACK"
		}
	} else if entry.Status == http.StatusUnauthorized || entry.Status == http.StatusForbidden {
		outcome, decision = "denied", "denied"
	} else if entry.Status >= http.StatusBadRequest {
		outcome, decision = "failure", "failed"
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	transport := "http"
	if systemEvent {
		transport = "non_http"
	}
	correlation := map[string]any{}
	if entry.RequestID != "" {
		correlation["request_id"] = entry.RequestID
	}
	target := map[string]any{"type": targetType, "id": targetID}
	if targetName := strings.TrimSpace(entry.TargetName); targetName != "" {
		target["name"] = targetName
	}
	actor := map[string]any{"id": actorID, "effective_subject": actorID, "type": actorType, "auth_method": authMethod}
	if actorName := strings.TrimSpace(entry.ActorNameSnapshot); actorName != "" {
		actor["display_name_snapshot"] = actorName
	}
	record := map[string]any{
		"schema_version":  auditpublisher.SchemaVersion,
		"event_id":        eventID.String(),
		"source_id":       "bkn-safe-admin",
		"category":        "audit.admin",
		"event_name":      "safe.admin.operation.observed",
		"occurred_at":     time.Now().UTC().Format(time.RFC3339Nano),
		"actor":           actor,
		"target":          target,
		"outcome":         outcome,
		"scope":           map[string]any{"business_module": "system_management", "environment": environment, "platform_scope": true, "knowledge_network_ids": []string{}},
		"request_context": map[string]any{"source_channel": channel, "transport": transport, "method": strings.ToUpper(entry.Method)},
		"correlation":     correlation,
		"summary":         "safe.admin.operation.observed " + entry.Action + " " + targetType,
		"facts":           map[string]any{"action": strings.NewReplacer(".", "_", "-", "_").Replace(entry.Action), "decision": decision},
	}
	if !systemEvent {
		record["http_status"] = entry.Status
	}
	if outcome != "success" && !systemEvent {
		record["failure_code"] = fmt.Sprintf("HTTP_%d", entry.Status)
	} else if failureCode != "" {
		record["failure_code"] = failureCode
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
