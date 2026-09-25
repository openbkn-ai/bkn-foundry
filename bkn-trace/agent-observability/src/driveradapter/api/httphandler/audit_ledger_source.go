package httphandler

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
	"time"
)

type auditLedgerSource struct{ reader *auditstore.Reader }

func NewAuditLedgerSource(reader *auditstore.Reader) *auditLedgerSource {
	return &auditLedgerSource{reader: reader}
}
func (s *auditLedgerSource) ID() string { return "audit-ledger" }
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
	p, err := s.reader.Query(ctx, auditsvc.Query{Categories: q.Categories, From: q.TimeFrom.UTC(), To: q.TimeTo.UTC(), BusinessModule: q.BusinessModule, ActorID: q.ActorID, TargetType: q.TargetType, TargetID: q.TargetID, Action: q.Action, EventNames: q.EventNames, Outcomes: q.Outcomes, FailedOnly: q.FailedOnly, Cursor: cursor, Limit: q.Limit})
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
func (s *auditLedgerSource) Get(ctx context.Context, id string) (observabilityvo.LogRecord, bool, error) {
	r, ok, e := s.reader.Get(ctx, id)
	if !ok || e != nil {
		return observabilityvo.LogRecord{}, ok, e
	}
	return auditLogRecord(r), true, nil
}
func auditLogRecord(r auditsvc.Record) observabilityvo.LogRecord {
	return observabilityvo.LogRecord{SchemaVersion: "1.0", EventID: r.EventID, LogID: r.EventID, SourceID: r.SourceID, SourceLogID: r.EventID, Category: r.Category, EventName: r.EventName, EventTime: r.OccurredAt, EventTimestamp: r.OccurredAt, ObservedTimestamp: time.Now().UTC(), RecordedAt: r.OccurredAt, ActorID: r.ActorID, EffectiveSubjectID: r.EffectiveSubjectID, ActorNameSnapshot: r.ActorNameSnapshot, ActorType: r.ActorType, AuthMethod: r.AuthMethod, SourceChannel: r.SourceChannel, BusinessModule: r.BusinessModule, TargetType: r.TargetType, TargetID: r.TargetID, Action: r.Action, Outcome: r.Outcome, SafeSummary: r.Summary, ServiceName: r.SourceID, Environment: r.Environment, IngressPrincipal: "audit-kafka-validator", SeverityNumber: 9, SeverityText: "INFO", TrustLevel: "trusted", Attributes: map[string]any{}}
}
