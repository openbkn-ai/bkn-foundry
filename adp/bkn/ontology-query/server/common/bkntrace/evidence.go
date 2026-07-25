// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	"ontology-query/interfaces"
)

const (
	ContractVersion = "2.1.0"
	ModuleName      = "bkn-ontology"
)

const (
	envEvidenceIngestURL       = "BKN_TRACE_EVIDENCE_INGEST_URL"
	envEvidenceIngestTimeoutMS = "BKN_TRACE_EVIDENCE_TIMEOUT_MS"
)

const maxInFlightEvidenceBatches = 64

const (
	EntityKindObjectInstance = "object_instance"
	EntityKindRelationPath   = "relation_path"
	EntityKindMetric         = "metric"
)

const (
	RefTypeRow    = "row_ref"
	RefTypeSchema = "schema_ref"
	RefTypeMetric = "metric_ref"
)

type Event map[string]any

type RequestContext struct {
	RequestID        string
	AccountID        string
	AccountType      string
	BusinessDomain   string
	InteractionID    string
	OperationID      string
	CausationEventID string
	ClaimID          string
	Attempt          int
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

type batch struct {
	ContractVersion string         `json:"bkn.trace.schema.version"`
	Trace           map[string]any `json:"trace"`
	Events          []Event        `json:"events"`
}

type eventContext struct {
	traceID          string
	spanID           string
	traceparent      string
	requestID        string
	accountID        string
	accountType      string
	businessDomain   string
	interactionID    string
	operationID      string
	causationEventID string
	attempt          int
}

var (
	evidenceHTTPClient = &http.Client{}
	evidenceInFlight   = make(chan struct{}, maxInFlightEvidenceBatches)
)

func EvidenceEnabled() bool {
	return evidenceIngestURL() != ""
}

func HashValue(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", value))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ClaimID(kind, subjectID string, value any) string {
	sum := sha256.Sum256([]byte(HashValue(map[string]any{
		"kind":       kind,
		"subject_id": subjectID,
		"value":      value,
	})))
	return "claim_" + hex.EncodeToString(sum[:])[:24]
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
	readEvent := buildEvent(ec, "data.query.observed", operation, map[string]any{
		"query_hash": strings.TrimSpace(subject.QueryHash), "query_type": queryType,
		"row_count": subject.ReturnedCount, "truncated": subject.Truncated, "version_status": "unversioned",
	})
	events := []Event{readEvent}
	claimID := strings.TrimSpace(reqCtx.ClaimID)
	if claimID == "" {
		return events
	}
	readEvent["claim_id"] = claimID
	evidenceRefs, _ := normalizedRefs(refs)
	if len(evidenceRefs) == 0 {
		unresolvedEvent := buildEvent(ec, "business.refs.resolved", operation, map[string]any{
			"claim_id":        claimID,
			"resolver_status": "unresolved",
			"business_refs":   []map[string]any{},
		})
		unresolvedEvent["claim_id"] = claimID
		unresolvedEvent["causation_event_id"] = readEvent["event_id"]
		return append(events, unresolvedEvent)
	}
	evidenceEvent := buildEvent(ec, "evidence.refs.created", operation, map[string]any{
		"claim_id": claimID, "evidence_refs": evidenceRefs,
	})
	evidenceEvent["claim_id"] = claimID
	evidenceEvent["causation_event_id"] = readEvent["event_id"]
	businessEvent := buildEvent(ec, "business.refs.resolved", operation, map[string]any{
		"claim_id": claimID, "resolver_status": "resolved", "business_refs": businessRefs(refs),
	})
	businessEvent["claim_id"] = claimID
	businessEvent["causation_event_id"] = evidenceEvent["event_id"]
	return append(events, evidenceEvent, businessEvent)
}

func EmitDataQueryEvents(ctx context.Context, reqCtx RequestContext, subject DataQuerySubject, refs []EvidenceRef) {
	if !EvidenceEnabled() {
		return
	}
	SubmitEvents(ctx, reqCtx, BuildDataQueryEvents(ctx, reqCtx, subject, refs))
}

func SubmitEvents(ctx context.Context, reqCtx RequestContext, events []Event) {
	if len(events) == 0 {
		return
	}
	ec, ok := contextFromRequest(ctx, reqCtx)
	if !ok {
		return
	}
	ingestURL := evidenceIngestURL()
	if ingestURL == "" {
		return
	}

	payload := batch{
		ContractVersion: ContractVersion,
		Trace: map[string]any{
			"trace_id":         ec.traceID,
			"traceparent":      ec.traceparent,
			"bkn.request.id":   ec.requestID,
			"business_domain":  ec.businessDomain,
			"bkn.account.id":   ec.accountID,
			"bkn.account.type": ec.accountType,
		},
		Events: events,
	}

	select {
	case evidenceInFlight <- struct{}{}:
	default:
		return
	}

	timeout := evidenceTimeout()
	go func() {
		defer func() { <-evidenceInFlight }()
		_ = postBatch(ingestURL, timeout, payload)
	}()
}

func ObjectRowRefs(knID, branch, objectTypeID string, rows []map[string]any) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(rows))
	for i, row := range rows {
		rowHash := HashValue(row)
		refs = append(refs, EvidenceRef{
			RefID:          fmt.Sprintf("object_instance:%s:%s", strings.TrimSpace(objectTypeID), shortHash(rowHash)),
			RefType:        RefTypeRow,
			PartialReasons: []string{"row_ref_unversioned"},
			Summary: map[string]any{
				"kind":            EntityKindObjectInstance,
				"kn_id":           strings.TrimSpace(knID),
				"branch":          strings.TrimSpace(branch),
				"object_type_id":  strings.TrimSpace(objectTypeID),
				"row_index":       i,
				"row_hash":        rowHash,
				"has_instance_id": row[interfaces.SYSTEM_PROPERTY_INSTANCE_ID] != nil,
			},
		})
	}
	return refs
}

func SubgraphRefs(knID, branch string, graph *interfaces.ObjectSubGraph) []EvidenceRef {
	if graph == nil {
		return nil
	}
	refs := make([]EvidenceRef, 0, len(graph.Objects)+len(graph.RelationPaths))
	for key, object := range graph.Objects {
		objectHash := HashValue(object)
		objectTypeID := strings.TrimSpace(object.ObjectTypeId)
		refs = append(refs, EvidenceRef{
			RefID:          fmt.Sprintf("object_instance:%s:%s", objectTypeID, shortHash(objectHash)),
			RefType:        RefTypeRow,
			PartialReasons: []string{"row_ref_unversioned"},
			Summary: map[string]any{
				"kind":            EntityKindObjectInstance,
				"kn_id":           strings.TrimSpace(knID),
				"branch":          strings.TrimSpace(branch),
				"object_type_id":  objectTypeID,
				"object_key_hash": HashValue(key),
				"row_hash":        objectHash,
				"has_instance_id": object.InstanceID != nil,
			},
		})
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
				RefID:          "relation_type:" + relationTypeID,
				RefType:        RefTypeSchema,
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
	refs := []EvidenceRef{{
		RefID:          "metric:" + strings.TrimSpace(metricID),
		RefType:        RefTypeMetric,
		PartialReasons: []string{"metric_ref_unversioned"},
		Summary: map[string]any{
			"kind":      EntityKindMetric,
			"kn_id":     strings.TrimSpace(knID),
			"branch":    strings.TrimSpace(branch),
			"metric_id": strings.TrimSpace(metricID),
		},
	}}
	for i, row := range rows {
		rowHash := HashValue(row)
		refs = append(refs, EvidenceRef{
			RefID:          fmt.Sprintf("metric_data:%s:%s", strings.TrimSpace(metricID), shortHash(rowHash)),
			RefType:        RefTypeRow,
			PartialReasons: []string{"row_ref_unversioned"},
			Summary: map[string]any{
				"kind":         "metric_data",
				"kn_id":        strings.TrimSpace(knID),
				"branch":       strings.TrimSpace(branch),
				"metric_id":    strings.TrimSpace(metricID),
				"series_index": i,
				"point_count":  len(row.Values),
				"time_count":   len(row.Times),
				"row_hash":     rowHash,
			},
		})
	}
	return refs
}

func normalizedRefs(refs []EvidenceRef) ([]map[string]any, string) {
	evidenceRefs := make([]map[string]any, 0, len(refs))
	refFingerprints := make([]string, 0, len(refs))
	for _, ref := range refs {
		refID := strings.TrimSpace(ref.RefID)
		refType := strings.TrimSpace(ref.RefType)
		if refID == "" || refType == "" {
			continue
		}
		summaryHash := HashValue(ref.Summary)
		evidenceRefs = append(evidenceRefs, map[string]any{
			"ref_id":         refID,
			"ref_type":       refType,
			"source_system":  ModuleName,
			"summary_hash":   summaryHash,
			"validity":       "observed",
			"version_status": "unversioned",
			"visibility":     "visible",
		})
		refFingerprints = append(refFingerprints, refType+":"+refID+":"+summaryHash)
	}
	sort.Strings(refFingerprints)
	return evidenceRefs, HashValue(refFingerprints)
}

func businessRefs(refs []EvidenceRef) []map[string]any {
	result := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		refID := strings.TrimSpace(ref.RefID)
		if refID == "" {
			continue
		}
		refType := "object"
		switch strings.TrimSpace(ref.RefType) {
		case RefTypeSchema:
			refType = "relation"
		case RefTypeMetric:
			refType = "metric"
		}
		result = append(result, map[string]any{
			"ref_id": refID, "ref_type": refType, "source_system": "bkn",
			"validity": "available", "version_status": "unversioned", "visibility": "visible",
		})
	}
	return result
}

func postBatch(ingestURL string, timeout time.Duration, payload batch) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	postCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, ingestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := evidenceHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func evidenceIngestURL() string {
	return strings.TrimSpace(os.Getenv(envEvidenceIngestURL))
}

func evidenceTimeout() time.Duration {
	value := strings.TrimSpace(os.Getenv(envEvidenceIngestTimeoutMS))
	if value == "" {
		return 2 * time.Second
	}
	var ms int
	if _, err := fmt.Sscanf(value, "%d", &ms); err != nil || ms <= 0 {
		return 2 * time.Second
	}
	return time.Duration(ms) * time.Millisecond
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
	flags := "00"
	if spanContext.TraceFlags().IsSampled() {
		flags = "01"
	}
	businessDomain := strings.TrimSpace(reqCtx.BusinessDomain)
	if businessDomain == "" {
		businessDomain = accountID
	}
	return eventContext{
		traceID:          spanContext.TraceID().String(),
		spanID:           spanContext.SpanID().String(),
		traceparent:      fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags),
		requestID:        requestID,
		accountID:        accountID,
		accountType:      accountType,
		businessDomain:   businessDomain,
		interactionID:    nonEmptyID(reqCtx.InteractionID, "int_"),
		operationID:      nonEmptyID(reqCtx.OperationID, "op_"),
		causationEventID: strings.TrimSpace(reqCtx.CausationEventID),
		attempt:          normalizedAttempt(reqCtx.Attempt),
	}, true
}

func buildEvent(ec eventContext, eventType, operationName string, payload map[string]any) Event {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	event := Event{
		"event_id":                 "evt_" + randomHex(16),
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

func nonEmptyID(value, prefix string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return prefix + randomHex(16)
}

func normalizedAttempt(attempt int) int {
	if attempt > 0 {
		return attempt
	}
	return 1
}

func randomHex(length int) string {
	if length <= 0 {
		return ""
	}
	buf := make([]byte, (length+1)/2)
	if _, err := rand.Read(buf); err != nil {
		sum := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
		return hex.EncodeToString(sum[:])[:length]
	}
	return hex.EncodeToString(buf)[:length]
}

func shortHash(hash string) string {
	hash = strings.TrimPrefix(strings.TrimSpace(hash), "sha256:")
	if len(hash) < 16 {
		return hash
	}
	return hash[:16]
}
