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
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	"ontology-query/interfaces"

	"github.com/bytedance/sonic"
	"go.opentelemetry.io/otel/trace"
)

const (
	ContractVersion = "2.1.0"
	ModuleName      = "bkn-ontology"
)

const (
	EntityKindObjectInstance = "object_instance"
	EntityKindRelationPath   = "relation_path"
	EntityKindMetric         = "metric"
)

const (
	RefTypeResource = "resource"
	RefTypeField    = "field"
	RefTypeObject   = "object"
	RefTypeProperty = "property"
	RefTypeRelation = "relation"
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
	InteractionID          string
	OperationID            string
	CausationEventID       string
	ClaimID                string
	Attempt                int
	ObservedAt             string
}

type DataQuerySubject struct {
	EntityKind    string
	Operation     string
	KNID          string
	Branch        string
	SubjectID     string
	QueryHash     string
	ReturnedCount int
	TotalCount    int64
	Truncated     bool
}

type EvidenceRef struct {
	RefID          string
	RefType        string
	PartialReasons []string
	Summary        map[string]any
}

type eventContext struct {
	traceID          string
	spanID           string
	traceparent      string
	requestID        string
	accountID        string
	accountType      string
	interactionID    string
	operationID      string
	causationEventID string
	attempt          int
	observedAt       string
}

func HashValue(value any) string {
	raw, err := sonic.ConfigStd.Marshal(value)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", value))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func BuildDataQueryEvents(ctx context.Context, reqCtx RequestContext, subject DataQuerySubject, refs []EvidenceRef) []Event {
	ec, ok := contextFromRequest(ctx, reqCtx)
	if !ok {
		return nil
	}
	operation := strings.TrimSpace(subject.Operation)
	if operation == "" {
		operation = "bkn.data.query"
	}

	queryType := strings.TrimSpace(subject.EntityKind)
	resourceRefs, fieldRefs := queryRefs(refs)
	readEvent := buildEvent(ec, "data.query.observed", operation, map[string]any{
		"query_hash": strings.TrimSpace(subject.QueryHash), "query_type": queryType,
		"row_count": subject.ReturnedCount, "truncated": subject.Truncated, "version_status": "unversioned",
		"resource_refs": resourceRefs, "field_refs": fieldRefs,
	})
	return []Event{readEvent}
}

func EmitDataQueryEvents(ctx context.Context, reqCtx RequestContext, subject DataQuerySubject, refs []EvidenceRef) string {
	if !EvidenceEnabled() {
		return ""
	}
	events := BuildDataQueryEvents(ctx, reqCtx, subject, refs)
	SubmitEvents(ctx, reqCtx, events)
	if len(events) == 0 {
		return ""
	}
	eventID, _ := events[0]["event_id"].(string)
	return eventID
}

func SubmitEvents(ctx context.Context, reqCtx RequestContext, events []Event) {
	if len(events) == 0 {
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
				"event_id": coreEvent.EventID, "event_type": coreEvent.EventType, "conversation_id": coreEvent.ConversationID,
				"interaction_id": coreEvent.InteractionID, "operation_id": coreEvent.OperationID,
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
	return coreEvidenceEvent{
		EventID: eventID, EventType: eventType, ConversationID: "conv_" + ec.requestID,
		InteractionID: ec.interactionID, OperationID: ec.operationID,
		Attempt: uint32(ec.attempt), RequestID: ec.requestID, TraceID: ec.traceID, SpanID: ec.spanID,
		StartedAt: observedAt, ObservedAt: observedAt, EmittedAt: observedAt, Envelope: raw,
	}, nil
}

func ObjectRowRefs(knID, branch, objectTypeID string, rows []map[string]any) []EvidenceRef {
	knID = strings.TrimSpace(knID)
	objectTypeID = strings.TrimSpace(objectTypeID)
	if knID == "" || objectTypeID == "" {
		return nil
	}
	return []EvidenceRef{{RefID: "object:" + knID + ":" + objectTypeID, RefType: RefTypeObject}}
}

func SubgraphRefs(knID, branch string, graph *interfaces.ObjectSubGraph) []EvidenceRef {
	knID = strings.TrimSpace(knID)
	if graph == nil || knID == "" {
		return nil
	}
	refs := make([]EvidenceRef, 0, len(graph.Objects)+len(graph.RelationPaths))
	seenObjects := map[string]bool{}
	for _, object := range graph.Objects {
		objectTypeID := strings.TrimSpace(object.ObjectTypeId)
		if objectTypeID != "" && !seenObjects[objectTypeID] {
			seenObjects[objectTypeID] = true
			refs = append(refs, EvidenceRef{RefID: "object:" + knID + ":" + objectTypeID, RefType: RefTypeObject})
		}
	}
	seenRelations := map[string]bool{}
	for _, path := range graph.RelationPaths {
		for _, relation := range path.Relations {
			relationTypeID := strings.TrimSpace(relation.RelationTypeId)
			if relationTypeID == "" || seenRelations[relationTypeID] {
				continue
			}
			seenRelations[relationTypeID] = true
			refs = append(refs, EvidenceRef{
				RefID:          "relation:" + knID + ":" + relationTypeID,
				RefType:        RefTypeRelation,
				PartialReasons: []string{"schema_ref_unversioned"},
				Summary: map[string]any{
					"kind":             "relation_type",
					"kn_id":            strings.TrimSpace(knID),
					"branch":           strings.TrimSpace(branch),
					"relation_type_id": relationTypeID,
				},
			})
		}
	}
	return refs
}

func MetricDataRefs(knID, branch, metricID string, rows []interfaces.Data) []EvidenceRef {
	knID = strings.TrimSpace(knID)
	metricID = strings.TrimSpace(metricID)
	if knID == "" || metricID == "" {
		return nil
	}
	return []EvidenceRef{{RefID: "metric:" + knID + ":" + metricID, RefType: RefTypeMetric}}
}

func queryRefs(refs []EvidenceRef) ([]map[string]any, []map[string]any) {
	resourceRefs := make([]map[string]any, 0, len(refs))
	fieldRefs := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		refID := strings.TrimSpace(ref.RefID)
		refType := strings.TrimSpace(ref.RefType)
		if refID == "" || refType == "" {
			continue
		}
		controlled := map[string]any{
			"ref_id":         refID,
			"ref_type":       refType,
			"source_system":  "bkn",
			"validity":       "observed",
			"version_status": "unversioned",
			"visibility":     "visible",
		}
		switch refType {
		case RefTypeResource, RefTypeObject, RefTypeRelation, RefTypeMetric:
			resourceRefs = append(resourceRefs, controlled)
		case RefTypeField, RefTypeProperty:
			fieldRefs = append(fieldRefs, controlled)
		}
	}
	return resourceRefs, fieldRefs
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
	flags := "00"
	if spanContext.TraceFlags().IsSampled() {
		flags = "01"
	}
	return eventContext{
		traceID:          spanContext.TraceID().String(),
		spanID:           spanContext.SpanID().String(),
		traceparent:      fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags),
		requestID:        requestID,
		accountID:        accountID,
		accountType:      accountType,
		interactionID:    interactionID,
		operationID:      operationID,
		causationEventID: strings.TrimSpace(reqCtx.CausationEventID),
		attempt:          normalizedAttempt(reqCtx.Attempt),
		observedAt:       observedAt,
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
		"attempt":                  ec.attempt,
		"payload":                  payload,
	}
	if ec.causationEventID != "" {
		event["causation_event_id"] = ec.causationEventID
	}
	return event
}

func stableEventID(traceID, operationID, eventType string, attempt int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d", traceID, operationID, eventType, attempt)))
	return "evt_" + hex.EncodeToString(sum[:])
}

func normalizedAttempt(attempt int) int {
	if attempt > 0 {
		return attempt
	}
	return 1
}
