package bkntrace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const schemaVersion = "2.1.0"

var ErrActionRejected = errors.New("bkn trace action rejected")

type Event struct {
	EventID          string         `json:"event_id"`
	EventType        string         `json:"event_type"`
	SchemaVersion    string         `json:"bkn.trace.schema.version"`
	ObservedAt       string         `json:"observed_at"`
	EmittedAt        string         `json:"emitted_at"`
	ProducerModule   string         `json:"producer_module"`
	TraceID          string         `json:"trace_id"`
	SpanID           string         `json:"span_id"`
	RequestID        string         `json:"bkn.request.id"`
	OperationName    string         `json:"bkn.operation.name"`
	InteractionID    string         `json:"interaction_id"`
	OperationID      string         `json:"operation_id"`
	CausationEventID string         `json:"causation_event_id"`
	ClaimID          string         `json:"claim_id"`
	Attempt          int            `json:"attempt"`
	Payload          map[string]any `json:"payload"`
}

type Action struct {
	traceID, spanID, requestID, interactionID, operationID string
	causationEventID, claimID, instanceID, observedAt      string
	approvalRequestedEventID                               string
	actionType, policyRef, actorRef, toolRef               string
	accountID, accountType, businessDomain                 string
	attempt                                                int
}

func ParseAction(headers map[string]any, boxID, toolID, userID string) (Action, bool) {
	get := func(key string) string {
		for header, value := range headers {
			if strings.EqualFold(header, key) {
				if text, ok := value.(string); ok {
					return strings.TrimSpace(text)
				}
			}
		}
		return ""
	}
	attempt, err := strconv.Atoi(get("bkn-attempt"))
	if err != nil || attempt < 1 {
		attempt = 1
	}
	traceID, spanID := parseTraceparent(get("traceparent"))
	action := Action{
		traceID: traceID, spanID: spanID, requestID: get("bkn-request-id"),
		interactionID: get("bkn-interaction-id"), operationID: get("bkn-operation-id"),
		causationEventID: get("bkn-causation-event-id"), claimID: get("bkn-claim-id"),
		instanceID: get("bkn-action-instance-id"), actionType: get("bkn-action-type"),
		policyRef: get("bkn-action-policy-ref"), observedAt: get("bkn-action-observed-at"),
		approvalRequestedEventID: get("bkn-action-approval-requested-event-id"),
		actorRef:                 hashRef("actor", userID),
		toolRef:                  hashRef("tool", toolID),
		accountID:                get("x-account-id"), accountType: get("x-account-type"),
		businessDomain: get("x-business-domain"),
		attempt:        attempt,
	}
	if action.businessDomain == "" {
		action.businessDomain = action.accountID
	}
	complete := action.traceID != "" && action.spanID != "" && action.requestID != "" &&
		action.interactionID != "" && action.operationID != "" && action.causationEventID != "" &&
		action.claimID != "" && action.instanceID != "" && action.observedAt != "" &&
		action.approvalRequestedEventID != "" &&
		action.accountID != "" && action.accountType != "" && action.businessDomain != ""
	safePolicy := action.actionType == "monitor" && strings.EqualFold(get("bkn-action-reversible"), "true") &&
		action.policyRef == "e2e-monitor-auto-approve"
	return action, complete && safePolicy
}

func (a Action) AfterPermission(permissionErr error) ([]Event, error) {
	parent := a.approvalRequestedEventID
	if permissionErr != nil {
		return []Event{a.event("action.rejected", parent, map[string]any{
			"action_instance_id": a.instanceID, "actor_ref": a.actorRef,
			"policy_decision_ref": hashRef("decision", a.instanceID+":rejected:"+permissionErr.Error()),
			"status":              "rejected",
		})}, ErrActionRejected
	}
	return []Event{a.event("action.approved", parent, map[string]any{
		"action_instance_id": a.instanceID, "actor_ref": a.actorRef,
		"policy_decision_ref": hashRef("decision", a.instanceID+":approved"), "status": "approved",
	})}, nil
}

func (a Action) AfterExecution(result []byte, executionErr error) []Event {
	status := "success"
	executedPayload := map[string]any{
		"action_instance_id": a.instanceID, "invocation_ref": a.toolRef, "status": status,
	}
	if executionErr != nil {
		status = "error"
		executedPayload["status"] = status
		executedPayload["error_category"] = "tool_execution"
		executedPayload["error_hash"] = hashValue(executionErr.Error())
	}
	executed := a.event("action.executed", a.eventID("action.approved"), executedPayload)
	resultPayload := map[string]any{
		"action_instance_id": a.instanceID, "status": status,
		"result_hash": hashValue(string(result)), "task_ref": hashRef("task", a.instanceID),
	}
	return []Event{executed, a.event("action.result_recorded", executed.EventID, resultPayload)}
}

func (a Action) Complete(permissionErr error, result []byte, executionErr error) ([]Event, error) {
	decision, err := a.AfterPermission(permissionErr)
	if err != nil {
		return decision, err
	}
	return append(decision, a.AfterExecution(result, executionErr)...), nil
}

func (a Action) event(eventType, cause string, payload map[string]any) Event {
	return Event{
		EventID: a.eventID(eventType), EventType: eventType, SchemaVersion: schemaVersion,
		ObservedAt: a.observedAt, EmittedAt: a.observedAt, ProducerModule: "operator-integration",
		TraceID: a.traceID, SpanID: a.spanID, RequestID: a.requestID,
		OperationName: "action.execute", InteractionID: a.interactionID, OperationID: a.operationID,
		CausationEventID: cause, ClaimID: a.claimID, Attempt: a.attempt, Payload: payload,
	}
}

func (a Action) eventID(eventType string) string {
	return "evt_" + digest(a.instanceID + ":" + a.operationID + ":" + strconv.Itoa(a.attempt) + ":" + eventType)[:32]
}

type Emitter interface {
	Emit(ctx context.Context, action Action, events []Event)
}

type HTTPEmitter struct {
	URL    string
	Client *http.Client
}

func NewHTTPEmitter() *HTTPEmitter {
	return &HTTPEmitter{URL: os.Getenv("BKN_TRACE_EVIDENCE_INGEST_URL"), Client: &http.Client{Timeout: 3 * time.Second}}
}

func (e *HTTPEmitter) Emit(ctx context.Context, action Action, events []Event) {
	if e == nil || e.URL == "" || len(events) == 0 {
		return
	}
	body, err := json.Marshal(map[string]any{
		"bkn.trace.schema.version": schemaVersion,
		"trace": map[string]any{
			"trace_id": action.traceID, "bkn.request.id": action.requestID,
			"business_domain": action.businessDomain, "bkn.account.id": action.accountID,
			"bkn.account.type": action.accountType,
		},
		"events": events,
	})
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.URL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func MustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func parseTraceparent(value string) (string, string) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(value)), "-")
	if len(parts) != 4 || len(parts[1]) != 32 || len(parts[2]) != 16 {
		return "", ""
	}
	return parts[1], parts[2]
}

func hashValue(value string) string     { return "sha256:" + digest(value) }
func hashRef(kind, value string) string { return kind + ":" + digest(value)[:24] }
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
