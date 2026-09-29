package operationaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

var auditSecretShape = regexp.MustCompile(`(?i)(?:bearer\s+[a-z0-9._~-]{8,}|^bak_[a-z0-9._-]{12,}$)`)

var managementTargetTypes = map[string]struct{}{
	"operator": {}, "mcp": {}, "toolbox": {}, "tool": {}, "skill": {},
	"import_batch": {}, "capability_bundle": {}, "operator_category": {},
}

var managementActions = map[string]struct{}{
	"create": {}, "update": {}, "delete": {}, "status_change": {}, "publish": {}, "import": {},
}

// SafeAuditAlias preserves ordinary source facts but replaces secret-shaped or
// oversized values with stable non-secret correlation aliases.
func SafeAuditAlias(value string, limit int) string {
	if len(value) <= limit && !auditSecretShape.MatchString(value) {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return "ref_" + hex.EncodeToString(sum[:])
}

// BuildKafkaRecord maps a completed management request to an observation
// event. It never claims a configuration change or copies a request body.
func BuildKafkaRecord(entry Entry, environment string) ([]byte, error) {
	if environment != "development" && environment != "test" && environment != "staging" && environment != "production" {
		return nil, errors.New("invalid Audit environment")
	}
	if _, ok := managementTargetTypes[entry.TargetType]; !ok {
		return nil, errors.New("unregistered Audit target type")
	}
	if _, ok := managementActions[entry.Action]; !ok {
		return nil, errors.New("unregistered Audit action")
	}
	if entry.Outcome != "success" && entry.Outcome != "failure" && entry.Outcome != "denied" && entry.Outcome != "unknown" {
		return nil, errors.New("invalid Audit outcome")
	}
	if entry.EventID == "" || entry.EventTime.IsZero() || entry.ActorID == "" || entry.TargetID == "" || entry.RequestID == "" || entry.HTTPStatus < 100 || entry.HTTPStatus > 599 {
		return nil, errors.New("missing or invalid Audit identity, target, or HTTP result")
	}
	actorType := "user"
	if entry.ActorType == "anonymous" {
		actorType = "anonymous"
	} else if entry.ActorType != "user" {
		actorType = "service_account"
	}
	actor := map[string]any{
		"id": entry.ActorID, "effective_subject": entry.ActorID,
		"type": actorType, "auth_method": entry.AuthMethod,
	}
	if name := SafeAuditAlias(strings.TrimSpace(entry.ActorName), 256); name != "" {
		actor["display_name_snapshot"] = name
	}
	target := map[string]any{"type": entry.TargetType, "id": SafeAuditAlias(entry.TargetID, 256)}
	if name := SafeAuditAlias(strings.TrimSpace(entry.TargetName), 512); name != "" {
		target["name"] = name
	}
	record := map[string]any{
		"schema_version": auditpublisher.SchemaVersion,
		"event_id":       entry.EventID,
		"source_id":      "execution-factory",
		"category":       "audit.admin",
		"event_name":     "execution_factory.operation.observed",
		"occurred_at":    entry.EventTime.UTC().Format(time.RFC3339Nano),
		"actor":          actor,
		"target":         target,
		"outcome":        entry.Outcome,
		"http_status":    entry.HTTPStatus,
		"scope": map[string]any{
			"business_module": "execution_factory", "environment": environment,
			"platform_scope": true, "knowledge_network_ids": []string{},
		},
		"request_context": map[string]any{
			"source_channel": executionAuditSourceChannel(entry.SourceChannel), "transport": "http", "method": strings.ToUpper(entry.Method),
		},
		"correlation": map[string]any{"request_id": SafeAuditAlias(entry.RequestID, 128)},
		"summary":     fmt.Sprintf("execution_factory.operation.observed %s %s", entry.Action, entry.TargetType),
		"facts":       map[string]any{"action": entry.Action},
	}
	if entry.Outcome == "failure" || entry.Outcome == "denied" {
		record["failure_code"] = fmt.Sprintf("HTTP_%d", entry.HTTPStatus)
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

func executionAuditSourceChannel(value string) string {
	switch value {
	case "api", "cli", "mcp", "sdk", "studio", "unknown":
		return value
	default:
		return "unknown"
	}
}
