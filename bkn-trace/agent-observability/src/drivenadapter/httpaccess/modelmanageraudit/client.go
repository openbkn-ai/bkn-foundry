// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package modelmanageraudit

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

const sourceID = "model-manager"

type Client struct {
	baseURL string
	http    *http.Client
}

func New(url string, c *http.Client) *Client {
	if c == nil {
		c = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(strings.TrimSpace(url), "/"), http: c}
}
func (c *Client) ID() string { return sourceID }
func (c *Client) Metadata() observabilityvo.SourceStatus {
	if c.baseURL == "" {
		return observabilityvo.SourceStatus{SourceID: sourceID, Status: "not_integrated", Reason: "source_not_configured", Reliability: "best_effort", CollectionMethod: "not_integrated", CoveredModules: []string{"model_management"}, CountAccuracy: "partial", Categories: []string{observabilityvo.CategoryAuditAdmin}}
	}
	return observabilityvo.SourceStatus{SourceID: sourceID, Status: "degraded", Reason: observabilityvo.SourceReasonPartialManagementAuditCoverage, Reliability: "best_effort", CollectionMethod: "source_adapter", CoveredModules: []string{"model_management"}, CountAccuracy: "partial", Categories: []string{observabilityvo.CategoryAuditAdmin}}
}
func (c *Client) Search(ctx context.Context, q observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	if !contains(q.AuthorizedCategories, observabilityvo.CategoryAuditAdmin) {
		return observabilityvo.SourcePage{CountAccuracy: "partial"}, nil
	}
	auth := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	if auth == "" || q.AuthorizedTenantID == "" {
		return observabilityvo.SourcePage{}, errors.New("model manager audit source requires caller authorization and trusted scope")
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
		v.Set("before_event_id", strip(q.PageBefore.LogID))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/mf-model-manager/v1/operation-audits?"+v.Encode(), nil)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	headers(req, auth, q.AuthorizedTenantID)
	resp, err := c.http.Do(req)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return observabilityvo.SourcePage{}, fmt.Errorf("model manager audit source returned status %d", resp.StatusCode)
	}
	var p struct {
		Entries []entry `json:"entries"`
		HasMore bool    `json:"has_more"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&p); err != nil {
		return observabilityvo.SourcePage{}, err
	}
	rs := make([]observabilityvo.LogRecord, 0, len(p.Entries))
	for _, e := range p.Entries {
		rs = append(rs, project(e))
	}
	n := int64(len(rs))
	if p.HasMore {
		n++
	}
	return observabilityvo.SourcePage{Records: rs, Count: n, CountAccuracy: "partial"}, nil
}
func (c *Client) Get(ctx context.Context, id string) (observabilityvo.LogRecord, bool, error) {
	auth := strings.TrimSpace(observabilityvo.SourceAuthorization(ctx))
	s := observabilityvo.SourceAccessScopeFromContext(ctx)
	if auth == "" || s.TenantID == "" {
		return observabilityvo.LogRecord{}, false, errors.New("model manager audit source requires caller authorization and trusted scope")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/mf-model-manager/v1/operation-audits/"+url.PathEscape(strip(id)), nil)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	headers(req, auth, s.TenantID)
	resp, err := c.http.Do(req)
	if err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return observabilityvo.LogRecord{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return observabilityvo.LogRecord{}, false, fmt.Errorf("model manager audit source returned status %d", resp.StatusCode)
	}
	var e entry
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&e); err != nil {
		return observabilityvo.LogRecord{}, false, err
	}
	return project(e), true, nil
}

type entry struct {
	EventID        string    `json:"event_id"`
	EventTime      time.Time `json:"event_time"`
	RecordedAt     time.Time `json:"recorded_at"`
	TenantID       string    `json:"tenant_id"`
	ActorID        string    `json:"actor_id"`
	ActorName      string    `json:"actor_name"`
	ActorType      string    `json:"actor_type"`
	AuthMethod     string    `json:"auth_method"`
	RequestID      string    `json:"request_id"`
	SourceChannel  string    `json:"source_channel"`
	Method         string    `json:"method"`
	Action         string    `json:"action"`
	TargetType     string    `json:"target_type"`
	TargetID       string    `json:"target_id"`
	TargetName     string    `json:"target_name"`
	Outcome        string    `json:"outcome"`
	FailureCode    string    `json:"failure_code"`
	FailureMessage string    `json:"failure_message"`
}

func project(e entry) observabilityvo.LogRecord {
	sev, text := 9, "INFO"
	if e.Outcome != "success" {
		sev, text = 17, "ERROR"
	}
	return observabilityvo.LogRecord{EventID: e.EventID, EventTime: e.EventTime, RecordedAt: e.RecordedAt, ActorNameSnapshot: first(e.ActorName, e.ActorID), ActorType: first(e.ActorType, "user"), AuthMethod: first(e.AuthMethod, "unknown"), SourceChannel: first(e.SourceChannel, "api"), BusinessModule: "model_management", Action: e.Action, TargetType: e.TargetType, TargetID: e.TargetID, TargetNameSnapshot: first(e.TargetName, e.TargetID), FailureCode: e.FailureCode, FailureMessage: e.FailureMessage, SchemaVersion: "1.0", LogID: sourceID + ":" + e.EventID, SourceID: sourceID, SourceLogID: e.EventID, Category: observabilityvo.CategoryAuditAdmin, EventName: "model_management.changed", EventTimestamp: e.EventTime, ObservedTimestamp: e.RecordedAt, SeverityNumber: sev, SeverityText: text, Outcome: e.Outcome, SafeSummary: strings.TrimSpace(strings.Join([]string{e.Method, e.Action, e.TargetType, first(e.TargetName, e.TargetID)}, " ")), ServiceName: "mf-model-manager", Environment: "unknown", TenantID: e.TenantID, ActorID: e.ActorID, EffectiveSubjectID: e.ActorID, RequestID: e.RequestID, IngressPrincipal: "model-manager", TrustLevel: "trusted", ResourceRef: &observabilityvo.ResourceRef{ResourceType: e.TargetType, ResourceID: e.TargetID}, Attributes: map[string]any{"method": e.Method}}
}
func headers(r *http.Request, a, t string) {
	r.Header.Set("Authorization", a)
	r.Header.Set("x-tenant-id", t)
}
func strip(id string) string { return strings.TrimPrefix(id, sourceID+":") }
func limit(v int) int {
	if v <= 0 {
		return 50
	}
	if v > 500 {
		return 500
	}
	return v
}
func contains(xs []string, w string) bool {
	for _, x := range xs {
		if x == w {
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
