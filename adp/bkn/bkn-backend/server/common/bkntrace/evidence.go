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

	"bkn-backend/interfaces"
	"go.opentelemetry.io/otel/trace"
)

const (
	ContractVersion = "2.0.0"
	ModuleName      = "bkn-backend"
)

const (
	envEvidenceIngestURL       = "BKN_TRACE_EVIDENCE_INGEST_URL"
	envEvidenceIngestTimeoutMS = "BKN_TRACE_EVIDENCE_TIMEOUT_MS"
)

const maxInFlightEvidenceBatches = 64

const (
	EntityKindObjectType   = "object_type"
	EntityKindRelationType = "relation_type"
	EntityKindActionType   = "action_type"
	EntityKindMetric       = "metric"
)

const (
	RefTypeSchema = "schema_ref"
	RefTypeAction = "action_ref"
	RefTypeMetric = "metric_ref"
)

type Event map[string]any

type RequestContext struct {
	RequestID      string
	AccountID      string
	AccountType    string
	BusinessDomain string
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

type batch struct {
	ContractVersion string         `json:"bkn.trace.schema.version"`
	Trace           map[string]any `json:"trace"`
	Events          []Event        `json:"events"`
}

type eventContext struct {
	traceID        string
	spanID         string
	traceparent    string
	requestID      string
	accountID      string
	accountType    string
	businessDomain string
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

func BuildSchemaReadEvents(ctx context.Context, reqCtx RequestContext, subject ReadSubject, refs []EvidenceRef) []Event {
	ec, ok := contextFromRequest(ctx, reqCtx)
	if !ok || len(refs) == 0 {
		return nil
	}
	operation := strings.TrimSpace(subject.Operation)
	if operation == "" {
		operation = "bkn.schema.read"
	}

	evidenceRefs := make([]map[string]any, 0, len(refs))
	refFingerprints := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.RefID) == "" || strings.TrimSpace(ref.RefType) == "" {
			continue
		}
		refID := strings.TrimSpace(ref.RefID)
		refType := strings.TrimSpace(ref.RefType)
		summaryHash := HashValue(ref.Summary)
		partialReasons := ref.PartialReasons
		if len(partialReasons) == 0 {
			partialReasons = []string{refType + "_unversioned"}
		}
		evidenceRefs = append(evidenceRefs, map[string]any{
			"ref_id":         refID,
			"ref_type":       refType,
			"source_system":  ModuleName,
			"summary":        ref.Summary,
			"summary_hash":   summaryHash,
			"validity":       "observed",
			"version_status": "unversioned",
			"visibility":     "visible",
			"partial_reason": partialReasons,
		})
		refFingerprints = append(refFingerprints, refType+":"+refID+":"+summaryHash)
	}
	if len(evidenceRefs) == 0 {
		return nil
	}
	sort.Strings(refFingerprints)
	evidenceRefsHash := HashValue(refFingerprints)

	resultSummary := map[string]any{
		"kn_id":              strings.TrimSpace(subject.KNID),
		"branch":             strings.TrimSpace(subject.Branch),
		"entity_kind":        strings.TrimSpace(subject.EntityKind),
		"requested_hash":     HashValue(subject.RequestedIDs),
		"returned_count":     subject.ReturnedCount,
		"total_count":        subject.TotalCount,
		"evidence_count":     len(evidenceRefs),
		"evidence_refs_hash": evidenceRefsHash,
		"operation":          operation,
		"producer_module":    ModuleName,
		"contract_version":   ContractVersion,
	}
	claimID := ClaimID(operation, subject.KNID, resultSummary)

	return []Event{
		buildEvent(ec, "claim.created", operation, map[string]any{
			"claim_id":       claimID,
			"claim_type":     "finding",
			"claim_hash":     HashValue(resultSummary),
			"visibility":     "visible",
			"version_status": "unversioned",
			"partial_reason": []string{"schema_refs_unversioned"},
			"subject_refs": map[string]any{
				"kn_id":               strings.TrimSpace(subject.KNID),
				"branch":              strings.TrimSpace(subject.Branch),
				"entity_kind":         strings.TrimSpace(subject.EntityKind),
				"requested_hash":      resultSummary["requested_hash"],
				"evidence_refs_hash":  evidenceRefsHash,
				"returned_ref_count":  len(evidenceRefs),
				"returned_count":      subject.ReturnedCount,
				"total_count":         subject.TotalCount,
				"data.classification": "internal",
			},
		}),
		buildEvent(ec, "evidence.refs.created", operation, map[string]any{
			"claim_id":      claimID,
			"evidence_refs": evidenceRefs,
		}),
	}
}

func EmitSchemaReadEvents(ctx context.Context, reqCtx RequestContext, subject ReadSubject, refs []EvidenceRef) {
	if !EvidenceEnabled() {
		return
	}
	SubmitEvents(ctx, reqCtx, BuildSchemaReadEvents(ctx, reqCtx, subject, refs))
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

func ObjectTypeRefs(items []*interfaces.ObjectType) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(items))
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.OTID) == "" {
			continue
		}
		refs = append(refs, EvidenceRef{
			RefID:   "object_type:" + strings.TrimSpace(item.OTID),
			RefType: RefTypeSchema,
			Summary: map[string]any{
				"kind":                 EntityKindObjectType,
				"id":                   strings.TrimSpace(item.OTID),
				"kn_id":                strings.TrimSpace(item.KNID),
				"branch":               strings.TrimSpace(item.Branch),
				"module_type":          strings.TrimSpace(item.ModuleType),
				"property_count":       len(item.DataProperties) + len(item.LogicProperties),
				"data_property_count":  len(item.DataProperties),
				"logic_property_count": len(item.LogicProperties),
				"primary_key_count":    len(item.PrimaryKeys),
				"has_status":           item.Status != nil,
				"update_time":          item.UpdateTime,
			},
		})
	}
	return refs
}

func RelationTypeRefs(items []*interfaces.RelationType) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(items))
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.RTID) == "" {
			continue
		}
		refs = append(refs, EvidenceRef{
			RefID:   "relation_type:" + strings.TrimSpace(item.RTID),
			RefType: RefTypeSchema,
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
		if item == nil || strings.TrimSpace(item.ATID) == "" {
			continue
		}
		refs = append(refs, EvidenceRef{
			RefID:          "action_type:" + strings.TrimSpace(item.ATID),
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
		if item == nil || strings.TrimSpace(item.ID) == "" {
			continue
		}
		refs = append(refs, EvidenceRef{
			RefID:          "metric:" + strings.TrimSpace(item.ID),
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
		traceID:        spanContext.TraceID().String(),
		spanID:         spanContext.SpanID().String(),
		traceparent:    fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags),
		requestID:      requestID,
		accountID:      accountID,
		accountType:    accountType,
		businessDomain: businessDomain,
	}, true
}

func buildEvent(ec eventContext, eventType, operationName string, payload map[string]any) Event {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return Event{
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
		"payload":                  payload,
	}
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
