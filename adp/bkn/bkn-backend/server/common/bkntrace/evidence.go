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
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"

	"bkn-backend/common/bkntrace/outbox"
	"bkn-backend/interfaces"
)

const (
	// ContractVersion is retained for the existing internal evidence builders.
	// SubmitEvents wraps those sanitized facts in the Core 3.0 immutable event.
	ContractVersion = "2.1.0"
	ModuleName      = "bkn-backend"
)

const (
	envEvidenceIngestURL       = "BKN_TRACE_EVIDENCE_INGEST_URL"
	envEvidenceIngestToken     = "BKN_TRACE_EVIDENCE_INGEST_TOKEN"
	envEvidenceIngestTimeoutMS = "BKN_TRACE_EVIDENCE_TIMEOUT_MS"
	envOutboxEnabled           = "BKN_TRACE_OUTBOX_ENABLED"
	envOutboxWorkerEnabled     = "BKN_TRACE_OUTBOX_WORKER_ENABLED"
	envOutboxCleanupOnly       = "BKN_TRACE_OUTBOX_CLEANUP_ONLY"
	envOutboxCleanupBatchSize  = "BKN_TRACE_OUTBOX_CLEANUP_BATCH_SIZE"
	envOutboxDeliveredDays     = "BKN_TRACE_OUTBOX_DELIVERED_RETENTION_DAYS"
	envOutboxAbandonedDays     = "BKN_TRACE_OUTBOX_ABANDONED_RETENTION_DAYS"
	envQueryGatewayToken       = "BKN_TRACE_QUERY_GATEWAY_TOKEN"
	envApplicationPrincipalID  = "BKN_TRACE_APPLICATION_PRINCIPAL_ID"
	envProducerStreamID        = "BKN_TRACE_PRODUCER_STREAM_ID"
)

const maxInFlightEvidenceBatches = 64

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
	InteractionID          string
	OperationID            string
	CausationEventID       string
	ClaimID                string
	Attempt                int
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
	interactionID    string
	operationID      string
	causationEventID string
	attempt          int
	observedAt       string
}

var (
	evidenceHTTPClient = &http.Client{}
	evidenceInFlight   = make(chan struct{}, maxInFlightEvidenceBatches)
	safeErrorCodeRE    = regexp.MustCompile(`^[0-9A-Za-z_.-]{1,128}$`)
	safeErrorPathRE    = regexp.MustCompile(`^\$(?:\.[0-9A-Za-z_.-]+|\[[0-9]+\])+$`)
	producerOutboxMu   sync.RWMutex
	producerOutbox     *outbox.Repository
)

func EvidenceEnabled() bool {
	producerOutboxMu.RLock()
	defer producerOutboxMu.RUnlock()
	return producerOutbox != nil
}

func ProducerOutboxEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(envOutboxEnabled)), "true")
}

func ProducerOutboxCleanupOnly() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(envOutboxCleanupOnly)), "true")
}

func CleanupProducerOutbox(ctx context.Context) (outbox.CleanupResult, error) {
	producerOutboxMu.RLock()
	repository := producerOutbox
	producerOutboxMu.RUnlock()
	if repository == nil {
		return outbox.CleanupResult{}, errors.New("BKN Trace producer outbox is not configured")
	}
	now := time.Now().UTC()
	return repository.Cleanup(ctx,
		now.AddDate(0, 0, -positiveEnvInt(envOutboxDeliveredDays, 30)),
		now.AddDate(0, 0, -positiveEnvInt(envOutboxAbandonedDays, 180)),
		now.AddDate(0, 0, -positiveEnvInt(envOutboxAbandonedDays, 180)),
		positiveEnvInt(envOutboxCleanupBatchSize, 1000),
	)
}

func positiveEnvInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

// ProducerOutbox returns the process-local repository used by the durable
// producer. It is nil when the feature is disabled or initialization failed.
func ProducerOutbox() *outbox.Repository {
	producerOutboxMu.RLock()
	defer producerOutboxMu.RUnlock()
	return producerOutbox
}

// ConfigureProducerOutbox enables durable 3.0 evidence delivery for this
// process. The service starts only the Worker after the database migration has
// been applied; an unset BKN_TRACE_OUTBOX_ENABLED preserves the old opt-out.
func ConfigureProducerOutbox(db *sql.DB) (*outbox.Worker, error) {
	if !ProducerOutboxEnabled() {
		WarnIfLegacyEvidenceMisconfigured()
		return nil, nil
	}
	if ProducerOutboxCleanupOnly() {
		repository, err := outbox.NewCleanupRepository(db, strings.TrimSpace(os.Getenv("DB_TYPE")))
		if err != nil {
			return nil, err
		}
		producerOutboxMu.Lock()
		producerOutbox = repository
		producerOutboxMu.Unlock()
		return nil, nil
	}
	workerEnabled := strings.EqualFold(strings.TrimSpace(os.Getenv(envOutboxWorkerEnabled)), "true")
	repository, err := outbox.NewRepository(db, outbox.Config{
		ProducerID:         ModuleName,
		ProducerStreamID:   producerStreamID(),
		DatabaseType:       strings.TrimSpace(os.Getenv("DB_TYPE")),
		IngestURL:          evidenceIngestURL(),
		IngestToken:        strings.TrimSpace(os.Getenv(envEvidenceIngestToken)),
		QueryGatewayToken:  strings.TrimSpace(os.Getenv(envQueryGatewayToken)),
		CoreRequestTimeout: evidenceTimeout(),
		LeaseDuration:      30 * time.Second,
		PollInterval:       250 * time.Millisecond,
		BumpEpochOnStart:   workerEnabled,
	})
	if err != nil {
		return nil, err
	}
	producerOutboxMu.Lock()
	producerOutbox = repository
	producerOutboxMu.Unlock()
	if !workerEnabled {
		return nil, nil
	}
	return outbox.NewWorker(repository), nil
}

func producerStreamID() string {
	if streamID := strings.TrimSpace(os.Getenv(envProducerStreamID)); streamID != "" {
		return streamID
	}
	return ModuleName
}

func WarnIfLegacyEvidenceMisconfigured() {
	if ProducerOutboxEnabled() || strings.TrimSpace(os.Getenv(envEvidenceIngestURL)) == "" {
		return
	}
	log.Printf(
		"WARN: %s is set but %s is not true; BKN Trace evidence production is disabled until producer outbox is enabled and migrated",
		envEvidenceIngestURL, envOutboxEnabled,
	)
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
	owner := trustedOwner(reqCtx, ec)
	producerOutboxMu.RLock()
	repository := producerOutbox
	producerOutboxMu.RUnlock()
	if repository == nil {
		return
	}
	for _, event := range events {
		coreEvent, err := toCoreEvent(event, ec)
		if err != nil {
			log.Printf("BKN Trace evidence outbox rejected event: %v", err)
			continue
		}
		if _, err := repository.Enqueue(ctx, coreEvent, owner); err != nil {
			// Query evidence is intentionally fail-open. The business response must
			// not wait for observability persistence, but the loss is observable.
			log.Printf("BKN Trace evidence outbox write failed: %v", err)
		}
	}
}

func trustedOwner(reqCtx RequestContext, ec eventContext) outbox.Owner {
	subjectID := strings.TrimSpace(reqCtx.EffectiveSubjectID)
	if subjectID == "" {
		subjectID = ec.accountID
	}
	subjectType := strings.TrimSpace(reqCtx.EffectiveSubjectType)
	if subjectType == "" {
		subjectType = coreSubjectType(ec.accountType)
	}
	return outbox.Owner{
		ApplicationPrincipalID: strings.TrimSpace(reqCtx.ApplicationPrincipalID),
		EffectiveSubjectType:   subjectType,
		EffectiveSubjectID:     subjectID,
		DelegationID:           strings.TrimSpace(reqCtx.DelegationID),
	}
}

func coreSubjectType(accountType string) string {
	if strings.EqualFold(strings.TrimSpace(accountType), "service") || strings.EqualFold(strings.TrimSpace(accountType), "app") {
		return "service"
	}
	return "user"
}

func toCoreEvent(event Event, ec eventContext) (outbox.Event, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return outbox.Event{}, err
	}
	observedAt, err := time.Parse(time.RFC3339Nano, ec.observedAt)
	if err != nil {
		return outbox.Event{}, err
	}
	eventID, _ := event["event_id"].(string)
	eventType, _ := event["event_type"].(string)
	if eventID == "" || eventType == "" {
		return outbox.Event{}, errors.New("evidence event ID and type are required")
	}
	conversationID := "conv_" + ec.requestID
	return outbox.Event{
		EventID: eventID, EventType: eventType, ConversationID: conversationID,
		InteractionID: ec.interactionID, OperationID: ec.operationID,
		Attempt: uint32(ec.attempt), RequestID: ec.requestID, TraceID: ec.traceID, SpanID: ec.spanID,
		CausationIDs: nonEmptyStrings(ec.causationEventID),
		StartedAt:    observedAt, ObservedAt: observedAt, EmittedAt: observedAt, Envelope: raw,
	}, nil
}

func nonEmptyStrings(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []string{strings.TrimSpace(value)}
}

func postBatchWithRetry(ingestURL string, timeout time.Duration, payload batch) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = postBatch(ingestURL, timeout, payload); err == nil {
			return nil
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
		}
	}
	return err
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
		refs = append(refs, EvidenceRef{
			RefID:   "object:" + knID + ":" + objectTypeID,
			RefType: RefTypeObject,
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
	if token := strings.TrimSpace(os.Getenv(envEvidenceIngestToken)); token != "" {
		req.Header.Set("X-BKN-Trace-Ingest-Token", token)
	}

	resp, err := evidenceHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("%s", safeIngestFailureSummary(resp.StatusCode, resp.Body))
	}
	return nil
}

func safeIngestFailureSummary(status int, body io.Reader) string {
	summary := fmt.Sprintf("HTTP %d", status)
	var response struct {
		Code    string `json:"code"`
		Details []struct {
			Path string `json:"path"`
		} `json:"details"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&response); err != nil {
		return summary
	}
	if safeErrorCodeRE.MatchString(response.Code) {
		summary += " code=" + response.Code
	}
	paths := make([]string, 0, 3)
	for _, detail := range response.Details {
		if safeErrorPathRE.MatchString(detail.Path) {
			paths = append(paths, detail.Path)
			if len(paths) == 3 {
				break
			}
		}
	}
	if len(paths) > 0 {
		summary += " paths=" + strings.Join(paths, ",")
	}
	return summary
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
