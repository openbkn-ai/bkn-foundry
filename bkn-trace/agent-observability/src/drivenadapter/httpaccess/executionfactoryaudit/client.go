// Package executionfactoryaudit reads bounded Execution Factory management facts.
package executionfactoryaudit

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

const sourceID = "execution-factory"

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), http: client}
}
func (c *Client) ID() string { return sourceID }
func (c *Client) Metadata() observabilityvo.SourceStatus {
	if c.baseURL == "" {
		return observabilityvo.SourceStatus{SourceID: sourceID, Status: "not_integrated", Reason: "source_not_configured", Reliability: "best_effort", CollectionMethod: "not_integrated", CoveredModules: []string{"execution_factory"}, CountAccuracy: "partial", Categories: []string{observabilityvo.CategoryAuditAdmin}}
	}
	return observabilityvo.SourceStatus{SourceID: sourceID, Status: "degraded", Reason: "partial_management_audit_coverage", Reliability: "best_effort", CollectionMethod: "source_adapter", CoveredModules: []string{"execution_factory"}, CountAccuracy: "partial", Categories: []string{observabilityvo.CategoryAuditAdmin}}
}
func (c *Client) Search(ctx context.Context, q observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	if !contains(q.AuthorizedCategories, observabilityvo.CategoryAuditAdmin) {
		return observabilityvo.SourcePage{CountAccuracy: "partial"}, nil
	}
	auth := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	if auth == "" || q.AuthorizedTenantID == "" || q.AuthorizedBusinessDomain == "" {
		return observabilityvo.SourcePage{}, errors.New("execution factory audit source requires caller authorization and trusted scope")
	}
	v := url.Values{}
	v.Set("limit", strconv.Itoa(limit(q.Limit)))
	setIfPresent(v, "actor_id", q.ActorID)
	setIfPresent(v, "action", q.Action)
	setIfPresent(v, "target_type", q.TargetType)
	setIfPresent(v, "target_id", q.TargetID)
	if len(q.Outcomes) == 1 {
		setIfPresent(v, "outcome", q.Outcomes[0])
	}
	if q.TimeFrom != nil {
		v.Set("from", q.TimeFrom.UTC().Format(time.RFC3339Nano))
	}
	if q.TimeTo != nil {
		v.Set("to", q.TimeTo.UTC().Format(time.RFC3339Nano))
	}
	if q.PageBefore != nil && !q.PageBefore.EventTimestamp.IsZero() {
		v.Set("before_time", q.PageBefore.EventTimestamp.UTC().Format(time.RFC3339Nano))
		v.Set("before_event_id", sourceLogID(q.PageBefore.LogID))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/agent-operator-integration/v1/operation-audits?"+v.Encode(), nil)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	trustedHeaders(req, auth, q.AuthorizedTenantID, q.AuthorizedBusinessDomain)
	resp, err := c.http.Do(req)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return observabilityvo.SourcePage{}, fmt.Errorf("execution factory audit source returned status %d", resp.StatusCode)
	}
	var payload struct {
		Entries []entry `json:"entries"`
		HasMore bool    `json:"has_more"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return observabilityvo.SourcePage{}, err
	}
	records := make([]observabilityvo.LogRecord, 0, len(payload.Entries))
	for _, item := range payload.Entries {
		records = append(records, project(item))
	}
	count := int64(len(records))
	if payload.HasMore {
		count++
	}
	return observabilityvo.SourcePage{Records: records, Count: count, CountAccuracy: "partial"}, nil
}
func (c *Client) Get(ctx context.Context, logID string) (observabilityvo.LogRecord, bool, error) {
	auth := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	scope := observabilityvo.SourceAccessScopeFromContext(ctx)
	if auth == "" || scope.TenantID == "" || scope.BusinessDomain == "" {
		return observabilityvo.LogRecord{}, false, errors.New("execution factory audit source requires caller authorization and trusted scope")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/agent-operator-integration/v1/operation-audits/"+url.PathEscape(sourceLogID(logID)), nil)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	trustedHeaders(req, auth, scope.TenantID, scope.BusinessDomain)
	resp, err := c.http.Do(req)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return observabilityvo.LogRecord{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("execution factory audit source returned status %d", resp.StatusCode)
	}
	var item entry
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&item); err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	return project(item), true, nil
}

type entry struct {
	EventID          string    `json:"event_id"`
	EventTime        time.Time `json:"event_time"`
	RecordedAt       time.Time `json:"recorded_at"`
	TenantID         string    `json:"tenant_id"`
	BusinessDomainID string    `json:"business_domain_id"`
	ActorID          string    `json:"actor_id"`
	ActorName        string    `json:"actor_name"`
	ActorType        string    `json:"actor_type"`
	AuthMethod       string    `json:"auth_method"`
	RequestID        string    `json:"request_id"`
	SourceChannel    string    `json:"source_channel"`
	Method           string    `json:"method"`
	Action           string    `json:"action"`
	TargetType       string    `json:"target_type"`
	TargetID         string    `json:"target_id"`
	TargetName       string    `json:"target_name"`
	Outcome          string    `json:"outcome"`
	FailureCode      string    `json:"failure_code"`
	FailureMessage   string    `json:"failure_message"`
}

func project(e entry) observabilityvo.LogRecord {
	severity, text := 9, "INFO"
	if e.Outcome != "success" {
		severity, text = 17, "ERROR"
	}
	return observabilityvo.LogRecord{EventID: e.EventID, EventTime: e.EventTime, RecordedAt: e.RecordedAt, ActorNameSnapshot: first(e.ActorName, e.ActorID), ActorType: first(e.ActorType, "user"), AuthMethod: first(e.AuthMethod, "unknown"), SourceChannel: first(e.SourceChannel, "api"), BusinessModule: "execution_factory", Action: e.Action, TargetType: e.TargetType, TargetID: e.TargetID, TargetNameSnapshot: first(e.TargetName, e.TargetID), FailureCode: e.FailureCode, FailureMessage: e.FailureMessage, SchemaVersion: "1.0", LogID: sourceID + ":" + e.EventID, SourceID: sourceID, SourceLogID: e.EventID, Category: observabilityvo.CategoryAuditAdmin, EventName: "execution_factory.management.changed", EventTimestamp: e.EventTime, ObservedTimestamp: e.RecordedAt, SeverityNumber: severity, SeverityText: text, Outcome: e.Outcome, SafeSummary: strings.TrimSpace(strings.Join([]string{e.Method, e.Action, e.TargetType, first(e.TargetName, e.TargetID)}, " ")), ServiceName: "agent-operator-integration", Environment: "unknown", TenantID: e.TenantID, BusinessDomain: e.BusinessDomainID, ActorID: e.ActorID, EffectiveSubjectID: e.ActorID, RequestID: e.RequestID, IngressPrincipal: "execution-factory", TrustLevel: "trusted", ResourceRef: &observabilityvo.ResourceRef{ResourceType: e.TargetType, ResourceID: e.TargetID}, Attributes: map[string]any{"method": e.Method}}
}
func trustedHeaders(r *http.Request, auth, tenant, domain string) {
	r.Header.Set("Authorization", auth)
	r.Header.Set("x-tenant-id", tenant)
	r.Header.Set("x-business-domain", domain)
}
func sourceLogID(id string) string { return strings.TrimPrefix(id, sourceID+":") }
func limit(v int) int {
	if v <= 0 {
		return 50
	}
	if v > 500 {
		return 500
	}
	return v
}
func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
func first(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

func setIfPresent(values url.Values, name, value string) {
	if strings.TrimSpace(value) != "" {
		values.Set(name, strings.TrimSpace(value))
	}
}
