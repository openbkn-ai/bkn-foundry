// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/logsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

// AuditLogDelegate adapts the center-ledger service to the existing logs
// envelope. It never calls module operation-audit APIs.
type AuditLogDelegate struct{ service *auditsvc.Service }

func NewAuditLogDelegate(service *auditsvc.Service) *AuditLogDelegate {
	return &AuditLogDelegate{service: service}
}

func (delegate *AuditLogDelegate) List(ctx context.Context, profile evidencevo.AccessProfile, query observabilityvo.LogQuery) (observabilityvo.ListResult, error) {
	if delegate == nil || delegate.service == nil || query.TimeFrom == nil || query.TimeTo == nil || len(query.Outcomes) > 1 {
		return observabilityvo.ListResult{}, logsvc.ErrInvalidQuery
	}
	allowed := make(map[string]bool)
	capabilities := observabilityvo.CapabilitiesFor(profile)
	for _, category := range capabilities.AllowedLogCategories {
		if category == observabilityvo.CategoryAuditAdmin || category == observabilityvo.CategoryAuditSecurity {
			allowed[category] = true
		}
	}
	var cursor *auditsvc.Position
	if query.Cursor != "" {
		position, err := decodeAuditCursor(query.Cursor)
		if err != nil {
			return observabilityvo.ListResult{}, logsvc.ErrCursorInvalid
		}
		cursor = &position
	}
	subjectID := profile.EffectiveSubjectID
	if subjectID == "" {
		subjectID = profile.ActorID
	}
	auditQuery := auditsvc.Query{Categories: query.Categories, From: query.TimeFrom.UTC(), To: query.TimeTo.UTC(), SourceID: query.SourceID,
		BusinessModule: query.BusinessModule, ActorID: query.ActorID, TargetType: query.TargetType, TargetID: query.TargetID,
		Action: query.Action, Cursor: cursor, Limit: query.Limit}
	if len(query.Outcomes) == 1 {
		auditQuery.Outcome = query.Outcomes[0]
	}
	page, err := delegate.service.Query(ctx, auditsvc.Principal{SubjectID: subjectID, AllowedCategories: allowed, CanReadSecurity: capabilities.SecurityAudit}, auditQuery)
	if err != nil {
		switch {
		case errors.Is(err, auditsvc.ErrUnauthorized):
			return observabilityvo.ListResult{}, logsvc.ErrAccessDenied
		case errors.Is(err, auditsvc.ErrInvalidQuery):
			return observabilityvo.ListResult{}, logsvc.ErrInvalidQuery
		default:
			return observabilityvo.ListResult{}, err
		}
	}
	records := make([]observabilityvo.LogRecord, 0, len(page.Records))
	for _, record := range page.Records {
		records = append(records, observabilityvo.LogRecord{SchemaVersion: "1.0", EventID: record.EventID, LogID: record.EventID,
			SourceID: record.SourceID, SourceLogID: record.EventID, Category: record.Category, EventName: record.EventName,
			EventTime: record.OccurredAt, EventTimestamp: record.OccurredAt, ObservedTimestamp: record.OccurredAt, RecordedAt: record.OccurredAt,
			ActorID: record.ActorID, BusinessModule: record.BusinessModule, TargetType: record.TargetType, TargetID: record.TargetID,
			Action: record.Action, Outcome: record.Outcome, SeverityNumber: 9, SeverityText: "INFO", TrustLevel: "trusted",
			Attributes: map[string]any{}})
	}
	status := observabilityvo.SourceStatus{SourceID: "audit-ledger", Status: "healthy", Reliability: "best_effort", CollectionMethod: "kafka_audit",
		CoveredModules: []string{"execution_factory", "observability"}, Categories: []string{observabilityvo.CategoryAuditAdmin, observabilityvo.CategoryAuditSecurity}, CountAccuracy: "exact"}
	result := observabilityvo.ListResult{Records: records, Count: int64(len(records)), CountExact: true, Page: normalizedAuditPage(query.Page), PageSize: query.Limit, SourceStatus: []observabilityvo.SourceStatus{status}, Partial: page.Incomplete}
	if page.Next != nil {
		result.NextCursor = encodeAuditCursor(*page.Next)
	}
	return result, nil
}

func normalizedAuditPage(page int) int {
	if page <= 0 {
		return 1
	}
	return page
}
