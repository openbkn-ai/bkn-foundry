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
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"ontology-query/common/bkntrace/outbox"
	"ontology-query/interfaces"
)

const (
	ContractVersion = "2.1.0"
	ModuleName      = "bkn-ontology"
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
	envPodName                 = "POD_NAME"
)

const maxInFlightEvidenceBatches = 64

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
	BusinessDomain         string
	TenantID               string
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
	observedAt       string
}

var (
	evidenceHTTPClient = &http.Client{}
	evidenceInFlight   = make(chan struct{}, maxInFlightEvidenceBatches)
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

func ConfigureProducerOutbox(db *sql.DB) (*outbox.Worker, error) {
	if !ProducerOutboxEnabled() {
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
	streamID, err := statefulSetStreamID(strings.TrimSpace(os.Getenv(envPodName)))
	if err != nil {
		return nil, err
	}
	repository, err := outbox.NewRepository(db, outbox.Config{
		ProducerID: ModuleName, ProducerStreamID: streamID, DatabaseType: strings.TrimSpace(os.Getenv("DB_TYPE")), IngestURL: evidenceIngestURL(),
		IngestToken: strings.TrimSpace(os.Getenv(envEvidenceIngestToken)), QueryGatewayToken: strings.TrimSpace(os.Getenv(envQueryGatewayToken)),
		CoreRequestTimeout: evidenceTimeout(), LeaseDuration: 30 * time.Second, PollInterval: 250 * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	producerOutboxMu.Lock()
	producerOutbox = repository
	producerOutboxMu.Unlock()
	if !strings.EqualFold(strings.TrimSpace(os.Getenv(envOutboxWorkerEnabled)), "true") {
		return nil, nil
	}
	return outbox.NewWorker(repository), nil
}

func statefulSetStreamID(podName string) (string, error) {
	parts := strings.Split(strings.TrimSpace(podName), "-")
	if len(parts) < 2 || parts[len(parts)-1] == "" {
		return "", fmt.Errorf("%s must be a StatefulSet pod name", envPodName)
	}
	for _, value := range parts[len(parts)-1] {
		if value < '0' || value > '9' {
			return "", fmt.Errorf("%s must end with a StatefulSet ordinal", envPodName)
		}
	}
	return ModuleName + "-" + parts[len(parts)-1], nil
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
	return outbox.Owner{TenantID: strings.TrimSpace(reqCtx.TenantID), BusinessDomainID: ec.businessDomain,
		ApplicationPrincipalID: strings.TrimSpace(reqCtx.ApplicationPrincipalID), EffectiveSubjectType: subjectType,
		EffectiveSubjectID: subjectID, DelegationID: strings.TrimSpace(reqCtx.DelegationID)}
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
	return outbox.Event{EventID: eventID, EventType: eventType, ConversationID: "conv_" + ec.requestID,
		InteractionID: ec.interactionID, OperationID: ec.operationID, Attempt: uint32(ec.attempt), RequestID: ec.requestID,
		TraceID: ec.traceID, SpanID: ec.spanID, CausationIDs: nonEmptyStrings(ec.causationEventID),
		StartedAt: observedAt, ObservedAt: observedAt, EmittedAt: observedAt, Envelope: raw}, nil
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

func shortHash(hash string) string {
	hash = strings.TrimPrefix(strings.TrimSpace(hash), "sha256:")
	if len(hash) < 16 {
		return hash
	}
	return hash[:16]
}
