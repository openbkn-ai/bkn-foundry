// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"

	"bkn-backend/interfaces"
)

const (
	// ContractVersion is retained for the existing internal evidence builders.
	// SubmitEvents wraps those sanitized facts in the Core 3.0 immutable event.
	ContractVersion = "2.1.0"
	ModuleName      = "bkn-backend"
)

const (
	envProducerStreamID = "BKN_TRACE_PRODUCER_STREAM_ID"
)

const (
	EntityKindObjectType   = "object_type"
	EntityKindRelationType = "relation_type"
	EntityKindActionType   = "action_type"
	EntityKindMetric       = "metric"
)

const (
	RefTypeObject   = "object"
	RefTypeProperty = "property"
	RefTypeRelation = "relation"
	RefTypeAction   = "action"
	RefTypeMetric   = "metric"
)

type Event map[string]any

type RequestContext struct {
	RequestID              string
	AccountID              string
	AccountType            string
	ApplicationPrincipalID string
	EffectiveSubjectID     string
	EffectiveSubjectType   string
	DelegationID           string
	ConversationID         string
	InteractionID          string
	SessionScopePresent    bool
	OperationID            string
	OperationScopePresent  bool
	ParentOperationID      string
	CausationEventID       string
	ClaimID                string
	Attempt                uint32
	ObservedAt             string
}

type ReadSubject struct {
	EntityKind    string
	Operation     string
	KNID          string
	Branch        string
	RequestedIDs  []string
	ReturnedCount int
	TotalCount    int64
}

type EvidenceRef struct {
	RefID          string
	RefType        string
	PartialReasons []string
	Summary        map[string]any
}

type eventContext struct {
	traceID                string
	spanID                 string
	traceparent            string
	requestID              string
	accountID              string
	accountType            string
	applicationPrincipalID string
	effectiveSubjectID     string
	effectiveSubjectType   string
	delegationID           string
	conversationID         string
	interactionID          string
	operationID            string
	operationScopePresent  bool
	parentOperationID      string
	causationEventID       string
	attempt                uint32
	observedAt             string
}

var (
	safeErrorCodeRE = regexp.MustCompile(`^[0-9A-Za-z_.-]{1,128}$`)
	safeErrorPathRE = regexp.MustCompile(`^\$(?:\.[0-9A-Za-z_.-]+|\[[0-9]+\])+$`)
)

func EvidenceEnabled() bool {
	return currentEvidencePublisher() != nil
}

func HashValue(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", value))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func BuildSchemaReadEvents(ctx context.Context, reqCtx RequestContext, subject ReadSubject, refs []EvidenceRef) []Event {
	ec, ok := contextFromRequest(ctx, reqCtx)
	if !ok {
		return nil
	}
	operation := strings.TrimSpace(subject.Operation)
	if operation == "" {
		operation = "bkn.schema.read"
	}

	_, businessRefs := controlledRefs(refs, subject.KNID)
	readEvent := buildEvent(ec, "knowledge.read.observed", operation, map[string]any{
		"kn_id":          strings.TrimSpace(subject.KNID),
		"read_kind":      strings.TrimSpace(subject.EntityKind),
		"version_status": "unversioned",
		"business_refs":  businessRefs,
	})
	return []Event{readEvent}
}

func controlledRefs(refs []EvidenceRef, knID string) ([]map[string]any, []map[string]any) {
	evidenceRefs := make([]map[string]any, 0, len(refs))
	businessRefs := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		refID := strings.TrimSpace(ref.RefID)
		refType := strings.TrimSpace(ref.RefType)
		if refID == "" || refType == "" {
			continue
		}
		if !isQualifiedBusinessRef(refID, refType, knID) {
			continue
		}
		businessType := refType
		switch businessType {
		case RefTypeObject, RefTypeProperty, RefTypeRelation, RefTypeAction, RefTypeMetric:
		default:
			continue
		}
		evidenceRefs = append(evidenceRefs, map[string]any{
			"ref_id": refID, "ref_type": refType, "source_system": "bkn",
			"validity": "observed", "version_status": "unversioned", "visibility": "visible",
			"summary_hash": HashValue(ref.Summary),
		})
		businessRefs = append(businessRefs, map[string]any{
			"ref_id": refID, "ref_type": businessType, "source_system": "bkn",
			"validity": "available", "version_status": "unversioned", "visibility": "visible",
		})
	}
	return evidenceRefs, businessRefs
}

func isQualifiedBusinessRef(refID, refType, knID string) bool {
	parts := strings.Split(refID, ":")
	knID = strings.TrimSpace(knID)
	if knID == "" || len(parts) < 3 || parts[1] != knID {
		return false
	}
	switch refType {
	case RefTypeObject:
		return parts[0] == "object" && len(parts) == 3 && parts[2] != ""
	case RefTypeProperty:
		return parts[0] == "property" && len(parts) == 4 && parts[2] != "" && parts[3] != ""
	case RefTypeRelation:
		return parts[0] == "relation" && len(parts) == 3 && parts[2] != ""
	case RefTypeAction:
		return parts[0] == "action_type" && len(parts) == 3 && parts[2] != ""
	case RefTypeMetric:
		return parts[0] == "metric" && len(parts) == 3 && parts[2] != ""
	default:
		return false
	}
}

func EmitSchemaReadEvents(ctx context.Context, reqCtx RequestContext, subject ReadSubject, refs []EvidenceRef) string {
	if !EvidenceEnabled() {
		return ""
	}
	events := BuildSchemaReadEvents(ctx, reqCtx, subject, refs)
	SubmitEvents(ctx, reqCtx, events)
	if len(events) == 0 || !hasSessionScope(reqCtx) || !hasVerifiedOwner(reqCtx) {
		return ""
	}
	eventID, _ := events[0]["event_id"].(string)
	return eventID
}

func SubmitEvents(ctx context.Context, reqCtx RequestContext, events []Event) {
	if len(events) == 0 {
		return
	}
	if !hasSessionScope(reqCtx) {
		log.Printf("BKN Trace evidence publisher dropped events reason=missing_trusted_session_scope")
		return
	}
	if !hasVerifiedOwner(reqCtx) {
		log.Printf("BKN Trace evidence publisher dropped events reason=missing_verified_owner")
		return
	}
	ec, ok := contextFromRequest(ctx, reqCtx)
	if !ok {
		return
	}
	for _, event := range events {
		coreEvent, err := toCoreEvent(event, ec)
		if err != nil {
			log.Printf("BKN Trace evidence publisher rejected event: %v", err)
			continue
		}
		if publisher := currentEvidencePublisher(); publisher != nil {
			result := publishEvidenceEvent(ctx, Event{
				"event_id": coreEvent.EventID, "event_type": coreEvent.EventType,
				"conversation_id": coreEvent.ConversationID,
				"interaction_id":  coreEvent.InteractionID, "operation_id": coreEvent.OperationID,
				"attempt": coreEvent.Attempt, "request_id": coreEvent.RequestID, "trace_id": coreEvent.TraceID,
				"span_id": coreEvent.SpanID, "started_at": coreEvent.StartedAt.Format(time.RFC3339Nano),
				"observed_at": coreEvent.ObservedAt.Format(time.RFC3339Nano), "emitted_at": coreEvent.EmittedAt.Format(time.RFC3339Nano),
				"envelope": json.RawMessage(coreEvent.Envelope),
			})
			if result.Disposition != evidencepublisher.Accepted {
				log.Printf("BKN Trace evidence publisher dropped event_id=%s reason=%s", coreEvent.EventID, result.Reason)
			}
			continue
		}
		log.Printf("BKN Trace evidence publisher unavailable; dropped event_id=%s", coreEvent.EventID)
	}
}

func hasSessionScope(reqCtx RequestContext) bool {
	return reqCtx.SessionScopePresent && strings.TrimSpace(reqCtx.ConversationID) != "" && strings.TrimSpace(reqCtx.InteractionID) != ""
}

func hasVerifiedOwner(reqCtx RequestContext) bool {
	return strings.TrimSpace(reqCtx.ApplicationPrincipalID) != "" && strings.TrimSpace(reqCtx.EffectiveSubjectID) != "" && strings.TrimSpace(reqCtx.EffectiveSubjectType) != ""
}

type coreEvidenceEvent struct {
	EventID, EventType, ConversationID, InteractionID, OperationID string
	Attempt                                                        uint32
	RequestID, TraceID, SpanID                                     string
	StartedAt, ObservedAt, EmittedAt                               time.Time
	Envelope                                                       json.RawMessage
}

func toCoreEvent(event Event, ec eventContext) (coreEvidenceEvent, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return coreEvidenceEvent{}, err
	}
	observedAt, err := time.Parse(time.RFC3339Nano, ec.observedAt)
	if err != nil {
		return coreEvidenceEvent{}, err
	}
	eventID, _ := event["event_id"].(string)
	eventType, _ := event["event_type"].(string)
	if eventID == "" || eventType == "" {
		return coreEvidenceEvent{}, errors.New("evidence event ID and type are required")
	}
	coreOperationID := ""
	if ec.operationScopePresent {
		coreOperationID = ec.operationID
	}
	return coreEvidenceEvent{
		EventID: eventID, EventType: eventType,
		ConversationID: ec.conversationID, InteractionID: ec.interactionID, OperationID: coreOperationID,
		Attempt: ec.attempt, RequestID: ec.requestID, TraceID: ec.traceID, SpanID: ec.spanID,
		StartedAt: observedAt, ObservedAt: observedAt, EmittedAt: observedAt, Envelope: raw,
	}, nil
}

func ObjectTypeRefs(items []*interfaces.ObjectType) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		knID := strings.TrimSpace(item.KNID)
		if strings.TrimSpace(item.OTID) == "" || knID == "" {
			continue
		}
		objectTypeID := strings.TrimSpace(item.OTID)
		summary := map[string]any{
			"kind":        EntityKindObjectType,
			"id":          strings.TrimSpace(item.OTID),
			"kn_id":       strings.TrimSpace(item.KNID),
			"branch":      strings.TrimSpace(item.Branch),
			"module_type": strings.TrimSpace(item.ModuleType),
			"has_status":  item.Status != nil,
			"update_time": item.UpdateTime,
		}
		if item.DataProperties != nil || item.LogicProperties != nil {
			summary["property_count"] = len(item.DataProperties) + len(item.LogicProperties)
			summary["data_property_count"] = len(item.DataProperties)
			summary["logic_property_count"] = len(item.LogicProperties)
		}
		if item.PrimaryKeys != nil {
			summary["primary_key_count"] = len(item.PrimaryKeys)
		}
		refs = append(refs, EvidenceRef{
			RefID:   "object:" + knID + ":" + objectTypeID,
			RefType: RefTypeObject,
			Summary: summary,
		})
		for _, property := range item.DataProperties {
			if property != nil && strings.TrimSpace(property.Name) != "" {
				refs = append(refs, EvidenceRef{RefID: "property:" + knID + ":" + objectTypeID + ":" + strings.TrimSpace(property.Name), RefType: RefTypeProperty})
			}
		}
		for _, property := range item.LogicProperties {
			if property != nil && strings.TrimSpace(property.Name) != "" {
				refs = append(refs, EvidenceRef{RefID: "property:" + knID + ":" + objectTypeID + ":" + strings.TrimSpace(property.Name), RefType: RefTypeProperty})
			}
		}
	}
	return refs
}

func RelationTypeRefs(items []*interfaces.RelationType) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		knID := strings.TrimSpace(item.KNID)
		if strings.TrimSpace(item.RTID) == "" || knID == "" {
			continue
		}
		refs = append(refs, EvidenceRef{
			RefID:   "relation:" + knID + ":" + strings.TrimSpace(item.RTID),
			RefType: RefTypeRelation,
			Summary: map[string]any{
				"kind":                  EntityKindRelationType,
				"id":                    strings.TrimSpace(item.RTID),
				"kn_id":                 strings.TrimSpace(item.KNID),
				"branch":                strings.TrimSpace(item.Branch),
				"module_type":           strings.TrimSpace(item.ModuleType),
				"source_object_type_id": strings.TrimSpace(item.SourceObjectTypeID),
				"target_object_type_id": strings.TrimSpace(item.TargetObjectTypeID),
				"relation_type":         strings.TrimSpace(item.Type),
				"has_mapping_rules":     item.MappingRules != nil,
				"update_time":           item.UpdateTime,
			},
		})
	}
	return refs
}

func ActionTypeRefs(items []*interfaces.ActionType) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		knID := strings.TrimSpace(item.KNID)
		if strings.TrimSpace(item.ATID) == "" || knID == "" {
			continue
		}
		refs = append(refs, EvidenceRef{
			RefID:          "action_type:" + knID + ":" + strings.TrimSpace(item.ATID),
			RefType:        RefTypeAction,
			PartialReasons: []string{"action_ref_unversioned"},
			Summary: map[string]any{
				"kind":                  EntityKindActionType,
				"id":                    strings.TrimSpace(item.ATID),
				"kn_id":                 strings.TrimSpace(item.KNID),
				"branch":                strings.TrimSpace(item.Branch),
				"module_type":           strings.TrimSpace(item.ModuleType),
				"object_type_id":        strings.TrimSpace(item.ObjectTypeID),
				"action_type":           strings.TrimSpace(item.ActionType),
				"parameter_count":       len(item.Parameters),
				"impact_contract_count": len(item.ImpactContracts),
				"update_time":           item.UpdateTime,
			},
		})
	}
	return refs
}

func MetricRefs(items []*interfaces.MetricDefinition) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		knID := strings.TrimSpace(item.KnID)
		if strings.TrimSpace(item.ID) == "" || knID == "" {
			continue
		}
		refs = append(refs, EvidenceRef{
			RefID:          "metric:" + knID + ":" + strings.TrimSpace(item.ID),
			RefType:        RefTypeMetric,
			PartialReasons: []string{"metric_ref_unversioned"},
			Summary: map[string]any{
				"kind":                     EntityKindMetric,
				"id":                       strings.TrimSpace(item.ID),
				"kn_id":                    strings.TrimSpace(item.KnID),
				"branch":                   strings.TrimSpace(item.Branch),
				"module_type":              strings.TrimSpace(item.ModuleType),
				"metric_type":              strings.TrimSpace(item.MetricType),
				"scope_type":               strings.TrimSpace(item.ScopeType),
				"scope_ref":                strings.TrimSpace(item.ScopeRef),
				"has_time_dimension":       item.TimeDimension != nil,
				"has_calculation_formula":  item.CalculationFormula != nil,
				"analysis_dimension_count": len(item.AnalysisDimensions),
				"update_time":              item.UpdateTime,
			},
		})
	}
	return refs
}

func contextFromRequest(ctx context.Context, reqCtx RequestContext) (eventContext, bool) {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return eventContext{}, false
	}
	requestID := strings.TrimSpace(reqCtx.RequestID)
	accountID := strings.TrimSpace(reqCtx.AccountID)
	accountType := strings.TrimSpace(reqCtx.AccountType)
	if requestID == "" || accountID == "" || accountType == "" {
		return eventContext{}, false
	}
	observedAt := strings.TrimSpace(reqCtx.ObservedAt)
	if _, err := time.Parse(time.RFC3339Nano, observedAt); err != nil {
		return eventContext{}, false
	}
	interactionID := strings.TrimSpace(reqCtx.InteractionID)
	operationID := strings.TrimSpace(reqCtx.OperationID)
	if interactionID == "" || operationID == "" {
		return eventContext{}, false
	}
	attempt := normalizedAttempt(reqCtx.Attempt)
	flags := "00"
	if spanContext.TraceFlags().IsSampled() {
		flags = "01"
	}
	return eventContext{
		traceID:                spanContext.TraceID().String(),
		spanID:                 spanContext.SpanID().String(),
		traceparent:            fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags),
		requestID:              requestID,
		accountID:              accountID,
		accountType:            accountType,
		applicationPrincipalID: strings.TrimSpace(reqCtx.ApplicationPrincipalID),
		effectiveSubjectID:     strings.TrimSpace(reqCtx.EffectiveSubjectID),
		effectiveSubjectType:   strings.TrimSpace(reqCtx.EffectiveSubjectType),
		delegationID:           strings.TrimSpace(reqCtx.DelegationID),
		conversationID:         strings.TrimSpace(reqCtx.ConversationID),
		interactionID:          interactionID,
		operationID:            operationID,
		operationScopePresent:  reqCtx.OperationScopePresent,
		parentOperationID:      strings.TrimSpace(reqCtx.ParentOperationID),
		causationEventID:       strings.TrimSpace(reqCtx.CausationEventID),
		attempt:                attempt,
		observedAt:             observedAt,
	}, true
}

func buildEvent(ec eventContext, eventType, operationName string, payload map[string]any) Event {
	now := ec.observedAt
	event := Event{
		"event_id":                 stableEventID(ec.traceID, ec.operationID, eventType, ec.attempt),
		"event_type":               eventType,
		"bkn.trace.schema.version": ContractVersion,
		"observed_at":              now,
		"emitted_at":               now,
		"producer_module":          ModuleName,
		"trace_id":                 ec.traceID,
		"span_id":                  ec.spanID,
		"bkn.request.id":           ec.requestID,
		"bkn.operation.name":       operationName,
		"interaction_id":           ec.interactionID,
		"operation_id":             ec.operationID,
		"attempt":                  int(ec.attempt),
		"payload":                  payload,
	}
	if ec.applicationPrincipalID != "" && ec.effectiveSubjectID != "" && ec.effectiveSubjectType != "" {
		owner := map[string]any{
			"application_principal_id": ec.applicationPrincipalID,
			"effective_subject_type":   ec.effectiveSubjectType,
			"effective_subject_id":     ec.effectiveSubjectID,
		}
		if ec.delegationID != "" {
			owner["delegation_id"] = ec.delegationID
		}
		event["owner"] = owner
	}
	if ec.causationEventID != "" {
		event["causation_event_id"] = ec.causationEventID
	}
	if ec.parentOperationID != "" {
		event["parent_operation_id"] = ec.parentOperationID
	}
	return event
}

func stableEventID(traceID, operationID, eventType string, attempt uint32) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d", traceID, operationID, eventType, attempt)))
	return "evt_" + hex.EncodeToString(sum[:])
}

func normalizedAttempt(attempt uint32) uint32 {
	if attempt > 0 {
		return attempt
	}
	return 1
}
