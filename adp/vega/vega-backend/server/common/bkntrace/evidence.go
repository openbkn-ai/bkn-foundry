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
	"vega-backend/interfaces"
)

const (
	ContractVersion = "2.1.0"
	ModuleName      = "vega-data"
)

const (
	envEvidenceIngestURL       = "BKN_TRACE_EVIDENCE_INGEST_URL"
	envEvidenceIngestTimeoutMS = "BKN_TRACE_EVIDENCE_TIMEOUT_MS"
)

const maxInFlightEvidenceBatches = 64

const (
	RefTypeResource = "resource_ref"
	RefTypeRow      = "row_ref"
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
	Operation     string
	ResourceID    string
	CatalogID     string
	QueryHash     string
	ReturnedCount int
	TotalCount    int64
	Truncated     bool
}

type EvidenceRef struct {
	RefID   string
	RefType string
	Summary map[string]any
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
	claimID          string
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

func BuildDataQueryEvents(ctx context.Context, reqCtx RequestContext, subject DataQuerySubject, refs []EvidenceRef) []Event {
	ec, ok := contextFromRequest(ctx, reqCtx)
	if !ok {
		return nil
	}
	operation := strings.TrimSpace(subject.Operation)
	if operation == "" {
		operation = "data.resource.query"
	}

	evidenceRefs, _ := normalizedRefs(refs)
	queryHash := strings.TrimSpace(subject.QueryHash)
	if queryHash == "" {
		queryHash = HashValue(nil)
	}
	fact := buildEvent(ec, "data.query.observed", operation, map[string]any{
		"query_hash":     queryHash,
		"query_type":     operation,
		"row_count":      subject.ReturnedCount,
		"truncated":      subject.Truncated,
		"version_status": "unversioned",
		"resource_refs":  evidenceRefs,
	}, ec.claimID, ec.causationEventID)
	events := []Event{fact}
	if ec.claimID == "" {
		return events
	}
	causationID := fact["event_id"].(string)
	if len(evidenceRefs) > 0 {
		evidence := buildEvent(ec, "evidence.refs.created", operation, map[string]any{
			"claim_id":      ec.claimID,
			"evidence_refs": evidenceRefs,
		}, ec.claimID, causationID)
		events = append(events, evidence)
		causationID = evidence["event_id"].(string)
	}
	businessRefs := resolvedBusinessRefs(evidenceRefs)
	resolverStatus := "resolved"
	if len(businessRefs) == 0 {
		resolverStatus = "unresolved"
	}
	events = append(events, buildEvent(ec, "business.refs.resolved", operation, map[string]any{
		"claim_id":        ec.claimID,
		"business_refs":   businessRefs,
		"resolver_status": resolverStatus,
	}, ec.claimID, causationID))
	return events
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

func ResourceRefs(items []*interfaces.Resource) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(items))
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.ID) == "" {
			continue
		}
		refs = append(refs, EvidenceRef{
			RefID:   "resource:" + strings.TrimSpace(item.ID),
			RefType: RefTypeResource,
			Summary: map[string]any{
				"kind":                  "resource",
				"id":                    strings.TrimSpace(item.ID),
				"catalog_id":            strings.TrimSpace(item.CatalogID),
				"category":              strings.TrimSpace(item.Category),
				"status":                strings.TrimSpace(item.Status),
				"schema_property_count": len(item.SchemaDefinition),
				"has_row_count":         item.RowCount != nil,
				"update_time":           item.UpdateTime,
			},
		})
	}
	return refs
}

func ResourceRowRefs(resource *interfaces.Resource, rows []map[string]any) []EvidenceRef {
	if resource == nil || strings.TrimSpace(resource.ID) == "" {
		return nil
	}
	refs := make([]EvidenceRef, 0, len(rows))
	for i, row := range rows {
		rowHash := HashValue(row)
		refs = append(refs, EvidenceRef{
			RefID:   fmt.Sprintf("resource_row:%s:%s", strings.TrimSpace(resource.ID), shortHash(rowHash)),
			RefType: RefTypeRow,
			Summary: map[string]any{
				"kind":        "resource_row",
				"resource_id": strings.TrimSpace(resource.ID),
				"catalog_id":  strings.TrimSpace(resource.CatalogID),
				"category":    strings.TrimSpace(resource.Category),
				"row_index":   i,
				"row_hash":    rowHash,
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

func resolvedBusinessRefs(refs []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		if ref["ref_type"] == RefTypeRow {
			continue
		}
		result = append(result, ref)
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
	interactionID := strings.TrimSpace(reqCtx.InteractionID)
	operationID := strings.TrimSpace(reqCtx.OperationID)
	if interactionID == "" || operationID == "" {
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
	attempt := reqCtx.Attempt
	if attempt < 1 || attempt > 1000 {
		attempt = 1
	}
	return eventContext{
		traceID:          spanContext.TraceID().String(),
		spanID:           spanContext.SpanID().String(),
		traceparent:      fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags),
		requestID:        requestID,
		accountID:        accountID,
		accountType:      accountType,
		businessDomain:   businessDomain,
		interactionID:    interactionID,
		operationID:      operationID,
		causationEventID: strings.TrimSpace(reqCtx.CausationEventID),
		claimID:          strings.TrimSpace(reqCtx.ClaimID),
		attempt:          attempt,
	}, true
}

func buildEvent(ec eventContext, eventType, operationName string, payload map[string]any, claimID, causationEventID string) Event {
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
	if causationEventID != "" {
		event["causation_event_id"] = causationEventID
	}
	if claimID != "" {
		event["claim_id"] = claimID
	}
	return event
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
