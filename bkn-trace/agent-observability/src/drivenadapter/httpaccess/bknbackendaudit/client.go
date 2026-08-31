// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bknbackendaudit

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
	sourceID         = "bkn-backend"
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
		return observabilityvo.SourceStatus{
			SourceID: sourceID, Status: "not_integrated", Reason: "source_not_configured",
			Reliability: "best_effort", CollectionMethod: "not_integrated",
			CoveredModules: []string{"domain_knowledge_network"}, CountAccuracy: "partial",
			Categories: []string{observabilityvo.CategoryAuditAdmin},
		}
	}
	return observabilityvo.SourceStatus{
		SourceID: sourceID, Status: "degraded", Reason: observabilityvo.SourceReasonPartialManagementAuditCoverage, Reliability: "best_effort",
		CollectionMethod: "source_adapter", CoveredModules: []string{"domain_knowledge_network"},
		CountAccuracy: "partial", Categories: []string{observabilityvo.CategoryAuditAdmin},
	}
}

func (client *Client) Search(ctx context.Context, query observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	if !contains(query.AuthorizedCategories, observabilityvo.CategoryAuditAdmin) {
		return observabilityvo.SourcePage{CountAccuracy: "partial"}, nil
	}
	authorization := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	if authorization == "" {
		return observabilityvo.SourcePage{}, errors.New("BKN Backend audit source requires caller authorization and trusted scope")
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/bkn-backend/v1/operation-audits?"+parameters.Encode(), nil)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	setTrustedHeaders(request, authorization)
	response, err := client.http.Do(request)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return observabilityvo.SourcePage{}, fmt.Errorf("BKN Backend audit source returned status %d", response.StatusCode)
	}
	var payload struct {
		Entries           []auditEntry `json:"entries"`
		HasMore           bool         `json:"has_more"`
		NextBeforeTime    string       `json:"next_before_time"`
		NextBeforeEventID string       `json:"next_before_event_id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return observabilityvo.SourcePage{}, fmt.Errorf("decode BKN Backend audit response: %w", err)
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
	if authorization == "" {
		return observabilityvo.LogRecord{}, false, errors.New("BKN Backend audit source requires caller authorization and trusted scope")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/bkn-backend/v1/operation-audits/"+url.PathEscape(sourceLogID(logID)), nil)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	setTrustedHeaders(request, authorization)
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
		return observabilityvo.LogRecord{}, false, fmt.Errorf("BKN Backend audit source returned status %d", response.StatusCode)
	}
	var entry auditEntry
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&entry); err != nil {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("decode BKN Backend audit detail: %w", err)
	}
	return project(entry), true, nil
}

type auditEntry struct {
	EventID            string         `json:"event_id"`
	EventTime          time.Time      `json:"event_time"`
	RecordedAt         time.Time      `json:"recorded_at"`
	KnowledgeNetworkID string         `json:"knowledge_network_id"`
	ActorID            string         `json:"actor_id"`
	ActorName          string         `json:"actor_name"`
	ActorType          string         `json:"actor_type"`
	AuthMethod         string         `json:"auth_method"`
	CredentialID       string         `json:"credential_id"`
	RequestID          string         `json:"request_id"`
	SourceChannel      string         `json:"source_channel"`
	Method             string         `json:"method"`
	Action             string         `json:"action"`
	TargetType         string         `json:"target_type"`
	TargetID           string         `json:"target_id"`
	TargetName         string         `json:"target_name"`
	Outcome            string         `json:"outcome"`
	FailureCode        string         `json:"failure_code"`
	FailureMessage     string         `json:"failure_message"`
	ChangeSummary      map[string]any `json:"change_summary"`
	SchemaVersion      string         `json:"schema_version"`
}

func project(entry auditEntry) observabilityvo.LogRecord {
	severityNumber, severityText := 9, "INFO"
	if entry.Outcome == "failure" || entry.Outcome == "denied" {
		severityNumber, severityText = 17, "ERROR"
	}
	actorName := firstNonEmpty(entry.ActorName, entry.ActorID)
	targetName := firstNonEmpty(entry.TargetName, entry.TargetID)
	return observabilityvo.LogRecord{
		EventID:             entry.EventID,
		EventTime:           entry.EventTime,
		RecordedAt:          entry.RecordedAt,
		ActorNameSnapshot:   actorName,
		ActorType:           firstNonEmpty(entry.ActorType, "user"),
		AuthMethod:          firstNonEmpty(entry.AuthMethod, "unknown"),
		CredentialID:        entry.CredentialID,
		SourceChannel:       firstNonEmpty(entry.SourceChannel, "api"),
		BusinessModule:      "domain_knowledge_network",
		Action:              entry.Action,
		TargetType:          entry.TargetType,
		TargetID:            entry.TargetID,
		TargetNameSnapshot:  targetName,
		FailureCode:         entry.FailureCode,
		FailureMessage:      entry.FailureMessage,
		SchemaVersion:       firstNonEmpty(entry.SchemaVersion, "1.0"),
		LogID:               sourceID + ":" + entry.EventID,
		SourceID:            sourceID,
		SourceLogID:         entry.EventID,
		Category:            observabilityvo.CategoryAuditAdmin,
		EventName:           "resource_config.changed",
		EventTimestamp:      entry.EventTime,
		ObservedTimestamp:   entry.RecordedAt,
		SeverityNumber:      severityNumber,
		SeverityText:        severityText,
		Outcome:             entry.Outcome,
		SafeSummary:         strings.TrimSpace(strings.Join([]string{entry.Method, entry.Action, entry.TargetType, targetName}, " ")),
		ServiceName:         "bkn-backend",
		Environment:         "unknown",
		ActorID:             entry.ActorID,
		EffectiveSubjectID:  entry.ActorID,
		RequestID:           entry.RequestID,
		IngressPrincipal:    "bkn-backend",
		TrustLevel:          "trusted",
		KnowledgeNetworkIDs: []string{entry.KnowledgeNetworkID},
		ResourceRef: &observabilityvo.ResourceRef{
			ResourceType: entry.TargetType,
			ResourceID:   entry.TargetID,
		},
		Attributes: map[string]any{
			"method":         entry.Method,
			"change_summary": entry.ChangeSummary,
		},
	}
}

func setTrustedHeaders(request *http.Request, authorization string) {
	request.Header.Set("Authorization", authorization)
}

func sourceLogID(logID string) string {
	return strings.TrimPrefix(strings.TrimSpace(logID), sourceID+":")
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

func setIfPresent(parameters url.Values, name, value string) {
	if strings.TrimSpace(value) != "" {
		parameters.Set(name, strings.TrimSpace(value))
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
