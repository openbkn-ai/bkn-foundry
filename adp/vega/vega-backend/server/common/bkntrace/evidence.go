// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
	"go.opentelemetry.io/otel/trace"
)

const (
	ContractVersion         = "2.1.0"
	ArtifactContractVersion = "2.2.0"
	ModuleName              = "vega-data"
)

const (
	envEvidenceIngestURL       = "BKN_TRACE_EVIDENCE_INGEST_URL"
	envArtifactIngestURL       = "BKN_TRACE_ARTIFACT_INGEST_URL"
	envEvidenceIngestToken     = "BKN_TRACE_EVIDENCE_INGEST_TOKEN"
	envEvidenceIngestTimeoutMS = "BKN_TRACE_EVIDENCE_TIMEOUT_MS"
)

const maxInFlightEvidenceBatches = 64
const maxEvidenceErrorBodyBytes = 8 << 10

const (
	RefTypeResource        = "resource"
	RefTypeField           = "field"
	ArtifactTypeQuery      = "query"
	ArtifactTypeDataResult = "data_result"
)

type Event map[string]any

type Artifact struct {
	ArtifactID     string   `json:"artifact_id"`
	ArtifactType   string   `json:"artifact_type"`
	RequestID      string   `json:"bkn.request.id"`
	TraceID        string   `json:"trace_id,omitempty"`
	InteractionID  string   `json:"interaction_id,omitempty"`
	OperationID    string   `json:"operation_id,omitempty"`
	SourceRef      string   `json:"source_ref,omitempty"`
	BusinessRefs   []string `json:"business_refs,omitempty"`
	ContentType    string   `json:"content_type"`
	SchemaVersion  string   `json:"schema_version"`
	ObservedAt     string   `json:"observed_at"`
	ContentHash    string   `json:"content_hash,omitempty"`
	Content        any      `json:"content"`
	TenantID       string   `json:"bkn.tenant.id,omitempty"`
	BusinessDomain string   `json:"business_domain,omitempty"`
	AccountID      string   `json:"bkn.account.id"`
	AccountType    string   `json:"bkn.account.type"`
	AgentOrApp     string   `json:"agent_or_app,omitempty"`
}

type RequestContext struct {
	RequestID          string
	AccountID          string
	AccountType        string
	TenantID           string
	BusinessDomain     string
	InteractionID      string
	OperationID        string
	CausationEventID   string
	ClaimID            string
	Attempt            int
	ObservedAt         string
	ObservedAtProvided bool
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
	tenantID         string
	businessDomain   string
	interactionID    string
	operationID      string
	causationEventID string
	claimID          string
	attempt          int
	observedAt       string
}

type evidenceHTTPError struct {
	statusCode     int
	validationCode string
}

func (e *evidenceHTTPError) Error() string {
	if e.validationCode != "" {
		return fmt.Sprintf("HTTP %d (%s)", e.statusCode, e.validationCode)
	}
	return fmt.Sprintf("HTTP %d", e.statusCode)
}

func (e *evidenceHTTPError) retryable() bool {
	return e.statusCode == http.StatusRequestTimeout ||
		e.statusCode == http.StatusTooManyRequests ||
		e.statusCode >= http.StatusInternalServerError
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

func ArtifactContentHash(value any) string {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return HashValue(value)
	}
	raw := bytes.TrimSuffix(body.Bytes(), []byte("\n"))
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func BuildDataQueryEvidence(
	ctx context.Context,
	reqCtx RequestContext,
	subject DataQuerySubject,
	refs []EvidenceRef,
	queryContent any,
	resultContent any,
) ([]Artifact, []Event) {
	ec, ok := contextFromRequest(ctx, reqCtx)
	if !ok || queryContent == nil || resultContent == nil {
		return nil, nil
	}
	operation := strings.TrimSpace(subject.Operation)
	if operation == "" {
		operation = "data.resource.query"
	}
	queryArtifact := buildArtifact(ec, subject, refs, ArtifactTypeQuery, queryContent)
	resultArtifact := buildArtifact(ec, subject, refs, ArtifactTypeDataResult, resultContent)

	resourceRefs, fieldRefs := normalizedRefs(refs)
	queryHash := strings.TrimSpace(subject.QueryHash)
	if queryHash == "" {
		queryHash = HashValue(queryContent)
	}
	event := buildEvent(ec, "data.query.observed", operation, map[string]any{
		"query_hash":          queryHash,
		"query_type":          operation,
		"row_count":           subject.ReturnedCount,
		"truncated":           subject.Truncated,
		"version_status":      "unversioned",
		"resource_refs":       resourceRefs,
		"field_refs":          fieldRefs,
		"query_artifact_ref":  "artifact:" + queryArtifact.ArtifactID,
		"result_artifact_ref": "artifact:" + resultArtifact.ArtifactID,
	}, "", ec.causationEventID)
	event["bkn.trace.schema.version"] = ArtifactContractVersion
	return []Artifact{queryArtifact, resultArtifact}, []Event{event}
}

func buildArtifact(ec eventContext, subject DataQuerySubject, refs []EvidenceRef, artifactType string, content any) Artifact {
	businessRefs := make([]string, 0, len(refs))
	for _, ref := range refs {
		if refID := strings.TrimSpace(ref.RefID); refID != "" {
			businessRefs = append(businessRefs, refID)
		}
	}
	sourceRef := ""
	if resourceID := strings.TrimSpace(subject.ResourceID); resourceID != "" {
		sourceRef = "resource:" + resourceID
	}
	return Artifact{
		ArtifactID: "art_" + shortHash(HashValue([]string{
			ec.traceID, ec.requestID, ec.interactionID, ec.operationID, artifactType,
		})),
		ArtifactType: artifactType, RequestID: ec.requestID, TraceID: ec.traceID,
		InteractionID: ec.interactionID, OperationID: ec.operationID, SourceRef: sourceRef,
		BusinessRefs: businessRefs, ContentType: "application/json",
		SchemaVersion: ArtifactContractVersion, ObservedAt: ec.observedAt,
		Content:  content,
		TenantID: ec.tenantID, BusinessDomain: ec.businessDomain,
		AccountID: ec.accountID, AccountType: ec.accountType, AgentOrApp: ModuleName,
	}
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

	resourceRefs, fieldRefs := normalizedRefs(refs)
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
		"resource_refs":  resourceRefs,
		"field_refs":     fieldRefs,
	}, "", ec.causationEventID)
	return []Event{fact}
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

func EmitDataQueryEvidence(
	ctx context.Context,
	reqCtx RequestContext,
	subject DataQuerySubject,
	refs []EvidenceRef,
	queryContent any,
	resultContent any,
) string {
	if !EvidenceEnabled() || artifactIngestURL() == "" {
		return ""
	}
	artifacts, events := BuildDataQueryEvidence(ctx, reqCtx, subject, refs, queryContent, resultContent)
	if len(artifacts) != 2 || len(events) != 1 {
		return ""
	}
	ec, ok := contextFromRequest(ctx, reqCtx)
	if !ok {
		return ""
	}
	payload := batch{
		ContractVersion: ArtifactContractVersion,
		Trace: map[string]any{
			"trace_id": ec.traceID, "traceparent": ec.traceparent, "bkn.request.id": ec.requestID,
			"bkn.tenant.id": ec.tenantID, "business_domain": ec.businessDomain,
			"bkn.account.id": ec.accountID, "bkn.account.type": ec.accountType,
		},
		Events: events,
	}
	select {
	case evidenceInFlight <- struct{}{}:
	default:
		log.Printf("BKN Trace evidence ingestion dropped: in-flight limit reached")
		return ""
	}
	go func() {
		defer func() { <-evidenceInFlight }()
		for _, artifact := range artifacts {
			if err := postArtifactWithRetry(artifactIngestURL(), evidenceTimeout(), artifact); err != nil {
				log.Printf("BKN Trace artifact ingestion unavailable: %v", err)
				return
			}
		}
		if err := postBatchWithRetry(evidenceIngestURL(), evidenceTimeout(), payload); err != nil {
			log.Printf("BKN Trace evidence ingestion unavailable: %v", err)
		}
	}()
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
			"bkn.tenant.id":    ec.tenantID,
			"business_domain":  ec.businessDomain,
			"bkn.account.id":   ec.accountID,
			"bkn.account.type": ec.accountType,
		},
		Events: events,
	}

	select {
	case evidenceInFlight <- struct{}{}:
	default:
		log.Printf("BKN Trace evidence ingestion dropped: in-flight limit reached")
		return
	}

	timeout := evidenceTimeout()
	go func() {
		defer func() { <-evidenceInFlight }()
		if err := postBatchWithRetry(ingestURL, timeout, payload); err != nil {
			log.Printf("BKN Trace evidence ingestion unavailable: %v", err)
		}
	}()
}

func postBatchWithRetry(ingestURL string, timeout time.Duration, payload batch) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = postBatch(ingestURL, timeout, payload); err == nil {
			return nil
		}
		var httpErr *evidenceHTTPError
		if errors.As(err, &httpErr) && !httpErr.retryable() {
			return err
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
		}
	}
	return err
}

func postArtifactWithRetry(ingestURL string, timeout time.Duration, artifact Artifact) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = postArtifact(ingestURL, timeout, artifact); err == nil {
			return nil
		}
		var httpErr *evidenceHTTPError
		if errors.As(err, &httpErr) && !httpErr.retryable() {
			return err
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
		}
	}
	return err
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
		for _, property := range item.SchemaDefinition {
			if property == nil || strings.TrimSpace(property.Name) == "" {
				continue
			}
			refs = append(refs, EvidenceRef{
				RefID: "field:" + strings.TrimSpace(item.ID) + ":" + shortHash(HashValue(property.Name)), RefType: RefTypeField,
			})
		}
	}
	return refs
}

func ResourceRowRefs(resource *interfaces.Resource, rows []map[string]any) []EvidenceRef {
	if resource == nil || strings.TrimSpace(resource.ID) == "" {
		return nil
	}
	return []EvidenceRef{{RefID: "resource:" + strings.TrimSpace(resource.ID), RefType: RefTypeResource}}
}

func normalizedRefs(refs []EvidenceRef) ([]map[string]any, []map[string]any) {
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
			"source_system":  ModuleName,
			"validity":       "observed",
			"version_status": "unversioned",
			"visibility":     "visible",
		}
		switch refType {
		case RefTypeResource:
			resourceRefs = append(resourceRefs, controlled)
		case RefTypeField:
			fieldRefs = append(fieldRefs, controlled)
		}
	}
	return resourceRefs, fieldRefs
}

func postBatch(ingestURL string, timeout time.Duration, payload batch) error {
	return postJSON(ingestURL, timeout, payload)
}

func postArtifact(ingestURL string, timeout time.Duration, artifact Artifact) error {
	return postJSON(ingestURL, timeout, artifact)
}

func postJSON(ingestURL string, timeout time.Duration, payload any) error {
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
	if token := strings.TrimSpace(os.Getenv(envEvidenceIngestToken)); token != "" {
		req.Header.Set("X-BKN-Trace-Ingest-Token", token)
	}

	resp, err := evidenceHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		response := struct {
			Code string `json:"code"`
		}{}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxEvidenceErrorBodyBytes))
		if readErr == nil {
			_ = json.Unmarshal(body, &response)
		}
		return &evidenceHTTPError{
			statusCode:     resp.StatusCode,
			validationCode: strings.TrimSpace(response.Code),
		}
	}
	return nil
}

func evidenceIngestURL() string {
	return strings.TrimSpace(os.Getenv(envEvidenceIngestURL))
}

func artifactIngestURL() string {
	return strings.TrimSpace(os.Getenv(envArtifactIngestURL))
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
	if !reqCtx.ObservedAtProvided {
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
		tenantID:         strings.TrimSpace(reqCtx.TenantID),
		businessDomain:   businessDomain,
		interactionID:    interactionID,
		operationID:      operationID,
		causationEventID: strings.TrimSpace(reqCtx.CausationEventID),
		claimID:          strings.TrimSpace(reqCtx.ClaimID),
		attempt:          attempt,
		observedAt:       observedAt,
	}, true
}

func buildEvent(ec eventContext, eventType, operationName string, payload map[string]any, claimID, causationEventID string) Event {
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
	if causationEventID != "" {
		event["causation_event_id"] = causationEventID
	}
	if claimID != "" {
		event["claim_id"] = claimID
	}
	return event
}

func stableEventID(traceID, operationID, eventType string, attempt int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d", traceID, operationID, eventType, attempt)))
	return "evt_" + hex.EncodeToString(sum[:])
}

func shortHash(hash string) string {
	hash = strings.TrimPrefix(strings.TrimSpace(hash), "sha256:")
	if len(hash) < 16 {
		return hash
	}
	return hash[:16]
}
