package bknsafeuseraccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

const (
	sourceID         = "bkn-safe-access"
	maxResponseBytes = 4 << 20
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), http: httpClient}
}

func (client *Client) ID() string { return sourceID }

func (client *Client) Metadata() observabilityvo.SourceStatus {
	if client.baseURL == "" {
		return observabilityvo.SourceStatus{SourceID: sourceID, Status: "not_integrated", Reason: "source_not_configured", Reliability: "best_effort", CollectionMethod: "not_integrated", CoveredModules: []string{"system_management"}, CountAccuracy: "partial", Categories: []string{observabilityvo.CategoryAccessUser}}
	}
	return observabilityvo.SourceStatus{SourceID: sourceID, Status: "healthy", Reliability: "best_effort", CollectionMethod: "source_adapter", CoveredModules: []string{"system_management"}, CountAccuracy: "exact", Categories: []string{observabilityvo.CategoryAccessUser}}
}

func (client *Client) Search(ctx context.Context, query observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	if !contains(query.AuthorizedCategories, observabilityvo.CategoryAccessUser) {
		return observabilityvo.SourcePage{CountAccuracy: "exact"}, nil
	}
	authorization := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	if authorization == "" {
		return observabilityvo.SourcePage{}, errors.New("BKN Safe access source requires caller authorization")
	}
	parameters := url.Values{}
	parameters.Set("limit", strconv.Itoa(normalizedLimit(query.Limit)))
	setIfPresent(parameters, "actor_id", query.ActorID)
	setIfPresent(parameters, "action", query.Action)
	if len(query.Outcomes) == 1 {
		setIfPresent(parameters, "outcome", query.Outcomes[0])
	}
	if query.TimeFrom != nil {
		parameters.Set("from", query.TimeFrom.UTC().Format(time.RFC3339Nano))
	}
	if query.TimeTo != nil {
		parameters.Set("to", query.TimeTo.UTC().Format(time.RFC3339Nano))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/safe/v1/admin/access-logs?"+parameters.Encode(), nil)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	request.Header.Set("Authorization", authorization)
	response, err := client.http.Do(request)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return observabilityvo.SourcePage{}, fmt.Errorf("BKN Safe access source returned status %d", response.StatusCode)
	}
	var payload struct {
		Logs  []accessLog `json:"logs"`
		Total int64       `json:"total"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("decode BKN Safe access response: %w", err)
	}
	records := make([]observabilityvo.LogRecord, 0, len(payload.Logs))
	for _, entry := range payload.Logs {
		records = append(records, project(entry, query.AuthorizedTenantID))
	}
	return observabilityvo.SourcePage{Records: records, Count: payload.Total, CountAccuracy: "exact"}, nil
}

func (client *Client) Get(ctx context.Context, logID string) (observabilityvo.LogRecord, bool, error) {
	authorization := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	if authorization == "" {
		return observabilityvo.LogRecord{}, false, errors.New("BKN Safe access source requires caller authorization")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/safe/v1/admin/access-logs/"+url.PathEscape(sourceLogID(logID)), nil)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	request.Header.Set("Authorization", authorization)
	response, err := client.http.Do(request)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return observabilityvo.LogRecord{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("BKN Safe access source returned status %d", response.StatusCode)
	}
	var entry accessLog
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&entry); err != nil {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("decode BKN Safe access detail: %w", err)
	}
	scope := observabilityvo.SourceAccessScopeFromContext(ctx)
	return project(entry, scope.TenantID), true, nil
}

type accessLog struct {
	ID                string    `json:"id"`
	ActorID           string    `json:"actor_id"`
	ActorNameSnapshot string    `json:"actor_name_snapshot"`
	AuthMethod        string    `json:"auth_method"`
	SourceChannel     string    `json:"source_channel"`
	Action            string    `json:"action"`
	Outcome           string    `json:"outcome"`
	FailureCode       string    `json:"failure_code"`
	RequestID         string    `json:"request_id"`
	ClientIP          string    `json:"client_ip"`
	CreatedAt         time.Time `json:"created_at"`
}

func project(entry accessLog, tenantID string) observabilityvo.LogRecord {
	action := strings.TrimSpace(entry.Action)
	outcome := strings.TrimSpace(entry.Outcome)
	eventName := "login.succeeded"
	if action == "logout" {
		eventName = "logout.succeeded"
	}
	if action == "login" && outcome == "failure" {
		eventName = "login.failed"
	}
	severityNumber, severityText := 9, "INFO"
	if outcome == "failure" {
		severityNumber, severityText = 17, "ERROR"
	}
	actorName := firstNonEmpty(entry.ActorNameSnapshot, entry.ActorID, "未识别用户")
	targetID := firstNonEmpty(entry.ActorID, entry.ActorNameSnapshot)
	return observabilityvo.LogRecord{
		EventID: entry.ID, EventTime: entry.CreatedAt, RecordedAt: entry.CreatedAt,
		ActorNameSnapshot: actorName, ActorType: "user", AuthMethod: firstNonEmpty(entry.AuthMethod, "unknown"), SourceChannel: firstNonEmpty(entry.SourceChannel, "web"),
		BusinessModule: "system_management", Action: action, TargetType: "user", TargetID: targetID, TargetNameSnapshot: actorName,
		FailureCode: entry.FailureCode, SchemaVersion: "1.0", LogID: sourceID + ":" + entry.ID, SourceID: sourceID, SourceLogID: entry.ID,
		Category: observabilityvo.CategoryAccessUser, EventName: eventName, EventTimestamp: entry.CreatedAt, ObservedTimestamp: entry.CreatedAt,
		SeverityNumber: severityNumber, SeverityText: severityText, Outcome: outcome, SafeSummary: accessSummary(action, actorName, outcome), ServiceName: "bkn-safe-access", Environment: "unknown", TenantID: tenantID,
		ActorID: entry.ActorID, EffectiveSubjectID: entry.ActorID, RequestID: entry.RequestID, IngressPrincipal: "bkn-safe", TrustLevel: "trusted",
		ResourceRef: &observabilityvo.ResourceRef{ResourceType: "user", ResourceID: targetID}, Attributes: map[string]any{"client_ip": entry.ClientIP},
	}
}

func accessSummary(action, actorName, outcome string) string {
	return strings.TrimSpace(strings.Join([]string{action, actorName, outcome}, " "))
}
func sourceLogID(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), sourceID+":")
}
func normalizedLimit(value int) int {
	if value <= 0 {
		return 50
	}
	if value > 500 {
		return 500
	}
	return value
}
func setIfPresent(values url.Values, key, value string) {
	if strings.TrimSpace(value) != "" {
		values.Set(key, strings.TrimSpace(value))
	}
}
func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
