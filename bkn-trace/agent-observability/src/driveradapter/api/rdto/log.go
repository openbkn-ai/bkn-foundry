// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package rdto

import (
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

// OperationAuditRecord is the public 0.1.4 operation-audit contract. The
// internal LogRecord remains a source-normalization model and must not leak
// legacy technical log fields through the HTTP API.
type OperationAuditRecord struct {
	SchemaVersion     string                    `json:"schema_version"`
	EventID           string                    `json:"event_id"`
	LogCategory       string                    `json:"log_category"`
	EventName         string                    `json:"event_name"`
	EventTime         time.Time                 `json:"event_time"`
	RecordedAt        time.Time                 `json:"recorded_at"`
	ActorID           string                    `json:"actor_id"`
	ActorNameSnapshot string                    `json:"actor_name_snapshot"`
	ActorType         string                    `json:"actor_type,omitempty"`
	AuthMethod        string                    `json:"auth_method"`
	CredentialID      string                    `json:"credential_id,omitempty"`
	CredentialName    string                    `json:"credential_name,omitempty"`
	TenantID          string                    `json:"tenant_id"`
	BusinessDomainID  string                    `json:"business_domain_id,omitempty"`
	SourceChannel     string                    `json:"source_channel"`
	SourceID          string                    `json:"source_id"`
	BusinessModule    string                    `json:"business_module"`
	Outcome           string                    `json:"outcome"`
	FailureCode       string                    `json:"failure_code,omitempty"`
	FailureMessage    string                    `json:"failure_message,omitempty"`
	Facts             OperationAuditFacts       `json:"facts"`
	Correlation       OperationAuditCorrelation `json:"correlation"`
	Attributes        map[string]any            `json:"attributes"`
}

type OperationAuditFacts struct {
	Action             string         `json:"action"`
	Method             string         `json:"method,omitempty"`
	StatusCode         int            `json:"status_code,omitempty"`
	ClientIP           string         `json:"client_ip,omitempty"`
	TargetType         string         `json:"target_type"`
	TargetID           string         `json:"target_id"`
	TargetNameSnapshot string         `json:"target_name_snapshot"`
	Detail             map[string]any `json:"detail,omitempty"`
	OperationType      string         `json:"operation_type,omitempty"`
	OperationStatus    string         `json:"operation_status,omitempty"`
	BusinessContext    string         `json:"business_context,omitempty"`
}

type OperationAuditCorrelation struct {
	RequestID      string `json:"request_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
	SpanID         string `json:"span_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	InteractionID  string `json:"interaction_id,omitempty"`
	OperationID    string `json:"operation_id,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
}

func NewOperationAuditRecord(record observabilityvo.LogRecord) OperationAuditRecord {
	eventID := record.EventID
	if eventID == "" {
		eventID = record.LogID
	}
	eventTime := record.EventTime
	if eventTime.IsZero() {
		eventTime = record.EventTimestamp
	}
	recordedAt := record.RecordedAt
	if recordedAt.IsZero() {
		recordedAt = record.ObservedTimestamp
	}
	return OperationAuditRecord{
		SchemaVersion: "1.0", EventID: eventID, LogCategory: record.Category, EventName: record.EventName,
		EventTime: eventTime, RecordedAt: recordedAt,
		ActorID: record.ActorID, ActorNameSnapshot: record.ActorNameSnapshot, ActorType: record.ActorType,
		AuthMethod: record.AuthMethod, CredentialID: record.CredentialID, CredentialName: record.CredentialName,
		TenantID: record.TenantID, BusinessDomainID: record.BusinessDomain,
		SourceChannel: record.SourceChannel, SourceID: record.SourceID, BusinessModule: record.BusinessModule,
		Outcome:     record.Outcome,
		FailureCode: record.FailureCode, FailureMessage: record.FailureMessage,
		Facts: OperationAuditFacts{
			Action: record.Action, Method: stringAttribute(record.Attributes, "method"),
			StatusCode: intAttribute(record.Attributes, "status_code"), ClientIP: stringAttribute(record.Attributes, "client_ip"),
			TargetType: record.TargetType, TargetID: record.TargetID, TargetNameSnapshot: record.TargetNameSnapshot,
			Detail: mapAttribute(record.Attributes, "detail"), OperationType: stringAttribute(record.Attributes, "operation_type"),
			OperationStatus: stringAttribute(record.Attributes, "operation_status"), BusinessContext: stringAttribute(record.Attributes, "business_context"),
		},
		Correlation: OperationAuditCorrelation{
			RequestID: record.RequestID, TraceID: record.TraceID, SpanID: record.SpanID,
			ConversationID: record.ConversationID, InteractionID: record.InteractionID,
			OperationID: record.OperationID, TaskID: record.TaskID,
		},
		Attributes: extensionAttributes(record.Attributes),
	}
}

func stringAttribute(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func intAttribute(values map[string]any, key string) int {
	switch value := values[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}

func mapAttribute(values map[string]any, key string) map[string]any {
	value, _ := values[key].(map[string]any)
	return value
}

func extensionAttributes(values map[string]any) map[string]any {
	result := make(map[string]any)
	for key, value := range values {
		switch key {
		case "method", "status_code", "client_ip", "detail", "operation_type", "operation_status", "business_context":
			continue
		default:
			result[key] = value
		}
	}
	return result
}

func NewOperationAuditRecords(records []observabilityvo.LogRecord) []OperationAuditRecord {
	result := make([]OperationAuditRecord, 0, len(records))
	for _, record := range records {
		result = append(result, NewOperationAuditRecord(record))
	}
	return result
}

type LogCount struct {
	Value    *int64 `json:"value"`
	Accuracy string `json:"accuracy"`
}

type PageMetadata struct {
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

type RequestTraceContext struct {
	RequestID       *string  `json:"request_id"`
	CurrentTraceID  *string  `json:"current_trace_id,omitempty"`
	RelatedTraceIDs []string `json:"related_trace_ids"`
}

type LogListResponse struct {
	Data                []OperationAuditRecord         `json:"data"`
	NextCursor          *string                        `json:"next_cursor"`
	Partial             bool                           `json:"partial"`
	Count               LogCount                       `json:"count"`
	Pagination          PageMetadata                   `json:"pagination"`
	SourceStatus        []observabilityvo.SourceStatus `json:"source_status"`
	RequestTraceContext RequestTraceContext            `json:"request_trace_context"`
}

type LogFieldProjection struct {
	PolicyRevision string   `json:"policy_revision"`
	RedactedFields []string `json:"redacted_fields"`
}

type LogDetailResponse struct {
	Data                OperationAuditRecord `json:"data"`
	FieldProjection     LogFieldProjection   `json:"field_projection"`
	RequestTraceContext RequestTraceContext  `json:"request_trace_context"`
}

type LogSourcesResponse struct {
	Data []observabilityvo.SourceStatus `json:"data"`
}

type LogPoliciesResponse struct {
	Data []observabilityvo.LogPolicy `json:"data"`
}
