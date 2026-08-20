package logsvc

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

const (
	cursorVersion     = "r6.2-v1"
	cursorSortVersion = "event_timestamp_desc_source_id_source_log_id_asc_v2"
	cursorTTL         = 15 * time.Minute
)

type cursorPayload struct {
	Version          string                                    `json:"v"`
	SortVersion      string                                    `json:"sort"`
	FilterHash       string                                    `json:"filter"`
	TenantID         string                                    `json:"tenant"`
	EffectiveSubject string                                    `json:"subject"`
	ApplicationID    string                                    `json:"application,omitempty"`
	ScopeFingerprint string                                    `json:"scope"`
	VisibleSources   []string                                  `json:"sources"`
	Positions        map[string]observabilityvo.SourcePosition `json:"positions"`
	QueryWatermark   time.Time                                 `json:"watermark"`
	ExpiresAt        time.Time                                 `json:"expires_at"`
}

func randomCursorKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("cannot initialize observability cursor signing key")
	}
	return key
}

func encodeCursor(key []byte, payload cursorPayload) string {
	body, _ := json.Marshal(payload)
	signature := signCursor(key, body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func decodeCursor(key []byte, token string) (cursorPayload, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return cursorPayload{}, false
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(body) != parts[0] {
		return cursorPayload{}, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(signature) != parts[1] ||
		!hmac.Equal(signature, signCursor(key, body)) {
		return cursorPayload{}, false
	}
	var payload cursorPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return cursorPayload{}, false
	}
	return payload, true
}

func signCursor(key, body []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(body)
	return mac.Sum(nil)
}

func logFilterHash(query observabilityvo.LogQuery) string {
	type filter struct {
		Query           string     `json:"q,omitempty"`
		TimeFrom        *time.Time `json:"time_from,omitempty"`
		TimeTo          *time.Time `json:"time_to,omitempty"`
		BusinessModule  string     `json:"module,omitempty"`
		Action          string     `json:"action,omitempty"`
		TargetType      string     `json:"target_type,omitempty"`
		TargetID        string     `json:"target_id,omitempty"`
		Outcomes        []string   `json:"outcomes,omitempty"`
		Categories      []string   `json:"categories,omitempty"`
		SeverityMinimum int        `json:"severity_min,omitempty"`
		Services        []string   `json:"services,omitempty"`
		Environments    []string   `json:"environments,omitempty"`
		EventNames      []string   `json:"event_names,omitempty"`
		BusinessDomain  string     `json:"business_domain,omitempty"`
		ActorID         string     `json:"actor_id,omitempty"`
		ActorQuery      string     `json:"actor,omitempty"`
		ApplicationID   string     `json:"application_id,omitempty"`
		ResourceType    string     `json:"resource_type,omitempty"`
		ResourceID      string     `json:"resource_id,omitempty"`
		ConversationID  string     `json:"conversation_id,omitempty"`
		InteractionID   string     `json:"interaction_id,omitempty"`
		OperationID     string     `json:"operation_id,omitempty"`
		RequestID       string     `json:"request_id,omitempty"`
		TraceID         string     `json:"trace_id,omitempty"`
		SpanID          string     `json:"span_id,omitempty"`
		FailedOnly      bool       `json:"failed_only,omitempty"`
	}
	normalized := filter{
		Query: strings.TrimSpace(query.Query), TimeFrom: query.TimeFrom, TimeTo: query.TimeTo,
		BusinessModule: query.BusinessModule, Action: query.Action,
		TargetType: query.TargetType, TargetID: query.TargetID, Outcomes: normalizedStrings(query.Outcomes),
		Categories: normalizedStrings(query.Categories), SeverityMinimum: query.SeverityMinimum,
		Services: normalizedStrings(query.Services), Environments: normalizedStrings(query.Environments),
		EventNames: normalizedStrings(query.EventNames), BusinessDomain: query.BusinessDomain,
		ActorID: query.ActorID, ActorQuery: query.ActorQuery, ApplicationID: query.ApplicationID,
		ResourceType: query.ResourceType, ResourceID: query.ResourceID,
		ConversationID: query.ConversationID, InteractionID: query.InteractionID,
		OperationID: query.OperationID, RequestID: query.RequestID,
		TraceID: query.TraceID, SpanID: query.SpanID, FailedOnly: query.FailedOnly,
	}
	body, _ := json.Marshal(normalized)
	hash := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func normalizedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func cursorMatches(
	payload cursorPayload,
	profile evidencevo.AccessProfile,
	query observabilityvo.LogQuery,
	visibleSources []string,
	now time.Time,
) bool {
	return payload.Version == cursorVersion && payload.SortVersion == cursorSortVersion &&
		payload.FilterHash == logFilterHash(query) && payload.TenantID == profile.TenantID &&
		payload.EffectiveSubject == profile.EffectiveSubjectID &&
		payload.ApplicationID == profile.ApplicationPrincipalID &&
		payload.ScopeFingerprint == profile.Fingerprint &&
		equalStrings(payload.VisibleSources, visibleSources) && !payload.QueryWatermark.IsZero() && now.Before(payload.ExpiresAt)
}

func equalStrings(left, right []string) bool {
	left = normalizedStrings(left)
	right = normalizedStrings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
