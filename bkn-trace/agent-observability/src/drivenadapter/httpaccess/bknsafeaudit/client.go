package bknsafeaudit

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
	sourceID         = "bkn-safe-admin"
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
	return observabilityvo.SourceStatus{
		SourceID: sourceID, Status: "available", Reliability: "best_effort",
		CollectionMethod: "source_adapter", CoveredModules: []string{"BKN Safe Admin API"},
		CountAccuracy: "exact", Categories: []string{observabilityvo.CategoryAuditAdmin},
	}
}

func (client *Client) Search(ctx context.Context, query observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	if !contains(query.AuthorizedCategories, observabilityvo.CategoryAuditAdmin) {
		return observabilityvo.SourcePage{CountAccuracy: "exact"}, nil
	}
	authorization := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	if authorization == "" {
		return observabilityvo.SourcePage{}, errors.New("BKN Safe audit source requires caller authorization")
	}
	parameters := url.Values{}
	parameters.Set("limit", strconv.Itoa(normalizedLimit(query.Limit)))
	if query.ActorID != "" {
		parameters.Set("actor_id", query.ActorID)
	}
	if query.ResourceID != "" {
		parameters.Set("target_id", query.ResourceID)
	}
	if query.ResourceType != "" {
		parameters.Set("resource", query.ResourceType)
	}
	if query.TimeFrom != nil {
		parameters.Set("from", query.TimeFrom.Format(time.RFC3339))
	}
	if query.TimeTo != nil {
		parameters.Set("to", query.TimeTo.Format(time.RFC3339))
	}
	if query.FailedOnly {
		parameters.Set("failed_only", "true")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/safe/v1/admin/audit-logs?"+parameters.Encode(), nil)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	request.Header.Set("Authorization", authorization)
	response, err := client.http.Do(request)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return observabilityvo.SourcePage{}, fmt.Errorf("BKN Safe audit source returned status %d", response.StatusCode)
	}
	var payload struct {
		Logs  []auditLog `json:"logs"`
		Total int64      `json:"total"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("decode BKN Safe audit response: %w", err)
	}
	records := make([]observabilityvo.LogRecord, 0, len(payload.Logs))
	for _, entry := range payload.Logs {
		records = append(records, projectAuditLog(entry, query.AuthorizedTenantID))
	}
	return observabilityvo.SourcePage{Records: records, Count: payload.Total, CountAccuracy: "exact"}, nil
}

func (client *Client) Get(ctx context.Context, logID string) (observabilityvo.LogRecord, bool, error) {
	authorization := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	if authorization == "" {
		return observabilityvo.LogRecord{}, false, errors.New("BKN Safe audit source requires caller authorization")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/safe/v1/admin/audit-logs/"+url.PathEscape(logID), nil)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	request.Header.Set("Authorization", authorization)
	response, err := client.http.Do(request)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return observabilityvo.LogRecord{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return observabilityvo.LogRecord{}, false, fmt.Errorf("BKN Safe audit source returned status %d", response.StatusCode)
	}
	var entry auditLog
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&entry); err != nil {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("decode BKN Safe audit detail: %w", err)
	}
	scope := observabilityvo.SourceAccessScopeFromContext(ctx)
	return projectAuditLog(entry, scope.TenantID), true, nil
}

type auditLog struct {
	ID         string    `json:"id"`
	ActorID    string    `json:"actor_id"`
	Method     string    `json:"method"`
	Resource   string    `json:"resource"`
	Action     string    `json:"action"`
	TargetID   string    `json:"target_id"`
	TargetName string    `json:"target_name"`
	Status     int       `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

func projectAuditLog(entry auditLog, tenantID string) observabilityvo.LogRecord {
	outcome := "success"
	severityNumber := 9
	severityText := "INFO"
	if entry.Status >= http.StatusBadRequest {
		outcome, severityNumber, severityText = "failure", 17, "ERROR"
	}
	target := entry.TargetName
	if target == "" {
		target = entry.TargetID
	}
	summary := strings.TrimSpace(strings.Join([]string{entry.Method, entry.Resource, target}, " "))
	return observabilityvo.LogRecord{
		SchemaVersion: "1.0.0", LogID: entry.ID, SourceID: sourceID, SourceLogID: entry.ID,
		Category: observabilityvo.CategoryAuditAdmin, EventName: auditEventName(entry),
		EventTimestamp: entry.CreatedAt, ObservedTimestamp: entry.CreatedAt,
		SeverityNumber: severityNumber, SeverityText: severityText, Outcome: outcome,
		SafeSummary: summary, ServiceName: "bkn-safe-admin",
		TenantID: tenantID, ActorID: entry.ActorID, EffectiveSubjectID: entry.ActorID,
		IngressPrincipal: "bkn-safe", TrustLevel: "trusted",
		ResourceRef: &observabilityvo.ResourceRef{ResourceType: entry.Resource, ResourceID: entry.TargetID},
	}
}

func auditEventName(entry auditLog) string {
	switch entry.Resource {
	case "users":
		if entry.Method == http.MethodPost {
			return "user.created"
		}
	case "roles":
		return "role.updated"
	case "models", "model-configs":
		return "model_config.changed"
	case "agents":
		return "agent.config.changed"
	case "tools":
		return "tool.config.changed"
	case "skills":
		return "skill.config.changed"
	case "toolboxes":
		return "toolbox.config.changed"
	case "mcp":
		return "mcp.config.changed"
	}
	return "resource_config.changed"
}

func normalizedLimit(value int) int {
	if value <= 0 {
		return 50
	}
	if value > 200 {
		return 200
	}
	return value
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
