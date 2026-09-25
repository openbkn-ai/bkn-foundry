// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
)

type auditLedgerSource struct{ reader *auditstore.Reader }

func NewAuditLedgerSource(reader *auditstore.Reader) *auditLedgerSource {
	return &auditLedgerSource{reader: reader}
}
func (s *auditLedgerSource) ID() string                   { return "audit-ledger" }
func (s *auditLedgerSource) SupportsSourceIDFilter() bool { return true }
func (s *auditLedgerSource) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{SourceID: s.ID(), Status: "degraded", Reason: "producer_coverage_unverified", Reliability: "best_effort", CollectionMethod: "kafka_audit", CountAccuracy: "partial", Categories: []string{"audit.admin", "audit.security"}}
}
func (s *auditLedgerSource) Search(ctx context.Context, q observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	if q.TimeFrom == nil || q.TimeTo == nil {
		return observabilityvo.SourcePage{}, nil
	}
	var cursor *auditsvc.Position
	if q.PageBefore != nil {
		cursor = &auditsvc.Position{OccurredAt: q.PageBefore.EventTimestamp, EventID: q.PageBefore.LogID}
	}
	query := auditsvc.Query{
		Categories: auditQueryCategories(q), From: q.TimeFrom.UTC(), To: q.TimeTo.UTC(),
		SourceID: q.SourceID, BusinessModule: q.BusinessModule, ActorID: q.ActorID,
		TargetType: q.TargetType, TargetID: q.TargetID, Action: q.Action, Outcomes: q.Outcomes,
		Cursor: cursor, Limit: q.Limit,
	}
	if q.ObservedBefore != nil {
		query.ObservedBefore = q.ObservedBefore.UTC()
	}
	p, err := s.reader.Query(ctx, query)
	if err != nil {
		return observabilityvo.SourcePage{}, err
	}
	out := observabilityvo.SourcePage{CountAccuracy: "partial"}
	for _, r := range p.Records {
		out.Records = append(out.Records, auditLogRecord(r))
	}
	out.Count = int64(len(out.Records))
	if len(out.Records) > 0 {
		x := out.Records[len(out.Records)-1]
		out.LastPosition = &observabilityvo.SourcePosition{EventTimestamp: x.EventTimestamp, LogID: x.LogID}
	}
	return out, nil
}

func auditQueryCategories(query observabilityvo.LogQuery) []string {
	authorized := make(map[string]struct{}, len(query.AuthorizedCategories))
	for _, category := range query.AuthorizedCategories {
		if category == observabilityvo.CategoryAuditAdmin || category == observabilityvo.CategoryAuditSecurity {
			authorized[category] = struct{}{}
		}
	}
	requested := query.Categories
	if len(requested) == 0 {
		requested = []string{observabilityvo.CategoryAuditAdmin, observabilityvo.CategoryAuditSecurity}
	}
	result := make([]string, 0, len(requested))
	for _, category := range requested {
		if _, ok := authorized[category]; ok {
			result = append(result, category)
		}
	}
	return result
}

func (s *auditLedgerSource) Get(ctx context.Context, id string) (observabilityvo.LogRecord, bool, error) {
	r, ok, e := s.reader.Get(ctx, id)
	if !ok || e != nil {
		return observabilityvo.LogRecord{}, ok, e
	}
	return auditLogRecord(r), true, nil
}
func auditLogRecord(r auditsvc.Record) observabilityvo.LogRecord {
	actorName := r.ActorNameSnapshot
	if actorName == "" {
		actorName = r.ActorID
	}
	targetName := r.TargetNameSnapshot
	if targetName == "" {
		targetName = r.TargetID
	}
	attributes := map[string]any{}
	if r.Transport != "" {
		attributes["transport"] = r.Transport
	}
	if r.Method != "" {
		attributes["method"] = r.Method
	}
	if r.HTTPStatus != 0 {
		attributes["status_code"] = r.HTTPStatus
	}
	if r.ClientIP != "" {
		attributes["client_ip"] = r.ClientIP
	}
	return observabilityvo.LogRecord{
		SchemaVersion: "1.0", EventID: r.EventID, LogID: r.EventID,
		SourceID: r.SourceID, SourceLogID: r.EventID, Category: r.Category, EventName: r.EventName,
		EventTime: r.OccurredAt, EventTimestamp: r.OccurredAt, ObservedTimestamp: r.BrokerReceivedAt, RecordedAt: r.RecordedAt,
		ActorID: r.ActorID, EffectiveSubjectID: r.EffectiveSubjectID, ActorNameSnapshot: actorName,
		ActorType: r.ActorType, AuthMethod: r.AuthMethod, SourceChannel: r.SourceChannel,
		BusinessModule: r.BusinessModule, TargetType: r.TargetType, TargetID: r.TargetID,
		TargetNameSnapshot: targetName, Action: r.Action, Outcome: r.Outcome, SafeSummary: r.Summary, FailureCode: r.FailureCode,
		ServiceName: r.SourceID, Environment: r.Environment, IngressPrincipal: "audit-kafka-validator",
		SeverityNumber: 9, SeverityText: "INFO", TrustLevel: "trusted", ApplicationID: r.ApplicationID,
		KnowledgeNetworkIDs: append([]string(nil), r.KnowledgeNetworkIDs...), RequestID: r.RequestID,
		TraceID: r.TraceID, OperationID: r.OperationID,
		ResourceRef: &observabilityvo.ResourceRef{ResourceType: r.TargetType, ResourceID: r.TargetID},
		Attributes:  attributes,
	}
}
