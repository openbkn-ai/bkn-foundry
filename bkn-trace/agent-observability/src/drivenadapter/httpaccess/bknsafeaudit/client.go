// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

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
		SourceID: sourceID, Status: "healthy", Reliability: "best_effort",
		CollectionMethod: "source_adapter", CoveredModules: []string{"system_management"},
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
	if query.Action != "" {
		parameters.Set("action", query.Action)
	}
	if query.TargetID != "" {
		parameters.Set("target_id", query.TargetID)
	}
	if query.TargetType != "" {
		parameters.Set("resource", query.TargetType)
	}
	if query.TimeFrom != nil {
		parameters.Set("from", query.TimeFrom.Format(time.RFC3339Nano))
	}
	if query.TimeTo != nil {
		parameters.Set("to", query.TimeTo.Format(time.RFC3339Nano))
	}
	if query.PageBefore != nil && !query.PageBefore.EventTimestamp.IsZero() {
		parameters.Set("to", query.PageBefore.EventTimestamp.Format(time.RFC3339Nano))
		parameters.Set("before_id", query.PageBefore.LogID)
	}
	if onlyFailedOutcomes(query.Outcomes) {
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
		if businessModuleForResource(entry.Resource) == "" {
			// Login and logout rows are carried by the dedicated access source.
			// This source is the management-audit projection, so do not pass its
			// legacy access duplicates to the public operation-log contract.
			continue
		}
		records = append(records, projectAuditLog(entry, query.AuthorizedTenantID))
	}
	var lastPosition *observabilityvo.SourcePosition
	if len(payload.Logs) > 0 {
		last := payload.Logs[len(payload.Logs)-1]
		lastPosition = &observabilityvo.SourcePosition{EventTimestamp: last.CreatedAt, SourceID: sourceID, LogID: last.ID}
	}
	countAccuracy := "exact"
	if payload.Total != int64(len(records)) {
		// The upstream total includes legacy rows outside this source's explicit
		// management-audit scope. Keep it as the pagination signal while marking
		// the total inexact, so later management rows remain reachable.
		countAccuracy = "partial"
	}
	return observabilityvo.SourcePage{Records: records, LastPosition: lastPosition, Count: payload.Total, CountAccuracy: countAccuracy}, nil
}

func onlyFailedOutcomes(outcomes []string) bool {
	if len(outcomes) == 0 {
		return false
	}
	for _, outcome := range outcomes {
		if outcome != "failure" && outcome != "denied" {
			return false
		}
	}
	return true
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
	ID                string    `json:"id"`
	ActorID           string    `json:"actor_id"`
	ActorNameSnapshot string    `json:"actor_name_snapshot"`
	AuthMethod        string    `json:"auth_method"`
	CredentialID      string    `json:"credential_id"`
	RequestID         string    `json:"request_id"`
	SourceChannel     string    `json:"source_channel"`
	Method            string    `json:"method"`
	Resource          string    `json:"resource"`
	Action            string    `json:"action"`
	TargetID          string    `json:"target_id"`
	TargetName        string    `json:"target_name"`
	Detail            string    `json:"detail"`
	Status            int       `json:"status"`
	ClientIP          string    `json:"client_ip"`
	CreatedAt         time.Time `json:"created_at"`
}

func projectAuditLog(entry auditLog, tenantID string) observabilityvo.LogRecord {
	// BKN Safe is deployed inside one OpenBKN tenant boundary and its legacy audit
	// table has no tenant column. The forwarded caller token authorizes the source
	// read; the adapter stamps that deployment tenant onto the safe projection.
	outcome := auditOutcome(entry.Status)
	severityNumber := 9
	severityText := "INFO"
	if entry.Status >= http.StatusBadRequest {
		severityNumber, severityText = 17, "ERROR"
	}
	failureCode := ""
	if entry.Status >= http.StatusBadRequest {
		failureCode = "http_" + strconv.Itoa(entry.Status)
	}
	target := entry.TargetName
	if target == "" {
		target = entry.TargetID
	}
	actorName := strings.TrimSpace(entry.ActorNameSnapshot)
	if actorName == "" {
		actorName = entry.ActorID
	}
	authMethod := strings.TrimSpace(entry.AuthMethod)
	if authMethod == "" {
		authMethod = "unknown"
	}
	sourceChannel := strings.TrimSpace(entry.SourceChannel)
	if sourceChannel == "" {
		// This adapter reads only the BKN Safe HTTP management audit table.
		// Older rows predate the persisted field, but their ingress is still
		// deterministically the Safe API rather than an inferred UI/CLI client.
		sourceChannel = "api"
	}
	summary := strings.TrimSpace(strings.Join([]string{entry.Method, entry.Resource, target}, " "))
	return observabilityvo.LogRecord{
		EventID: entry.ID, EventTime: entry.CreatedAt, RecordedAt: entry.CreatedAt,
		ActorNameSnapshot: actorName, ActorType: "user",
		AuthMethod: authMethod, CredentialID: entry.CredentialID, SourceChannel: sourceChannel,
		BusinessModule: businessModuleForResource(entry.Resource), Action: businessAction(entry),
		TargetType: entry.Resource, TargetID: entry.TargetID, TargetNameSnapshot: target,
		FailureCode:   failureCode,
		SchemaVersion: "1.0", LogID: sourceID + ":" + entry.ID, SourceID: sourceID, SourceLogID: entry.ID,
		Category: observabilityvo.CategoryAuditAdmin, EventName: auditEventName(entry),
		EventTimestamp: entry.CreatedAt, ObservedTimestamp: entry.CreatedAt,
		SeverityNumber: severityNumber, SeverityText: severityText, Outcome: outcome,
		SafeSummary: summary, ServiceName: "bkn-safe-admin", Environment: "unknown",
		TenantID: tenantID, ActorID: entry.ActorID, EffectiveSubjectID: entry.ActorID, RequestID: entry.RequestID,
		IngressPrincipal: "bkn-safe", TrustLevel: "trusted",
		ResourceRef: &observabilityvo.ResourceRef{ResourceType: entry.Resource, ResourceID: entry.TargetID},
		Attributes: map[string]any{
			"method": entry.Method, "status_code": entry.Status, "client_ip": entry.ClientIP,
			"detail": safeAuditDetail(entry.Detail),
		},
	}
}

func safeAuditDetail(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil
	}
	for key := range result {
		switch strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", "")) {
		case "password", "secret", "token", "authorization", "cookie", "apikey", "clientsecret", "privatekey":
			delete(result, key)
		}
	}
	return result
}

func businessAction(entry auditLog) string {
	action := strings.TrimSpace(entry.Action)
	// Rows written before explicit business actions used the route noun. Only
	// normalize the two management routes whose HTTP method is unambiguous.
	if action != entry.Resource {
		return action
	}
	switch entry.Resource {
	case "role-bindings":
		if entry.Method == http.MethodDelete {
			return "unbind_role"
		}
		if entry.Method == http.MethodPost {
			return "bind_role"
		}
	case "object-grants":
		if entry.Method == http.MethodDelete {
			return "revoke"
		}
		if entry.Method == http.MethodPost {
			return "grant"
		}
	}
	return action
}

func auditOutcome(status int) string {
	switch {
	case status == 0:
		return "unknown"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "denied"
	case status >= http.StatusBadRequest:
		return "failure"
	default:
		return "success"
	}
}

func businessModuleForResource(resource string) string {
	switch strings.TrimSpace(resource) {
	case "users", "departments", "members", "organizations", "roles", "permissions",
		"authorizations", "policies", "role-bindings", "object-grants", "api-keys", "app-keys",
		"license", "licenses", "clients", "profile":
		return "system_management"
	default:
		return ""
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
