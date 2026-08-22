// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package vegaaudit

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

const sourceID = "vega"

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
		return observabilityvo.SourceStatus{SourceID: sourceID, Status: "not_integrated", Reason: "source_not_configured", Reliability: "best_effort", CollectionMethod: "not_integrated", CoveredModules: []string{"data_resource_knowledge_network"}, CountAccuracy: "partial", Categories: []string{observabilityvo.CategoryAuditAdmin}}
	}
	return observabilityvo.SourceStatus{SourceID: sourceID, Status: "degraded", Reason: observabilityvo.SourceReasonPartialManagementAuditCoverage, Reliability: "best_effort", CollectionMethod: "source_adapter", CoveredModules: []string{"data_resource_knowledge_network"}, CountAccuracy: "partial", Categories: []string{observabilityvo.CategoryAuditAdmin}}
}

func (client *Client) Search(ctx context.Context, query observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	if !contains(query.AuthorizedCategories, observabilityvo.CategoryAuditAdmin) {
		return observabilityvo.SourcePage{CountAccuracy: "partial"}, nil
	}
	authorization := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	if authorization == "" || query.AuthorizedTenantID == "" || query.AuthorizedBusinessDomain == "" {
		return observabilityvo.SourcePage{}, errors.New("vega audit source requires caller authorization and trusted scope")
	}
	parameters := url.Values{}
	parameters.Set("limit", strconv.Itoa(normalizedLimit(query.Limit)))
	setIfPresent(parameters, "actor_id", query.ActorID)
	setIfPresent(parameters, "action", query.Action)
	setIfPresent(parameters, "target_type", query.TargetType)
	setIfPresent(parameters, "target_id", query.TargetID)
	if len(query.Outcomes) == 1 {
		setIfPresent(parameters, "outcome", query.Outcomes[0])
	}
	if query.TimeFrom != nil {
		parameters.Set("from", query.TimeFrom.UTC().Format(time.RFC3339Nano))
	}
	if query.TimeTo != nil {
		parameters.Set("to", query.TimeTo.UTC().Format(time.RFC3339Nano))
	}
	if query.PageBefore != nil && !query.PageBefore.EventTimestamp.IsZero() {
		parameters.Set("before_time", query.PageBefore.EventTimestamp.UTC().Format(time.RFC3339Nano))
		parameters.Set("before_event_id", sourceLogID(query.PageBefore.LogID))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/vega-backend/v1/operation-audits?"+parameters.Encode(), nil)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	setTrustedHeaders(request, authorization, query.AuthorizedTenantID, query.AuthorizedBusinessDomain)
	response, err := client.http.Do(request)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<20))
		return observabilityvo.SourcePage{}, fmt.Errorf("vega audit source returned status %d", response.StatusCode)
	}
	var payload struct {
		Entries []auditEntry `json:"entries"`
		HasMore bool         `json:"has_more"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("decode Vega audit response: %w", err)
	}
	records := make([]observabilityvo.LogRecord, 0, len(payload.Entries))
	for _, entry := range payload.Entries {
		records = append(records, project(entry))
	}
	count := int64(len(records))
	if payload.HasMore {
		count++
	}
	return observabilityvo.SourcePage{Records: records, Count: count, CountAccuracy: "partial"}, nil
}

func (client *Client) Get(ctx context.Context, logID string) (observabilityvo.LogRecord, bool, error) {
	authorization := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	scope := observabilityvo.SourceAccessScopeFromContext(ctx)
	if authorization == "" || scope.TenantID == "" || scope.BusinessDomain == "" {
		return observabilityvo.LogRecord{}, false, errors.New("vega audit source requires caller authorization and trusted scope")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/vega-backend/v1/operation-audits/"+url.PathEscape(sourceLogID(logID)), nil)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	setTrustedHeaders(request, authorization, scope.TenantID, scope.BusinessDomain)
	response, err := client.http.Do(request)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return observabilityvo.LogRecord{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("vega audit source returned status %d", response.StatusCode)
	}
	var entry auditEntry
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&entry); err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	return project(entry), true, nil
}

type auditEntry struct {
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
	SchemaVersion    string    `json:"schema_version"`
}

func project(entry auditEntry) observabilityvo.LogRecord {
	severityNumber, severityText := 9, "INFO"
	if entry.Outcome != "success" {
		severityNumber, severityText = 17, "ERROR"
	}
	return observabilityvo.LogRecord{EventID: entry.EventID, EventTime: entry.EventTime, RecordedAt: entry.RecordedAt, ActorNameSnapshot: firstNonEmpty(entry.ActorName, entry.ActorID), ActorType: firstNonEmpty(entry.ActorType, "user"), AuthMethod: firstNonEmpty(entry.AuthMethod, "unknown"), SourceChannel: firstNonEmpty(entry.SourceChannel, "api"), BusinessModule: "data_resource_knowledge_network", Action: entry.Action, TargetType: entry.TargetType, TargetID: entry.TargetID, TargetNameSnapshot: firstNonEmpty(entry.TargetName, entry.TargetID), FailureCode: entry.FailureCode, FailureMessage: entry.FailureMessage, SchemaVersion: firstNonEmpty(entry.SchemaVersion, "1.0"), LogID: sourceID + ":" + entry.EventID, SourceID: sourceID, SourceLogID: entry.EventID, Category: observabilityvo.CategoryAuditAdmin, EventName: "resource_config.changed", EventTimestamp: entry.EventTime, ObservedTimestamp: entry.RecordedAt, SeverityNumber: severityNumber, SeverityText: severityText, Outcome: entry.Outcome, SafeSummary: strings.TrimSpace(strings.Join([]string{entry.Method, entry.Action, entry.TargetType, firstNonEmpty(entry.TargetName, entry.TargetID)}, " ")), ServiceName: "vega-backend", Environment: "unknown", TenantID: entry.TenantID, BusinessDomain: entry.BusinessDomainID, ActorID: entry.ActorID, EffectiveSubjectID: entry.ActorID, RequestID: entry.RequestID, IngressPrincipal: "vega", TrustLevel: "trusted", ResourceRef: &observabilityvo.ResourceRef{ResourceType: entry.TargetType, ResourceID: entry.TargetID}, Attributes: map[string]any{"method": entry.Method}}
}
func setTrustedHeaders(request *http.Request, authorization, tenantID, businessDomain string) {
	request.Header.Set("Authorization", authorization)
	request.Header.Set("x-tenant-id", tenantID)
	request.Header.Set("x-business-domain", businessDomain)
}
func sourceLogID(logID string) string { return strings.TrimPrefix(logID, sourceID+":") }
func normalizedLimit(value int) int {
	if value <= 0 {
		return 50
	}
	if value > 500 {
		return 500
	}
	return value
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
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

func setIfPresent(parameters url.Values, name, value string) {
	if strings.TrimSpace(value) != "" {
		parameters.Set(name, strings.TrimSpace(value))
	}
}
