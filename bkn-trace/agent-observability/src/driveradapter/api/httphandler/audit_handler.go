// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

// AuditHandler is the authenticated read boundary for bkn_audit. It is kept
// separate from the shared log route until bootstrap ownership is approved.
type AuditHandler struct {
	service    *auditsvc.Service
	authorizer *EvidenceHandler
}

func NewAuditHandler(service *auditsvc.Service, authorizer *EvidenceHandler) *AuditHandler {
	return &AuditHandler{service: service, authorizer: authorizer}
}

type auditListResponse struct {
	Data       []auditsvc.Record `json:"data"`
	NextCursor string            `json:"next_cursor,omitempty"`
	Incomplete bool              `json:"incomplete"`
}

func (handler *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeObservabilityError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}
	if handler.service == nil || handler.authorizer == nil {
		writeObservabilityError(w, r, http.StatusServiceUnavailable, "audit_query_unavailable", "Audit query is not configured")
		return
	}
	if !handler.authorizer.authorizeQueryGateway(w, r) {
		return
	}
	scope, ok := handler.authorizer.queryScopeFromRequest(w, r, false)
	if !ok || scope.AccessProfile == nil {
		return
	}
	query, err := parseAuditQuery(r)
	if err != nil {
		writeObservabilityError(w, r, http.StatusBadRequest, "invalid_audit_filter", err.Error())
		return
	}
	capabilities := observabilityvo.CapabilitiesFor(*scope.AccessProfile)
	allowed := make(map[string]bool, len(capabilities.AllowedLogCategories))
	for _, category := range capabilities.AllowedLogCategories {
		if category == observabilityvo.CategoryAuditAdmin || category == observabilityvo.CategoryAuditSecurity {
			allowed[category] = true
		}
	}
	subjectID := scope.AccessProfile.EffectiveSubjectID
	if subjectID == "" {
		subjectID = scope.AccountID
	}
	page, err := handler.service.Query(r.Context(), auditsvc.Principal{
		SubjectID: subjectID, AllowedCategories: allowed, CanReadSecurity: capabilities.SecurityAudit,
	}, query)
	if err != nil {
		switch {
		case errors.Is(err, auditsvc.ErrUnauthorized):
			writeObservabilityError(w, r, http.StatusForbidden, "audit_query_unauthorized", "the current access profile cannot query the requested Audit category")
		case errors.Is(err, auditsvc.ErrInvalidQuery):
			writeObservabilityError(w, r, http.StatusBadRequest, "invalid_audit_filter", "the Audit query exceeds its supported bounds")
		default:
			writeObservabilityError(w, r, http.StatusInternalServerError, "audit_query_failed", "center Audit ledger query failed")
		}
		return
	}
	response := auditListResponse{Data: page.Records, Incomplete: page.Incomplete}
	if page.Next != nil {
		response.NextCursor = encodeAuditCursor(*page.Next)
	}
	writeJSON(w, r, http.StatusOK, response)
}

func parseAuditQuery(r *http.Request) (auditsvc.Query, error) {
	values := r.URL.Query()
	categories := queryList(values["categories"])
	if len(categories) == 0 {
		return auditsvc.Query{}, errors.New("categories is required")
	}
	from, err := parseAuditTime(values.Get("time_from"), "time_from")
	if err != nil {
		return auditsvc.Query{}, err
	}
	to, err := parseAuditTime(values.Get("time_to"), "time_to")
	if err != nil {
		return auditsvc.Query{}, err
	}
	limit, err := parseBoundedInteger(values.Get("limit"), 50, 1, 200, "limit")
	if err != nil {
		return auditsvc.Query{}, err
	}
	var cursor *auditsvc.Position
	if raw := strings.TrimSpace(values.Get("cursor")); raw != "" {
		position, err := decodeAuditCursor(raw)
		if err != nil {
			return auditsvc.Query{}, errors.New("cursor is invalid")
		}
		cursor = &position
	}
	return auditsvc.Query{Categories: categories, From: from, To: to, SourceID: strings.TrimSpace(values.Get("source_id")),
		BusinessModule: strings.TrimSpace(values.Get("business_module")), ActorID: strings.TrimSpace(values.Get("actor_id")),
		TargetType: strings.TrimSpace(values.Get("target_type")), TargetID: strings.TrimSpace(values.Get("target_id")),
		Action: strings.TrimSpace(values.Get("action")), Outcome: strings.TrimSpace(values.Get("outcome")), Cursor: cursor, Limit: limit}, nil
}

func parseAuditTime(raw, name string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, errors.New(name + " must be an RFC3339 timestamp")
	}
	return value.UTC(), nil
}

type encodedAuditCursor struct {
	OccurredAt string `json:"occurred_at"`
	EventID    string `json:"event_id"`
}

func encodeAuditCursor(position auditsvc.Position) string {
	payload, _ := json.Marshal(encodedAuditCursor{OccurredAt: position.OccurredAt.UTC().Format(time.RFC3339Nano), EventID: position.EventID})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeAuditCursor(raw string) (auditsvc.Position, error) {
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return auditsvc.Position{}, err
	}
	var cursor encodedAuditCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.EventID == "" {
		return auditsvc.Position{}, errors.New("invalid Audit cursor")
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, cursor.OccurredAt)
	if err != nil {
		return auditsvc.Position{}, err
	}
	return auditsvc.Position{OccurredAt: occurredAt.UTC(), EventID: cursor.EventID}, nil
}
