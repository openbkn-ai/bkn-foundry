package evidencesvc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

type fakeStore struct {
	calls  int
	traces []evidencevo.NormalizedTrace
}

func (s *fakeStore) StoreEvidence(_ context.Context, _ evidencevo.NormalizedTrace) error {
	s.calls++
	return nil
}

type capturingStore struct {
	fakeStore
}

type fakeBusinessResolver struct {
	requests    []ibusinessresolver.ResolveRequest
	resolutions []ibusinessresolver.Resolution
	err         error
}

func (r *fakeBusinessResolver) ResolveBusinessRefs(_ context.Context, request ibusinessresolver.ResolveRequest) ([]ibusinessresolver.Resolution, error) {
	r.requests = append(r.requests, request)
	return r.resolutions, r.err
}

func (s *capturingStore) StoreEvidence(_ context.Context, trace evidencevo.NormalizedTrace) error {
	s.calls++
	s.traces = append(s.traces, trace)
	return nil
}

func (s *fakeStore) GetEvidenceByTraceID(_ context.Context, traceID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	var result []evidencevo.NormalizedTrace
	for _, trace := range s.traces {
		if trace.TraceID == traceID {
			result = append(result, trace)
		}
	}
	return fakeLimitedResult(result, options.Limit), nil
}

func (s *fakeStore) GetEvidenceHistoryByTraceID(_ context.Context, traceID string) ([]evidencevo.NormalizedTrace, error) {
	var result []evidencevo.NormalizedTrace
	for _, trace := range s.traces {
		if trace.TraceID == traceID {
			result = append(result, trace)
		}
	}
	return result, nil
}

func (s *fakeStore) GetEvidenceByRequestID(_ context.Context, requestID string, options evidencevo.EvidenceQueryOptions) (evidencevo.EvidenceQueryResult, error) {
	var result []evidencevo.NormalizedTrace
	for _, trace := range s.traces {
		if trace.RequestID == requestID {
			result = append(result, trace)
		}
	}
	return fakeLimitedResult(result, options.Limit), nil
}

func fakeLimitedResult(traces []evidencevo.NormalizedTrace, limit int) evidencevo.EvidenceQueryResult {
	if limit <= 0 || len(traces) <= limit {
		return evidencevo.EvidenceQueryResult{Traces: traces}
	}
	return evidencevo.EvidenceQueryResult{
		Traces:    traces[:limit],
		Truncated: true,
	}
}

func TestIngestAcceptsPhaseTwoEvidenceBatch(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	response, validationErrors, err := service.Ingest(context.Background(), []byte(validBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) > 0 {
		t.Fatalf("unexpected validation errors: %+v", validationErrors)
	}
	if store.calls != 1 {
		t.Fatalf("expected evidence to be stored once, got %d", store.calls)
	}
	if response.TraceID != "8c0d0000000000000000000000000001" {
		t.Fatalf("unexpected trace id: %s", response.TraceID)
	}
	if response.AcceptedEvents != 3 || response.ClaimCount != 1 || response.EvidenceRefCount != 1 || response.BusinessRefCount != 1 {
		t.Fatalf("unexpected response counts: %+v", response)
	}
}

func TestIngestAcceptsIntegratedProducerTwoPointOneFixtures(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..", "..", ".."))
	fixtures := []string{
		filepath.Join(repositoryRoot, "adp", "context-loader", "agent-retrieval", "fixtures", "bkn-trace", "phase2", "retrieval_completed_2_1_positive.json"),
		filepath.Join(repositoryRoot, "adp", "vega", "vega-backend", "fixtures", "bkn-trace", "phase2", "data_query_observed_2_1_positive.json"),
	}
	for _, fixture := range fixtures {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			body, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatalf("read integrated producer fixture: %v", err)
			}
			var batch map[string]any
			if err := json.Unmarshal(body, &batch); err != nil {
				t.Fatalf("decode integrated producer fixture: %v", err)
			}
			trace := batch["trace"].(map[string]any)
			trace["bkn.tenant.id"] = "tenant_fixture"
			trace["business_domain"] = "domain_fixture"
			trace["bkn.account.id"] = "account_fixture"
			trace["bkn.account.type"] = "app"

			_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, batch))
			if err != nil || len(validationErrors) > 0 {
				t.Fatalf("integrated producer fixture must satisfy core contract: errors=%+v err=%v", validationErrors, err)
			}
		})
	}
}

func TestContractVersionDefaultsToTwoPointOne(t *testing.T) {
	if evidencevo.ContractVersion != "2.1.0" {
		t.Fatalf("expected default contract version 2.1.0, got %s", evidencevo.ContractVersion)
	}
}

func TestIngestAcceptsTwoPointOneBusinessEventsAndPreservesCausality(t *testing.T) {
	store := &capturingStore{}
	service := New(store)

	response, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(validTwoPointOneEvents())))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) > 0 {
		t.Fatalf("unexpected validation errors: %+v", validationErrors)
	}
	if response.SchemaVersion != "2.1.0" || response.AcceptedEvents != len(validTwoPointOneEvents()) {
		t.Fatalf("unexpected response: %+v", response)
	}
	if len(store.traces) != 1 {
		t.Fatalf("expected one stored trace, got %d", len(store.traces))
	}
	conversation := reflect.ValueOf(store.traces[0]).FieldByName("ConversationID")
	if !conversation.IsValid() || conversation.String() != "agent:thread_supply_chain" {
		t.Fatalf("normalized trace dropped bkn.conversation.id: %+v", store.traces[0])
	}
	event := store.traces[0].Events[1]
	encoded, marshalErr := json.Marshal(event)
	if marshalErr != nil {
		t.Fatalf("marshal stored event: %v", marshalErr)
	}
	stored := string(encoded)
	for _, expected := range []string{
		`"interaction_id":"int_001"`,
		`"operation_id":"op_data_001"`,
		`"causation_event_id":"evt_interaction"`,
		`"attempt":1`,
	} {
		if !strings.Contains(stored, expected) {
			t.Fatalf("stored event missing %s: %s", expected, stored)
		}
	}
}

func TestIngestRejectsInvalidConversationAndInteractionIDs(t *testing.T) {
	service := New(evidencestore.New())
	tests := []struct {
		field  string
		mutate func(map[string]any)
	}{
		{
			field: "bkn.conversation.id",
			mutate: func(batch map[string]any) {
				batch["trace"].(map[string]any)["bkn.conversation.id"] = "invalid conversation"
			},
		},
		{
			field: "interaction_id",
			mutate: func(batch map[string]any) {
				batch["events"].([]map[string]any)[0]["interaction_id"] = "invalid interaction"
			},
		},
	}
	for _, testCase := range tests {
		batch := twoPointOneBatch(validTwoPointOneEvents())
		testCase.mutate(batch)
		_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, batch))
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		if !hasValidationPath(validationErrors, testCase.field) {
			t.Fatalf("%s must fail validation: %+v", testCase.field, validationErrors)
		}
	}
}

func TestIngestDefaultsTwoPointOneAttemptToOne(t *testing.T) {
	store := &capturingStore{}
	service := New(store)
	events := validTwoPointOneEvents()
	delete(events[1], "attempt")

	_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) > 0 {
		t.Fatalf("unexpected validation errors: %+v", validationErrors)
	}
	encoded, marshalErr := json.Marshal(store.traces[0].Events[1])
	if marshalErr != nil {
		t.Fatalf("marshal stored event: %v", marshalErr)
	}
	if !strings.Contains(string(encoded), `"attempt":1`) {
		t.Fatalf("expected default attempt 1, got %s", encoded)
	}
}

func TestIngestRejectsUnknownTwoPointOneMinorVersion(t *testing.T) {
	service := New(&fakeStore{})
	body := mustJSON(t, twoPointOneBatch(validTwoPointOneEvents()))
	body = []byte(strings.ReplaceAll(string(body), "2.1.0", "2.1.1"))

	_, validationErrors, err := service.Ingest(context.Background(), body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_SCHEMA_VERSION_UNSUPPORTED") {
		t.Fatalf("expected unsupported schema version, got %+v", validationErrors)
	}
}

func TestIngestRejectsMissingTwoPointOnePublicFields(t *testing.T) {
	service := New(&fakeStore{})
	events := validTwoPointOneEvents()
	delete(events[1], "interaction_id")
	delete(events[1], "operation_id")
	for _, event := range events {
		if event["event_type"] == "evidence.refs.created" {
			delete(event, "causation_event_id")
		}
	}

	_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, field := range []string{"interaction_id", "operation_id", "causation_event_id"} {
		if !hasValidationPath(validationErrors, field) {
			t.Fatalf("expected missing %s validation, got %+v", field, validationErrors)
		}
	}
}

func TestIngestValidatesRegisteredTwoPointOneEventPayloads(t *testing.T) {
	tests := []struct {
		eventType string
		field     string
	}{
		{eventType: "agent.interaction.started", field: "intent_hash"},
		{eventType: "retrieval.completed", field: "candidate_count"},
		{eventType: "knowledge.read.observed", field: "kn_id"},
		{eventType: "data.query.observed", field: "query_hash"},
		{eventType: "model.call.observed", field: "model_name"},
		{eventType: "action.recommended", field: "reason_hash"},
		{eventType: "action.approval_requested", field: "policy_ref"},
		{eventType: "action.approved", field: "actor_ref"},
		{eventType: "action.rejected", field: "policy_decision_ref"},
		{eventType: "action.executed", field: "status"},
		{eventType: "action.result_recorded", field: "result_hash"},
	}
	for _, tt := range tests {
		t.Run(tt.eventType+" requires "+tt.field, func(t *testing.T) {
			events := validTwoPointOneEvents()
			if tt.eventType == "action.rejected" {
				rejected := cloneEventByType(t, events, "action.approved")
				rejected["event_id"] = "evt_action_rejected"
				rejected["event_type"] = "action.rejected"
				rejected["payload"].(map[string]any)["status"] = "rejected"
				events = append(removeEventTypes(events, "action.approved", "action.executed", "action.result_recorded"), rejected)
			}
			for _, event := range events {
				if event["event_type"] == tt.eventType {
					delete(event["payload"].(map[string]any), tt.field)
				}
			}
			service := New(&fakeStore{})
			_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !hasValidationPath(validationErrors, tt.field) {
				t.Fatalf("expected required field %s, got %+v", tt.field, validationErrors)
			}
		})
	}
}

func TestIngestAcceptsDirectRootKnowledgeReadWithoutFabricatedCausation(t *testing.T) {
	knowledge := cloneEventByType(t, validTwoPointOneEvents(), "knowledge.read.observed")
	delete(knowledge, "causation_event_id")

	_, validationErrors, err := New(evidencestore.New()).Ingest(
		context.Background(),
		mustJSON(t, twoPointOneBatch([]map[string]any{knowledge})),
	)
	if err != nil {
		t.Fatalf("unexpected infrastructure error: %v", err)
	}
	if len(validationErrors) > 0 {
		t.Fatalf("direct root fact must not require fabricated causation: %+v", validationErrors)
	}
}

func TestDirectKnowledgeReadExposesBusinessRefsWithoutFabricatingClaim(t *testing.T) {
	knowledge := cloneEventByType(t, validTwoPointOneEvents(), "knowledge.read.observed")
	delete(knowledge, "causation_event_id")
	knowledge["payload"].(map[string]any)["business_refs"] = []any{map[string]any{
		"ref_id": "object:supplychain:forecast", "ref_type": "object", "source_system": "bkn",
		"validity": "available", "version_status": "unversioned", "visibility": "visible",
	}}
	store := evidencestore.New()
	service := New(store)

	ingested, validationErrors, err := service.Ingest(
		context.Background(),
		mustJSON(t, twoPointOneBatch([]map[string]any{knowledge})),
	)
	if err != nil || len(validationErrors) > 0 {
		t.Fatalf("ingest failed: errors=%+v err=%v", validationErrors, err)
	}
	if ingested.BusinessRefCount != 1 || ingested.ClaimCount != 0 {
		t.Fatalf("root fact counts must preserve business refs without claim: %+v", ingested)
	}

	chain, found, err := service.GetEvidenceChainByTraceID(
		context.Background(),
		"11111111111111111111111111111111",
		evidencevo.EvidenceQueryOptions{},
	)
	if err != nil || !found {
		t.Fatalf("query evidence chain: found=%v err=%v", found, err)
	}
	if len(chain.Data.BusinessRefs) != 1 || chain.Data.BusinessRefs[0]["ref_id"] != "object:supplychain:forecast" {
		t.Fatalf("knowledge business refs missing from evidence chain: %+v", chain.Data.BusinessRefs)
	}
	if len(chain.Data.Claims) != 0 || !chain.Partial || !contains(chain.PartialReasons, "missing_claim") {
		t.Fatalf("root fact must stay claim-free and explicit about partiality: %+v", chain)
	}

	graph, found, err := service.GetBusinessGraphByTraceID(
		context.Background(),
		"11111111111111111111111111111111",
		evidencevo.EvidenceQueryOptions{},
	)
	if err != nil || !found {
		t.Fatalf("query business graph: found=%v err=%v", found, err)
	}
	if !graphHasNode(graph.Data.Nodes, "event:evt_knowledge") ||
		!graphHasNode(graph.Data.Nodes, "business:object:supplychain:forecast") ||
		!graphHasEdgeType(graph.Data.Edges, "uses_business_ref") {
		t.Fatalf("root fact business story missing: %+v", graph.Data)
	}
	if contains(graph.PartialReasons, "missing_business_refs") || !contains(graph.PartialReasons, "missing_claim") {
		t.Fatalf("root fact partial reasons are misleading: %+v", graph.PartialReasons)
	}
}

func TestIngestPersistsDerivedKnowledgeNetworkScope(t *testing.T) {
	store := evidencestore.New()
	ingested, validationErrors, err := New(store).Ingest(
		context.Background(),
		mustJSON(t, twoPointOneBatch(validTwoPointOneEvents())),
	)
	if err != nil || len(validationErrors) > 0 {
		t.Fatalf("ingest failed: errors=%+v err=%v", validationErrors, err)
	}
	stored, err := store.GetEvidenceByTraceID(context.Background(), ingested.TraceID, evidencevo.EvidenceQueryOptions{})
	if err != nil || len(stored.Traces) != 1 {
		t.Fatalf("load stored trace: result=%+v err=%v", stored, err)
	}
	if !reflect.DeepEqual(stored.Traces[0].KnowledgeNetworkIDs, []string{"supplychain"}) {
		t.Fatalf("stored trace lost knowledge network scope: %v", stored.Traces[0].KnowledgeNetworkIDs)
	}
}

func TestIngestIsIdempotentForSameEventIDAndContent(t *testing.T) {
	store := evidencestore.New()
	service := New(store)
	body := mustJSON(t, twoPointOneBatch(validTwoPointOneEvents()[:2]))

	for i := 0; i < 2; i++ {
		_, validationErrors, err := service.Ingest(context.Background(), body)
		if err != nil {
			t.Fatalf("ingest %d: %v", i+1, err)
		}
		if len(validationErrors) > 0 {
			t.Fatalf("ingest %d validation errors: %+v", i+1, validationErrors)
		}
	}
	if got := store.TraceCount("11111111111111111111111111111111"); got != 1 {
		t.Fatalf("expected one stored trace after idempotent replay, got %d", got)
	}
}

func TestIngestRejectsSameEventIDWithDifferentContent(t *testing.T) {
	store := evidencestore.New()
	service := New(store)
	body := mustJSON(t, twoPointOneBatch(validTwoPointOneEvents()[:2]))
	if _, validationErrors, err := service.Ingest(context.Background(), body); err != nil || len(validationErrors) > 0 {
		t.Fatalf("first ingest failed: errors=%+v err=%v", validationErrors, err)
	}
	changed := strings.Replace(string(body), testHash("2"), testHash("b"), 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(changed))
	if err != nil {
		t.Fatalf("expected domain validation, got infrastructure error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_EVENT_ID_CONFLICT") {
		t.Fatalf("expected event id conflict, got %+v", validationErrors)
	}
	if got := store.TraceCount("11111111111111111111111111111111"); got != 1 {
		t.Fatalf("conflicting replay must not be stored, got %d traces", got)
	}
}

func TestIngestRejectsActionExecutionBeforeApproval(t *testing.T) {
	events := validTwoPointOneEvents()
	events = removeEventTypes(events, "action.approval_requested", "action.approved")
	service := New(evidencestore.New())

	_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_ACTION_TRANSITION_INVALID") {
		t.Fatalf("expected invalid action transition, got %+v", validationErrors)
	}
}

func TestIngestRejectsActionAfterRejectedTerminalStateAcrossBatches(t *testing.T) {
	store := evidencestore.New()
	service := New(store)
	events := validTwoPointOneEvents()
	rejected := cloneEventByType(t, events, "action.approved")
	rejected["event_id"] = "evt_action_rejected"
	rejected["event_type"] = "action.rejected"
	rejected["payload"].(map[string]any)["status"] = "rejected"
	first := removeEventTypes(events, "action.approved", "action.executed", "action.result_recorded")
	first = append(first, rejected)
	if _, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(first))); err != nil || len(validationErrors) > 0 {
		t.Fatalf("rejected lifecycle setup failed: errors=%+v err=%v", validationErrors, err)
	}
	executed := cloneEventByType(t, events, "action.executed")
	executed["causation_event_id"] = "evt_action_rejected"

	_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch([]map[string]any{executed})))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_ACTION_TRANSITION_INVALID") {
		t.Fatalf("expected rejected terminal state violation, got %+v", validationErrors)
	}
}

func TestIngestRejectsActionIdentityDrift(t *testing.T) {
	events := validTwoPointOneEvents()
	for _, event := range events {
		if event["event_type"] == "action.executed" {
			event["claim_id"] = "claim_other"
		}
	}
	service := New(evidencestore.New())

	_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_ACTION_TRANSITION_INVALID") {
		t.Fatalf("expected action identity drift rejection, got %+v", validationErrors)
	}
}

func TestIngestTwoPointOneSensitiveFieldProtection(t *testing.T) {
	events := validTwoPointOneEvents()
	events[1]["payload"].(map[string]any)["raw_sql"] = "select email from customer"
	service := New(evidencestore.New())

	_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_FORBIDDEN_RAW_PAYLOAD_FIELD") {
		t.Fatalf("expected sensitive field rejection, got %+v", validationErrors)
	}
}

func TestIngestRejectsUnregisteredTwoPointOnePayloadField(t *testing.T) {
	events := validTwoPointOneEvents()
	events[1]["payload"].(map[string]any)["unexpected_detail"] = "not registered"
	service := New(evidencestore.New())

	_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_PAYLOAD_FIELD_UNSUPPORTED") {
		t.Fatalf("expected payload allowlist rejection, got %+v", validationErrors)
	}
}

func TestIngestRejectsSensitiveKeysAndAllRawSQLForBothVersions(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{name: "2.1 password", body: mustJSON(t, batchWithPayloadField(t, "password", "secret"))},
		{name: "2.1 private key", body: mustJSON(t, batchWithPayloadField(t, "private_key", "key material"))},
		{name: "2.1 approval comment", body: mustJSON(t, batchWithPayloadField(t, "approval_comment", "approved because..."))},
		{name: "2.0 user question", body: []byte(strings.Replace(validBatch(), `"version_status": "versioned"`, `"version_status": "versioned", "user_question": "raw question"`, 1))},
		{name: "2.0 update sql", body: []byte(strings.Replace(validBatch(), `"version_status": "versioned"`, `"version_status": "versioned", "note": "UPDATE customer SET name = ?"`, 1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), tt.body)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !hasValidationCode(validationErrors, "BKN_TRACE_SENSITIVE_VALUE_LEAKED") && !hasValidationCode(validationErrors, "BKN_TRACE_FORBIDDEN_RAW_PAYLOAD_FIELD") {
				t.Fatalf("expected sensitive payload rejection, got %+v", validationErrors)
			}
		})
	}
}

func TestIngestRejectsLegacyPrivateEventsInTwoPointOne(t *testing.T) {
	for _, eventType := range []string{"structured_output.validated", "agent_as_tool.invoked", "tool.budget.exhausted"} {
		t.Run(eventType, func(t *testing.T) {
			events := validTwoPointOneEvents()
			events[1]["event_type"] = eventType
			_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !hasValidationCode(validationErrors, "BKN_TRACE_EVENT_TYPE_UNSUPPORTED") {
				t.Fatalf("expected unsupported event, got %+v", validationErrors)
			}
		})
	}
}

func TestIngestKeepsLegacyPrivateEventsReadableInTwoPointZero(t *testing.T) {
	for _, eventType := range []string{"structured_output.validated", "agent_as_tool.invoked", "tool.budget.exhausted"} {
		t.Run(eventType, func(t *testing.T) {
			body := strings.Replace(toolEventsBatch(), `"event_type": "tool.called"`, `"event_type": "`+eventType+`"`, 1)
			_, validationErrors, err := New(&fakeStore{}).Ingest(context.Background(), []byte(body))
			if err != nil || len(validationErrors) > 0 {
				t.Fatalf("expected 2.0 compatibility, errors=%+v err=%v", validationErrors, err)
			}
		})
	}
}

func TestBusinessRefsUnresolvedAllowsEmptyRefsAndMarksPartial(t *testing.T) {
	events := removeEventTypes(validTwoPointOneEvents(), "action.recommended", "action.approval_requested", "action.approved", "action.executed", "action.result_recorded")
	for _, event := range events {
		if event["event_type"] == "business.refs.resolved" {
			payload := event["payload"].(map[string]any)
			payload["resolver_status"] = "unresolved"
			payload["business_refs"] = []any{}
		}
	}
	store := evidencestore.New()
	service := New(store)
	if _, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events))); err != nil || len(validationErrors) > 0 {
		t.Fatalf("unresolved ingest failed: errors=%+v err=%v", validationErrors, err)
	}
	response, found, err := service.GetEvidenceChainByTraceID(context.Background(), "11111111111111111111111111111111", evidencevo.EvidenceQueryOptions{})
	if err != nil || !found {
		t.Fatalf("query failed: found=%v err=%v", found, err)
	}
	if !response.Partial || !contains(response.PartialReasons, "business_ref_unresolved") {
		t.Fatalf("expected business_ref_unresolved, got %+v", response.PartialReasons)
	}
}

func TestBusinessRefsResolvedRequiresAtLeastOneRef(t *testing.T) {
	events := validTwoPointOneEvents()
	for _, event := range events {
		if event["event_type"] == "business.refs.resolved" {
			event["payload"].(map[string]any)["business_refs"] = []any{}
		}
	}
	_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil || !hasValidationPath(validationErrors, "business_refs") {
		t.Fatalf("expected resolved refs requirement, errors=%+v err=%v", validationErrors, err)
	}
}

func TestBusinessRefsPartialRequiresAtLeastOneRef(t *testing.T) {
	events := validTwoPointOneEvents()
	for _, event := range events {
		if event["event_type"] == "business.refs.resolved" {
			payload := event["payload"].(map[string]any)
			payload["resolver_status"] = "partial"
			payload["business_refs"] = []any{}
		}
	}
	_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil || !hasValidationPath(validationErrors, "business_refs") {
		t.Fatalf("expected partial refs requirement, errors=%+v err=%v", validationErrors, err)
	}
}

func TestBusinessRefsPartialWithRefsMarksQueryPartial(t *testing.T) {
	events := removeEventTypes(validTwoPointOneEvents(), "action.recommended", "action.approval_requested", "action.approved", "action.executed", "action.result_recorded")
	for _, event := range events {
		if event["event_type"] == "business.refs.resolved" {
			event["payload"].(map[string]any)["resolver_status"] = "partial"
		}
	}
	store := evidencestore.New()
	service := New(store)
	if _, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events))); err != nil || len(validationErrors) > 0 {
		t.Fatalf("partial ingest failed: errors=%+v err=%v", validationErrors, err)
	}
	response, found, err := service.GetEvidenceChainByTraceID(context.Background(), "11111111111111111111111111111111", evidencevo.EvidenceQueryOptions{})
	if err != nil || !found || !contains(response.PartialReasons, "business_ref_unresolved") {
		t.Fatalf("expected partial resolver reason, response=%+v found=%v err=%v", response, found, err)
	}
}

func TestIngestEnforcesExactRefAllowlistAndHashFormat(t *testing.T) {
	t.Run("unregistered ref field", func(t *testing.T) {
		events := validTwoPointOneEvents()
		for _, event := range events {
			if event["event_type"] == "evidence.refs.created" {
				ref := event["payload"].(map[string]any)["evidence_refs"].([]any)[0].(map[string]any)
				ref["summary"] = "raw summary"
				ref["label"] = "forecast table"
				ref["partial_reason"] = "not registered"
			}
		}
		_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
		if err != nil || !hasValidationPath(validationErrors, "summary") || !hasValidationPath(validationErrors, "label") || !hasValidationPath(validationErrors, "partial_reason") {
			t.Fatalf("expected exact ref allowlist rejection, errors=%+v err=%v", validationErrors, err)
		}
	})
	t.Run("invalid hash", func(t *testing.T) {
		events := validTwoPointOneEvents()
		events[0]["payload"].(map[string]any)["intent_hash"] = "sha256:ABC"
		_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
		if err != nil || !hasValidationPath(validationErrors, "intent_hash") {
			t.Fatalf("expected canonical hash rejection, errors=%+v err=%v", validationErrors, err)
		}
	})
	t.Run("registered summary hash", func(t *testing.T) {
		events := validTwoPointOneEvents()
		for _, event := range events {
			if event["event_type"] == "evidence.refs.created" {
				ref := event["payload"].(map[string]any)["evidence_refs"].([]any)[0].(map[string]any)
				ref["summary_hash"] = testHash("c")
			}
		}
		_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
		if err != nil || len(validationErrors) > 0 {
			t.Fatalf("expected registered summary_hash, errors=%+v err=%v", validationErrors, err)
		}
	})
}

func TestIngestRejectsSelfAndForwardCausation(t *testing.T) {
	tests := map[string]func([]map[string]any){
		"self": func(events []map[string]any) { events[1]["causation_event_id"] = events[1]["event_id"] },
		"cycle": func(events []map[string]any) {
			events[1]["causation_event_id"] = events[2]["event_id"]
			events[2]["causation_event_id"] = events[1]["event_id"]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			events := validTwoPointOneEvents()
			mutate(events)
			_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !hasValidationPath(validationErrors, "causation_event_id") {
				t.Fatalf("expected causation rejection, got %+v", validationErrors)
			}
		})
	}
}

func TestAsyncFactCausationBecomesCompleteWhenParentArrives(t *testing.T) {
	store := evidencestore.New()
	service := New(store)
	events := validTwoPointOneEvents()
	child := cloneEventByType(t, events, "data.query.observed")
	child["causation_event_id"] = "evt_interaction"

	if _, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch([]map[string]any{child}))); err != nil || len(validationErrors) > 0 {
		t.Fatalf("child fact must be accepted before its parent: errors=%+v err=%v", validationErrors, err)
	}
	first, found, err := service.GetEvidenceChainByTraceID(context.Background(), "11111111111111111111111111111111", evidencevo.EvidenceQueryOptions{})
	if err != nil || !found || !first.Partial || !contains(first.PartialReasons, "causality_missing") {
		t.Fatalf("missing parent must produce causality_missing: response=%+v found=%v err=%v", first, found, err)
	}

	parent := cloneEventByType(t, events, "agent.interaction.started")
	if _, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch([]map[string]any{parent}))); err != nil || len(validationErrors) > 0 {
		t.Fatalf("late parent must be accepted: errors=%+v err=%v", validationErrors, err)
	}
	second, found, err := service.GetEvidenceChainByTraceID(context.Background(), "11111111111111111111111111111111", evidencevo.EvidenceQueryOptions{})
	if err != nil || !found || contains(second.PartialReasons, "causality_missing") {
		t.Fatalf("late parent must resolve causality_missing: response=%+v found=%v err=%v", second, found, err)
	}
}

func TestIngestRejectsTraceOwnershipDrift(t *testing.T) {
	store := evidencestore.New()
	service := New(store)
	initial := removeEventTypes(validTwoPointOneEvents(), "data.query.observed", "retrieval.completed", "knowledge.read.observed", "model.call.observed", "claim.created", "evidence.refs.created", "business.refs.resolved", "action.recommended", "action.approval_requested", "action.approved", "action.executed", "action.result_recorded")
	if _, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(initial))); err != nil || len(validationErrors) > 0 {
		t.Fatalf("initial ingest failed: errors=%+v err=%v", validationErrors, err)
	}

	tests := map[string]func(map[string]any){
		"request": func(batch map[string]any) {
			batch["trace"].(map[string]any)["bkn.request.id"] = "req_other"
			batch["events"].([]map[string]any)[0]["bkn.request.id"] = "req_other"
		},
		"domain":  func(batch map[string]any) { batch["trace"].(map[string]any)["business_domain"] = "bd_other" },
		"account": func(batch map[string]any) { batch["trace"].(map[string]any)["bkn.account.id"] = "acct_other" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			late := cloneEventByType(t, validTwoPointOneEvents(), "data.query.observed")
			late["event_id"] = "evt_late_" + name
			batch := twoPointOneBatch([]map[string]any{late})
			mutate(batch)
			_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, batch))
			if err != nil || !hasValidationCode(validationErrors, "BKN_TRACE_OWNERSHIP_CONFLICT") {
				t.Fatalf("expected ownership conflict: errors=%+v err=%v", validationErrors, err)
			}
		})
	}
}

func TestIngestChecksOwnershipBeforeEventContent(t *testing.T) {
	store := evidencestore.New()
	service := New(store)
	interaction := cloneEventByType(t, validTwoPointOneEvents(), "agent.interaction.started")
	if _, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch([]map[string]any{interaction}))); err != nil || len(validationErrors) > 0 {
		t.Fatalf("initial ingest failed: errors=%+v err=%v", validationErrors, err)
	}
	conflicting := cloneEventByType(t, validTwoPointOneEvents(), "agent.interaction.started")
	conflicting["payload"].(map[string]any)["intent_hash"] = testHash("f")
	batch := twoPointOneBatch([]map[string]any{conflicting})
	batch["trace"].(map[string]any)["bkn.account.id"] = "acct_intruder"

	_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, batch))
	if err != nil || !hasValidationCode(validationErrors, "BKN_TRACE_OWNERSHIP_CONFLICT") || hasValidationCode(validationErrors, "BKN_TRACE_EVENT_ID_CONFLICT") {
		t.Fatalf("ownership must be checked before event content: errors=%+v err=%v", validationErrors, err)
	}
}

func TestIngestPreservesLegacyCausalityAfterTwoPointOneAppend(t *testing.T) {
	store := evidencestore.New()
	service := New(store)
	if _, validationErrors, err := service.Ingest(context.Background(), []byte(validBatch())); err != nil || len(validationErrors) > 0 {
		t.Fatalf("legacy ingest failed: errors=%+v err=%v", validationErrors, err)
	}
	late := cloneEventByType(t, validTwoPointOneEvents(), "agent.interaction.started")
	late["trace_id"] = "8c0d0000000000000000000000000001"
	late["bkn.request.id"] = "req_phase2_001"
	batch := twoPointOneBatch([]map[string]any{late})
	trace := batch["trace"].(map[string]any)
	trace["trace_id"] = "8c0d0000000000000000000000000001"
	trace["traceparent"] = "00-8c0d0000000000000000000000000001-1f12000000000001-01"
	trace["bkn.request.id"] = "req_phase2_001"
	trace["bkn.tenant.id"] = "tenant_demo"
	trace["business_domain"] = ""
	trace["bkn.account.id"] = "acct_demo"
	trace["bkn.account.type"] = "app"
	if _, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, batch)); err != nil || len(validationErrors) > 0 {
		t.Fatalf("2.1 append failed: errors=%+v err=%v", validationErrors, err)
	}
	response, found, err := service.GetEvidenceChainByTraceID(context.Background(), "8c0d0000000000000000000000000001", evidencevo.EvidenceQueryOptions{})
	if err != nil || !found || !contains(response.PartialReasons, "causality_missing") {
		t.Fatalf("mixed history must retain causality_missing: response=%+v found=%v err=%v", response, found, err)
	}
}

func TestIngestRejectsInvalidTechnicalJoinAndTimestamp(t *testing.T) {
	tests := map[string]func(map[string]any){
		"traceparent join": func(batch map[string]any) {
			batch["trace"].(map[string]any)["traceparent"] = "00-22222222222222222222222222222222-1000000000000001-01"
		},
		"span format": func(batch map[string]any) { batch["events"].([]map[string]any)[0]["span_id"] = "span-bad" },
		"calendar time": func(batch map[string]any) {
			batch["events"].([]map[string]any)[0]["observed_at"] = "2026-02-31T08:00:00Z"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			batch := twoPointOneBatch([]map[string]any{cloneEventByType(t, validTwoPointOneEvents(), "agent.interaction.started")})
			mutate(batch)
			_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, batch))
			if err != nil || len(validationErrors) == 0 {
				t.Fatalf("expected technical join rejection: errors=%+v err=%v", validationErrors, err)
			}
		})
	}
}

func TestIngestRejectsSensitiveReferenceValues(t *testing.T) {
	values := []string{
		"https://bucket.example/evidence.json?X-Amz-Signature=secret",
		"s3://evidence-bucket/snapshot.json",
		"alice@example.com",
		"13800138000",
		"physical_table.customer_email",
		"customer.email",
		"table:customer",
		"column:email",
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			events := validTwoPointOneEvents()
			for _, event := range events {
				if event["event_type"] == "evidence.refs.created" {
					event["payload"].(map[string]any)["evidence_refs"].([]any)[0].(map[string]any)["ref_id"] = value
				}
			}
			_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
			if err != nil || !hasValidationCode(validationErrors, "BKN_TRACE_SENSITIVE_VALUE_LEAKED") {
				t.Fatalf("expected sensitive ref rejection: errors=%+v err=%v", validationErrors, err)
			}
		})
	}
}

func TestCheckSensitiveAllowsCanonicalPayloadHash(t *testing.T) {
	validationErrors := evidencevo.ValidationErrors{}
	checkSensitive(map[string]any{
		"payload_hash": "43258cff783fe7036d8a43033f830adfc60ec037382473548ac742b888292777",
	}, "$", &validationErrors)
	if len(validationErrors) != 0 {
		t.Fatalf("canonical payload hash must not be treated as sensitive: %+v", validationErrors)
	}
}

func TestIngestRejectsAmbiguousShortBusinessReference(t *testing.T) {
	events := validTwoPointOneEvents()
	for _, event := range events {
		if event["event_type"] == "business.refs.resolved" {
			event["payload"].(map[string]any)["business_refs"].([]any)[0].(map[string]any)["ref_id"] = "object:customer"
		}
	}
	_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil || !hasValidationCode(validationErrors, "BKN_TRACE_REFERENCE_ID_INVALID") {
		t.Fatalf("expected short business ref rejection: errors=%+v err=%v", validationErrors, err)
	}
}

func TestIngestRejectsAmbiguousShortActionTarget(t *testing.T) {
	events := validTwoPointOneEvents()
	for _, event := range events {
		if event["event_type"] == "action.recommended" {
			event["payload"].(map[string]any)["target_refs"] = []any{"object:customer"}
		}
	}
	_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil || !hasValidationCode(validationErrors, "BKN_TRACE_REFERENCE_ID_INVALID") {
		t.Fatalf("expected short action target rejection: errors=%+v err=%v", validationErrors, err)
	}
}

func TestIngestBindsActionStatusAndRequiresSafeExecutionError(t *testing.T) {
	t.Run("status mismatch", func(t *testing.T) {
		events := validTwoPointOneEvents()
		for _, event := range events {
			if event["event_type"] == "action.approved" {
				event["payload"].(map[string]any)["status"] = "rejected"
			}
		}
		_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
		if err != nil || !hasValidationPath(validationErrors, "status") {
			t.Fatalf("expected status mismatch rejection, errors=%+v err=%v", validationErrors, err)
		}
	})
	t.Run("execution error summary", func(t *testing.T) {
		events := validTwoPointOneEvents()
		for _, event := range events {
			if event["event_type"] == "action.executed" {
				event["payload"].(map[string]any)["status"] = "error"
			}
		}
		_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
		if err != nil || !hasValidationPath(validationErrors, "error_category") || !hasValidationPath(validationErrors, "error_hash") {
			t.Fatalf("expected safe error summary requirement, errors=%+v err=%v", validationErrors, err)
		}
	})
}

func TestModelCallErrorRequiresSafeErrorSummary(t *testing.T) {
	events := validTwoPointOneEvents()
	for _, event := range events {
		if event["event_type"] == "model.call.observed" {
			event["payload"].(map[string]any)["status"] = "error"
		}
	}
	_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil || !hasValidationPath(validationErrors, "error_category") || !hasValidationPath(validationErrors, "error_hash") {
		t.Fatalf("expected model error summary requirement, errors=%+v err=%v", validationErrors, err)
	}

	for _, event := range events {
		if event["event_type"] == "model.call.observed" {
			payload := event["payload"].(map[string]any)
			payload["error_category"] = "provider_unavailable"
			payload["error_hash"] = testHash("d")
		}
	}
	_, validationErrors, err = New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
	if err != nil || len(validationErrors) > 0 {
		t.Fatalf("expected safe model error summary acceptance, errors=%+v err=%v", validationErrors, err)
	}
}

func TestIngestEnforcesTwoPointOneRequiredTypesAndEnums(t *testing.T) {
	tests := map[string]func([]map[string]any){
		"retrieval source refs": func(events []map[string]any) {
			delete(clonePayloadByType(t, events, "retrieval.completed"), "source_refs")
		},
		"retrieval count type": func(events []map[string]any) {
			clonePayloadByType(t, events, "retrieval.completed")["candidate_count"] = "3"
		},
		"data version": func(events []map[string]any) {
			delete(clonePayloadByType(t, events, "data.query.observed"), "version_status")
		},
		"data truncated type": func(events []map[string]any) {
			clonePayloadByType(t, events, "data.query.observed")["truncated"] = "false"
		},
		"model status enum": func(events []map[string]any) {
			clonePayloadByType(t, events, "model.call.observed")["status"] = "unknown"
		},
		"claim type enum": func(events []map[string]any) {
			clonePayloadByType(t, events, "claim.created")["claim_type"] = "free_form"
		},
		"action result status enum": func(events []map[string]any) {
			clonePayloadByType(t, events, "action.result_recorded")["status"] = "anything"
		},
		"claim source type": func(events []map[string]any) {
			clonePayloadByType(t, events, "claim.created")["source_event_ids"] = []any{7}
		},
		"action target type": func(events []map[string]any) {
			clonePayloadByType(t, events, "action.recommended")["target_refs"] = []any{false}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			events := validTwoPointOneEvents()
			mutate(events)
			_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, twoPointOneBatch(events)))
			if err != nil || len(validationErrors) == 0 {
				t.Fatalf("expected strict contract rejection: errors=%+v err=%v", validationErrors, err)
			}
		})
	}
}

func clonePayloadByType(t *testing.T, events []map[string]any, eventType string) map[string]any {
	t.Helper()
	for _, event := range events {
		if event["event_type"] == eventType {
			return event["payload"].(map[string]any)
		}
	}
	t.Fatalf("event type %s not found", eventType)
	return nil
}

func TestIngestAtomicallyRejectsConcurrentActionForkInMemory(t *testing.T) {
	store := evidencestore.New()
	service := New(store)
	events := validTwoPointOneEvents()
	setup := removeEventTypes(events, "action.approved", "action.executed", "action.result_recorded")
	if _, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch(setup))); err != nil || len(validationErrors) > 0 {
		t.Fatalf("setup failed: errors=%+v err=%v", validationErrors, err)
	}
	approved := cloneEventByType(t, events, "action.approved")
	rejected := cloneEventByType(t, events, "action.approved")
	rejected["event_id"] = "evt_action_rejected"
	rejected["event_type"] = "action.rejected"
	rejected["payload"].(map[string]any)["status"] = "rejected"

	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for _, event := range []map[string]any{approved, rejected} {
		wg.Add(1)
		go func(event map[string]any) {
			defer wg.Done()
			_, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointOneBatch([]map[string]any{event})))
			results <- err == nil && len(validationErrors) == 0
		}(event)
	}
	wg.Wait()
	close(results)
	successes := 0
	for success := range results {
		if success {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one action branch to commit, got %d", successes)
	}
}

func TestIngestAcceptsHashOnlyToolEvents(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	response, validationErrors, err := service.Ingest(context.Background(), []byte(toolEventsBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) > 0 {
		t.Fatalf("unexpected validation errors: %+v", validationErrors)
	}
	if store.calls != 1 {
		t.Fatalf("expected evidence to be stored once, got %d", store.calls)
	}
	if response.AcceptedEvents != 2 || response.ClaimCount != 0 || response.EvidenceRefCount != 0 || response.BusinessRefCount != 0 {
		t.Fatalf("unexpected response counts: %+v", response)
	}
}

func TestIngestRejectsMissingClaimID(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(missingClaimIDBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) == 0 {
		t.Fatal("expected validation errors")
	}
	if validationErrors[0].Code != "BKN_TRACE_REQUIRED_FIELD_MISSING" {
		t.Fatalf("unexpected error code: %+v", validationErrors[0])
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsSensitivePayload(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(sensitiveBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) == 0 {
		t.Fatal("expected validation errors")
	}
	if validationErrors[0].Code != "BKN_TRACE_FORBIDDEN_RAW_PAYLOAD_FIELD" {
		t.Fatalf("unexpected error code: %+v", validationErrors[0])
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsRawToolPayload(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(rawToolPayloadBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_FORBIDDEN_RAW_PAYLOAD_FIELD") {
		t.Fatalf("expected raw tool payload rejection, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsUnknownClaimIDWithoutClaimBatch(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(unknownClaimIDBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_UNKNOWN_CLAIM_ID") {
		t.Fatalf("expected unknown claim id error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsJoinMismatch(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"bkn.request.id": "req_phase2_001",`, `"bkn.request.id": "req_phase2_other",`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_JOIN_FAILED") {
		t.Fatalf("expected join failed error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsUnsupportedSchemaVersion(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"bkn.trace.schema.version": "2.0.0"`, `"bkn.trace.schema.version": "1.0.0"`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_SCHEMA_VERSION_UNSUPPORTED") {
		t.Fatalf("expected unsupported schema error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsInvalidTraceparent(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"traceparent": "00-8c0d0000000000000000000000000001-1f12000000000001-01"`, `"traceparent": "00-00000000000000000000000000000000-0000000000000000-01"`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_INVALID_TRACEPARENT") {
		t.Fatalf("expected invalid traceparent error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsInvalidTimestamp(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"observed_at": "2026-07-22T04:00:00.000000000Z"`, `"observed_at": "2026-07-22 04:00:00"`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_INVALID_TIMESTAMP") {
		t.Fatalf("expected invalid timestamp error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsUnsupportedVisibilityState(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"visibility": "visible"`, `"visibility": "tenant_denied"`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_VISIBILITY_UNSUPPORTED") {
		t.Fatalf("expected unsupported visibility error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestRejectsEmptyEvents(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(emptyEventsBatch()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_REQUIRED_FIELD_MISSING") {
		t.Fatalf("expected required field error, got %+v", validationErrors)
	}
	if store.calls != 0 {
		t.Fatalf("invalid batch must not be stored")
	}
}

func TestIngestAllowsReferenceLikeStringsAndNonSensitiveKeySubstrings(t *testing.T) {
	store := &fakeStore{}
	service := New(store)
	body := strings.Replace(validBatch(), `"version_status": "versioned"`, `"version_status": "versioned",
		"source_ref": "document:source:123",
		"owner_ref": "account:company-user",
        "prompt_note": "prompt: is a label in external documentation",
        "token_bucket": "rate-limit-window",
        "cookie_policy": "same-site",
        "authorization_scope": "trace:evidence"`, 1)

	_, validationErrors, err := service.Ingest(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(validationErrors) > 0 {
		t.Fatalf("unexpected validation errors: %+v", validationErrors)
	}
	if store.calls != 1 {
		t.Fatalf("expected evidence to be stored once, got %d", store.calls)
	}
}

func TestGetEvidenceChainByTraceIDFiltersHiddenRefsAndReturnsEnvelope(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{queryTrace("trace_query_001", "req_query_001")}}
	service := New(store)

	response, found, err := service.GetEvidenceChainByTraceID(context.Background(), "trace_query_001", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected evidence chain to be found")
	}
	if response.TraceID != "trace_query_001" || response.RequestID != "req_query_001" {
		t.Fatalf("unexpected identity: %+v", response)
	}
	if response.Partial {
		t.Fatalf("expected complete visible response, got partial: %+v", response.PartialReasons)
	}
	if response.VisibilitySummary.HiddenRefCount != 1 || response.VisibilitySummary.AuthorizedRefCount != 2 {
		t.Fatalf("unexpected visibility summary: %+v", response.VisibilitySummary)
	}
	if response.Page.NodeCount != 3 || response.Page.EdgeCount != 2 || response.Page.Truncated {
		t.Fatalf("unexpected page: %+v", response.Page)
	}
	if len(response.Data.Claims) != 1 || len(response.Data.EvidenceRefs) != 1 || len(response.Data.BusinessRefs) != 1 {
		t.Fatalf("unexpected data counts: %+v", response.Data)
	}
	if response.Data.EvidenceRefs[0]["ref_id"] != "row:visible" {
		t.Fatalf("hidden evidence ref leaked or visible ref missing: %+v", response.Data.EvidenceRefs)
	}
}

func TestGetEvidenceChainByRequestIDReturnsMissingClaimPartial(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{{
		TraceID:   "trace_no_claim",
		RequestID: "req_no_claim",
		Events: []evidencevo.EvidenceEvent{
			{
				EventType: "evidence.refs.created",
				Payload: map[string]any{
					"claim_id":      "missing_claim",
					"evidence_refs": []any{map[string]any{"ref_id": "row:visible", "visibility": "visible"}},
				},
			},
		},
	}}}
	service := New(store)

	response, found, err := service.GetEvidenceChainByRequestID(context.Background(), "req_no_claim", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected evidence chain to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "missing_claim") {
		t.Fatalf("expected missing claim partial, got: %+v", response)
	}
}

func TestGetEvidenceChainMarksQueryTruncated(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{
		queryTrace("trace_query_001", "req_query_truncated"),
		queryTrace("trace_query_002", "req_query_truncated"),
	}}
	service := New(store)

	response, found, err := service.GetEvidenceChainByRequestID(context.Background(), "req_query_truncated", evidencevo.EvidenceQueryOptions{Limit: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected evidence chain to be found")
	}
	if !response.Partial || !response.Page.Truncated || !contains(response.PartialReasons, "evidence_query_truncated") {
		t.Fatalf("expected truncated partial response, got: %+v", response)
	}
}

func TestGetEvidenceChainMarksMissingClaimSourceEvent(t *testing.T) {
	trace := queryTrace("trace_missing_source", "req_missing_source")
	trace.SchemaVersion = evidencevo.ContractVersion
	trace.Events[0].Payload["source_event_ids"] = []any{"evt_not_arrived"}
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{trace}}

	response, found, err := New(store).GetEvidenceChainByTraceID(context.Background(), trace.TraceID, evidencevo.EvidenceQueryOptions{})
	if err != nil || !found {
		t.Fatalf("query failed: found=%v err=%v", found, err)
	}
	if !response.Partial || !contains(response.PartialReasons, "source_event_missing") {
		t.Fatalf("expected source_event_missing, got %+v", response.PartialReasons)
	}
}

func TestGetEvidenceChainMarksLegacyCausalityMissing(t *testing.T) {
	trace := queryTrace("trace_legacy", "req_legacy")
	trace.SchemaVersion = evidencevo.LegacyContractVersion
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{trace}}

	response, found, err := New(store).GetEvidenceChainByTraceID(context.Background(), trace.TraceID, evidencevo.EvidenceQueryOptions{})
	if err != nil || !found {
		t.Fatalf("query failed: found=%v err=%v", found, err)
	}
	if !response.Partial || !contains(response.PartialReasons, "causality_missing") {
		t.Fatalf("expected causality_missing, got %+v", response.PartialReasons)
	}
}

func TestGetEvidenceChainSeparatesUnauthorizedRefsWithoutLeakingDetails(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{evidenceChainTraceWithUnauthorizedRef("trace_chain_authz", "req_chain_authz")}}
	service := New(store)

	response, found, err := service.GetEvidenceChainByTraceID(context.Background(), "trace_chain_authz", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected evidence chain to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "evidence_ref_unauthorized") {
		t.Fatalf("expected unauthorized partial reason, got: %+v", response)
	}
	if response.VisibilitySummary.UnauthorizedRefCount != 1 || response.VisibilitySummary.AuthorizedRefCount != 2 {
		t.Fatalf("expected distinct authorized/unauthorized counts, got %+v", response.VisibilitySummary)
	}
	if len(response.Data.EvidenceRefs) != 1 || response.Data.EvidenceRefs[0]["ref_id"] != "row:visible" {
		t.Fatalf("unauthorized evidence ref details must not leak: %+v", response.Data.EvidenceRefs)
	}
}

func TestGetBusinessGraphByTraceIDReturnsClaimAndBusinessNodes(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{queryTrace("trace_graph_001", "req_graph_001")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_graph_001", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if response.TraceID != "trace_graph_001" || response.RequestID != "req_graph_001" {
		t.Fatalf("unexpected identity: %+v", response)
	}
	if !response.Partial || !contains(response.PartialReasons, "resolver_unresolved") {
		t.Fatalf("graph without an authorized query resolver must be partial: %+v", response.PartialReasons)
	}
	if response.Page.NodeCount != 2 || response.Page.EdgeCount != 1 {
		t.Fatalf("unexpected page: %+v", response.Page)
	}
	if response.VisibilitySummary.AuthorizedRefCount != 1 {
		t.Fatalf("unexpected visibility summary: %+v", response.VisibilitySummary)
	}
	if len(response.Data.Nodes) != 2 || len(response.Data.Edges) != 1 {
		t.Fatalf("unexpected graph size: %+v", response.Data)
	}
	for _, node := range response.Data.Nodes {
		if strings.HasPrefix(node.ID, "business:") {
			if node.Label != "" {
				t.Fatalf("untrusted event label must not become a business display name: %+v", node)
			}
			if _, leaked := node.Properties["label"]; leaked {
				t.Fatalf("untrusted event label must not leak through node properties: %+v", node)
			}
		}
	}
	if response.Data.Edges[0].SourceID != "claim:claim_visible" || response.Data.Edges[0].TargetID != "business:object:kn_demo:customer" {
		t.Fatalf("unexpected edge: %+v", response.Data.Edges[0])
	}
}

func TestBusinessGraphAddsDisplayOnlyFromResolvedResolverMetadata(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{queryTrace("trace_graph_display", "req_graph_display")}}
	resolver := &fakeBusinessResolver{resolutions: []ibusinessresolver.Resolution{{
		RefID: "object:kn_demo:customer", Visibility: "visible", Display: &evidencevo.BusinessDisplay{
			Name: "客户", BusinessPath: []string{"客户管理", "客户"}, ControlledSummary: "客户主数据对象",
			ResolutionStatus: "resolved", SourceVersion: "schema-v7",
		},
	}}}
	scope := evidencevo.QueryScope{TenantID: "tenant_1", BusinessDomain: "domain_1", AccountID: "user_1", AccountType: "user"}

	response, found, err := NewWithBusinessResolver(store, resolver).GetBusinessGraphByTraceID(
		context.Background(), "trace_graph_display", evidencevo.EvidenceQueryOptions{Scope: scope},
	)
	if err != nil || !found {
		t.Fatalf("query resolved graph: found=%v err=%v", found, err)
	}
	if len(resolver.requests) != 1 || resolver.requests[0].Scope != scope {
		t.Fatalf("resolver must receive trusted query scope: %+v", resolver.requests)
	}
	if len(resolver.requests[0].Refs) != 1 || resolver.requests[0].Refs[0].RefID != "object:kn_demo:customer" {
		t.Fatalf("resolver must receive controlled refs: %+v", resolver.requests[0].Refs)
	}
	node := businessNodeByID(t, response.Data.Nodes, "business:object:kn_demo:customer")
	if node.Display == nil || node.Display.Name != "客户" || strings.Join(node.Display.BusinessPath, "/") != "客户管理/客户" {
		t.Fatalf("expected authorized controlled display: %+v", node)
	}
	if contains(response.PartialReasons, "resolver_unresolved") {
		t.Fatalf("resolved graph must clear resolver_unresolved: %+v", response.PartialReasons)
	}
}

func TestBusinessGraphPromotesClaimedRetrievalSourceToResolvedBusinessEvidence(t *testing.T) {
	trace := evidencevo.NormalizedTrace{
		TraceID: "trace_claimed_retrieval", RequestID: "req_claimed_retrieval",
		Events: []evidencevo.EvidenceEvent{
			{
				EventID: "evt_retrieval", EventType: "retrieval.completed",
				InteractionID: "interaction_1", OperationID: "operation_query",
				Payload: map[string]any{
					"source_refs": []any{map[string]any{
						"ref_id":   "object:supplychain_hd0202:supplychain_hd0202_forecast",
						"ref_type": "object", "source_system": "bkn",
						"validity": "observed", "version_status": "unversioned", "visibility": "visible",
					}},
				},
			},
			{
				EventID: "evt_claim", EventType: "claim.created", InteractionID: "interaction_1",
				ClaimID: "claim_forecast_zero", Payload: map[string]any{
					"claim_id": "claim_forecast_zero", "claim_type": "answer",
					"claim_hash": "sha256:claim", "visibility": "visible", "version_status": "unversioned",
					"source_event_ids": []any{"evt_retrieval"}, "operation_ids": []any{"operation_query"},
				},
			},
			{
				EventID: "evt_unresolved", EventType: "business.refs.resolved",
				InteractionID: "interaction_1", OperationID: "operation_query",
				ClaimID: "claim_forecast_zero", Payload: map[string]any{
					"claim_id": "claim_forecast_zero", "resolver_status": "unresolved",
					"business_refs": []any{},
				},
			},
		},
	}
	resolver := &fakeBusinessResolver{resolutions: []ibusinessresolver.Resolution{{
		RefID:      "object:supplychain_hd0202:supplychain_hd0202_forecast",
		Visibility: "visible",
		Display: &evidencevo.BusinessDisplay{
			Name: "产品需求预测单", BusinessPath: []string{"供应链", "产品需求预测单"},
			ResolutionStatus: "resolved", SourceVersion: "main",
		},
	}}}
	scope := evidencevo.QueryScope{
		BusinessDomain: "bd_public", AccountID: "user_1", AccountType: "user",
	}

	response, found, err := NewWithBusinessResolver(
		&fakeStore{traces: []evidencevo.NormalizedTrace{trace}}, resolver,
	).GetBusinessGraphByTraceID(
		context.Background(), trace.TraceID, evidencevo.EvidenceQueryOptions{Scope: scope},
	)

	if err != nil || !found {
		t.Fatalf("query graph: found=%v err=%v", found, err)
	}
	node := businessNodeByID(
		t, response.Data.Nodes,
		"business:object:supplychain_hd0202:supplychain_hd0202_forecast",
	)
	if node.Display == nil || node.Display.Name != "产品需求预测单" {
		t.Fatalf("claimed retrieval source must become resolved business evidence: %+v", node)
	}
	if node.Stage != "evidence" {
		t.Fatalf("resolved business reference must be projected into evidence stage: %+v", node)
	}
	if contains(response.PartialReasons, "business_ref_unresolved") ||
		contains(response.PartialReasons, "missing_business_refs") ||
		contains(response.PartialReasons, "resolver_unresolved") {
		t.Fatalf("resolved claimed source must clear business-ref partial reasons: %+v", response.PartialReasons)
	}
	if len(resolver.requests) != 1 || len(resolver.requests[0].Refs) != 1 {
		t.Fatalf("resolver must receive claimed retrieval source ref: %+v", resolver.requests)
	}
}

func TestBusinessGraphResolverVisibilityDoesNotOverrideRecordAuthorization(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{queryTrace("trace_graph_resolver_denied", "req_graph_resolver_denied")}}
	resolver := &fakeBusinessResolver{resolutions: []ibusinessresolver.Resolution{{
		RefID: "object:kn_demo:customer", Visibility: "unauthorized",
	}}}
	scope := evidencevo.QueryScope{BusinessDomain: "domain_1", AccountID: "user_2", AccountType: "user"}

	response, found, err := NewWithBusinessResolver(store, resolver).GetBusinessGraphByTraceID(
		context.Background(), "trace_graph_resolver_denied", evidencevo.EvidenceQueryOptions{Scope: scope},
	)
	if err != nil || !found {
		t.Fatalf("query denied graph: found=%v err=%v", found, err)
	}
	node := businessNodeByID(t, response.Data.Nodes, "business:object:kn_demo:customer")
	if node.Display != nil {
		t.Fatalf("unresolved resolver metadata must not provide a display: %+v", node)
	}
	if response.VisibilitySummary.UnauthorizedRefCount != 0 || contains(response.PartialReasons, "business_ref_unauthorized") {
		t.Fatalf("resolver visibility must not be treated as record authorization: %+v", response)
	}
	if !contains(response.PartialReasons, "resolver_unresolved") {
		t.Fatalf("missing display metadata must remain explicit: %+v", response.PartialReasons)
	}
}

func TestBusinessGraphWithoutClaimSourceMarksContentUnavailable(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{queryTrace("trace_claim_content", "req_claim_content")}}

	response, found, err := New(store).GetBusinessGraphByTraceID(context.Background(), "trace_claim_content", evidencevo.EvidenceQueryOptions{})
	if err != nil || !found {
		t.Fatalf("query graph: found=%v err=%v", found, err)
	}
	if !contains(response.PartialReasons, "claim_content_unavailable") {
		t.Fatalf("claim type/hash without authorized source must be explicitly partial: %+v", response.PartialReasons)
	}
	claim := businessNodeByID(t, response.Data.Nodes, "claim:claim_visible")
	if claim.Display != nil || claim.Label != "" {
		t.Fatalf("claim type must not masquerade as business conclusion: %+v", claim)
	}
}

func TestBusinessGraphIgnoresUntrustedLegacyBusinessLabel(t *testing.T) {
	trace := queryTrace("trace_graph_legacy_label", "req_graph_legacy_label")
	ref := trace.Events[2].Payload["business_refs"].([]any)[0].(map[string]any)
	ref["label"] = "Customer PII Display Name"
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{trace}}

	response, found, err := New(store).GetBusinessGraphByTraceID(context.Background(), trace.TraceID, evidencevo.EvidenceQueryOptions{})
	if err != nil || !found {
		t.Fatalf("query legacy business graph: found=%v err=%v", found, err)
	}
	for _, node := range response.Data.Nodes {
		if !strings.HasPrefix(node.ID, "business:") {
			continue
		}
		if node.Label != "" {
			t.Fatalf("legacy event label must not become a display name: %+v", node)
		}
		if _, leaked := node.Properties["label"]; leaked {
			t.Fatalf("legacy event label must not leak through properties: %+v", node)
		}
	}
	if !response.Partial || !contains(response.PartialReasons, "resolver_unresolved") {
		t.Fatalf("unresolved display must remain explicit: %+v", response.PartialReasons)
	}
}

func TestGetBusinessGraphByRequestIDHandlesHiddenAndUnresolvedRefs(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{businessGraphTraceWithGovernance("trace_graph_002", "req_graph_002")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByRequestID(context.Background(), "req_graph_002", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "business_ref_unresolved") {
		t.Fatalf("expected unresolved partial reason, got: %+v", response)
	}
	if response.VisibilitySummary.HiddenRefCount != 1 || response.VisibilitySummary.UnresolvedRefCount != 1 {
		t.Fatalf("unexpected visibility summary: %+v", response.VisibilitySummary)
	}
	if len(response.Data.Nodes) != 2 {
		t.Fatalf("hidden/unresolved refs must not leak as graph nodes: %+v", response.Data.Nodes)
	}
}

func TestGetBusinessGraphByRequestIDFallsBackToAuthorizedProjection(t *testing.T) {
	trace := businessGraphTraceWithGovernance("trace_graph_projection", "req_graph_projection")
	source := &capturingProjectionSource{result: iprojectionsource.Result{
		Traces: []evidencevo.NormalizedTrace{trace},
	}}
	service := NewWithProjectionSource(&fakeStore{}, source)

	response, found, err := service.GetBusinessGraphByRequestID(
		context.Background(),
		"req_graph_projection",
		evidencevo.EvidenceQueryOptions{Scope: evidencevo.QueryScope{
			BusinessDomain: "bd_public", AccountID: "user_1", AccountType: "user",
		}},
	)

	if err != nil || !found || len(response.Data.Nodes) == 0 {
		t.Fatalf("request business graph must recover from the authorized projection: response=%+v found=%v err=%v", response, found, err)
	}
	if len(source.queries) != 1 || source.queries[0].RequestID != "req_graph_projection" {
		t.Fatalf("projection fallback must remain request-scoped: %+v", source.queries)
	}
}

func TestReceiptBusinessRefsPopulateEvidenceChainAndBusinessGraph(t *testing.T) {
	trace := evidencevo.NormalizedTrace{
		TraceID: "trace_receipt_refs", RequestID: "req_receipt_refs",
		Events: []evidencevo.EvidenceEvent{{
			EventID: "receipt:receipt_refs", EventType: "retrieval.completed",
			InteractionID: "interaction_refs", OperationID: "operation_refs",
			Payload: map[string]any{
				"status": "completed",
				"business_refs": []any{
					map[string]any{"ref_id": "kn:supplychain_hd0202", "ref_type": "knowledge_network", "visibility": "visible", "version_status": "versioned"},
					map[string]any{"ref_id": "object:supplychain_hd0202:forecast", "ref_type": "object_type", "visibility": "visible", "version_status": "versioned"},
				},
			},
		}},
	}
	source := &capturingProjectionSource{result: iprojectionsource.Result{Traces: []evidencevo.NormalizedTrace{trace}}}
	resolver := &fakeBusinessResolver{resolutions: []ibusinessresolver.Resolution{
		{RefID: "kn:supplychain_hd0202", Visibility: "visible", Display: &evidencevo.BusinessDisplay{Name: "HD供应链业务知识网络_v3", ResolutionStatus: "resolved"}},
		{RefID: "object:supplychain_hd0202:forecast", Visibility: "visible", Display: &evidencevo.BusinessDisplay{Name: "产品需求预测单", ResolutionStatus: "resolved"}},
	}}
	service := NewWithBusinessResolverAndProjectionSource(&fakeStore{}, resolver, source)
	options := evidencevo.EvidenceQueryOptions{Scope: evidencevo.QueryScope{
		BusinessDomain: "bd_public", AccountID: "user_1", AccountType: "user",
	}}

	chain, found, err := service.GetEvidenceChainByRequestID(context.Background(), trace.RequestID, options)
	if err != nil || !found || len(chain.Data.BusinessRefs) != 2 {
		t.Fatalf("receipt business refs must populate evidence chain: chain=%+v found=%v err=%v", chain, found, err)
	}
	graph, found, err := service.GetBusinessGraphByRequestID(context.Background(), trace.RequestID, options)
	if err != nil || !found {
		t.Fatalf("query receipt business graph: found=%v err=%v", found, err)
	}
	if contains(graph.PartialReasons, "missing_business_refs") {
		t.Fatalf("governed receipt refs must satisfy business evidence: %+v", graph.PartialReasons)
	}
	kn := businessNodeByID(t, graph.Data.Nodes, "business:kn:supplychain_hd0202")
	if kn.Display == nil || kn.Display.Name != "HD供应链业务知识网络_v3" {
		t.Fatalf("business resolver display must be used: %+v", kn)
	}
	if !graphHasNode(graph.Data.Nodes, "business:object:supplychain_hd0202:forecast") ||
		!graphHasEdgeType(graph.Data.Edges, "uses_business_ref") {
		t.Fatalf("receipt refs must be connected to their OpenBKN operation: %+v", graph.Data)
	}
}

func TestGetBusinessGraphSeparatesUnauthorizedAndUnresolvedRefs(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{businessGraphTraceWithUnauthorizedAndUnresolvedRefs("trace_graph_authz", "req_graph_authz")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_graph_authz", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "business_ref_unauthorized") || !contains(response.PartialReasons, "business_ref_unresolved") {
		t.Fatalf("expected unauthorized and unresolved partial reasons, got: %+v", response)
	}
	if response.VisibilitySummary.UnauthorizedRefCount != 1 || response.VisibilitySummary.UnresolvedRefCount != 1 {
		t.Fatalf("expected distinct unauthorized/unresolved counts, got %+v", response.VisibilitySummary)
	}
	if response.Page.NodeCount != 2 || response.Page.EdgeCount != 1 {
		t.Fatalf("unauthorized/unresolved refs must not leak as graph nodes or edges: page=%+v data=%+v", response.Page, response.Data)
	}
}

func TestGetBusinessGraphDoesNotDependOnEventOrder(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{businessGraphTraceWithBusinessBeforeClaim("trace_graph_003", "req_graph_003")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_graph_003", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if contains(response.PartialReasons, "missing_claim") {
		t.Fatalf("business graph must collect claims before linking refs: %+v", response)
	}
	if response.Page.NodeCount != 2 || response.Page.EdgeCount != 1 {
		t.Fatalf("unexpected page: %+v", response.Page)
	}
}

func TestGetBusinessGraphDeduplicatesEdgesAndAuthorizedRefs(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{businessGraphTraceWithDuplicateRefs("trace_graph_004", "req_graph_004")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_graph_004", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if response.Page.EdgeCount != 1 || len(response.Data.Edges) != 1 {
		t.Fatalf("expected duplicate edges to be collapsed, got page=%+v edges=%+v", response.Page, response.Data.Edges)
	}
	if response.VisibilitySummary.AuthorizedRefCount != 1 {
		t.Fatalf("expected duplicate refs to count once, got %+v", response.VisibilitySummary)
	}
}

func TestGetBusinessGraphDoesNotLeakHiddenClaimThroughSyntheticNode(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{businessGraphTraceWithHiddenClaim("trace_graph_005", "req_graph_005")}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_graph_005", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "hidden_claim") {
		t.Fatalf("expected hidden claim partial, got %+v", response)
	}
	if response.Page.NodeCount != 0 || response.Page.EdgeCount != 0 {
		t.Fatalf("hidden claim must not leak through nodes or edges: %+v", response)
	}
	if response.VisibilitySummary.HiddenRefCount != 1 || response.VisibilitySummary.OmittedRefCount != 1 {
		t.Fatalf("expected hidden claim and omitted business ref counts, got %+v", response.VisibilitySummary)
	}
}

func TestGetBusinessGraphReturnsMissingBusinessRefsPartial(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{{
		TraceID:   "trace_no_business_refs",
		RequestID: "req_no_business_refs",
		Events: []evidencevo.EvidenceEvent{
			{
				EventType: "claim.created",
				Payload: map[string]any{
					"claim_id":       "claim_no_business_refs",
					"claim_type":     "answer",
					"claim_hash":     "sha256:claim",
					"visibility":     "visible",
					"version_status": "versioned",
				},
			},
		},
	}}}
	service := New(store)

	response, found, err := service.GetBusinessGraphByTraceID(context.Background(), "trace_no_business_refs", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected business graph to be found")
	}
	if !response.Partial || !contains(response.PartialReasons, "missing_business_refs") {
		t.Fatalf("expected missing business refs partial, got: %+v", response)
	}
}

func TestGetBusinessGraphProjectsFiveStageBusinessStory(t *testing.T) {
	trace := evidencevo.NormalizedTrace{
		TraceID: "trace_business_story", RequestID: "req_business_story",
		Events: []evidencevo.EvidenceEvent{
			{EventID: "evt_intent", EventType: "agent.interaction.started", InteractionID: "interaction_1", Payload: map[string]any{"intent_hash": "sha256:intent", "mode": "task", "agent_id": "agent_1"}},
			{EventID: "evt_data", EventType: "data.query.observed", InteractionID: "interaction_1", OperationID: "operation_data", CausationID: "evt_intent", Payload: map[string]any{"query_hash": "sha256:query", "query_type": "aggregate", "row_count": 3, "truncated": false, "version_status": "versioned"}},
			{EventID: "evt_claim", EventType: "claim.created", InteractionID: "interaction_1", CausationID: "evt_data", ClaimID: "claim_1", Payload: map[string]any{"claim_id": "claim_1", "claim_type": "answer", "claim_hash": "sha256:claim", "source_event_ids": []any{"evt_data"}, "operation_ids": []any{"operation_data"}, "visibility": "visible", "version_status": "versioned"}},
			{EventID: "evt_evidence", EventType: "evidence.refs.created", InteractionID: "interaction_1", OperationID: "operation_data", CausationID: "evt_claim", ClaimID: "claim_1", Payload: map[string]any{"claim_id": "claim_1", "evidence_refs": []any{map[string]any{"ref_id": "resource:sales", "ref_type": "data_resource", "source_system": "vega", "validity": "observed", "version_status": "versioned", "visibility": "visible"}}}},
			{EventID: "evt_business", EventType: "business.refs.resolved", InteractionID: "interaction_1", OperationID: "operation_data", CausationID: "evt_evidence", ClaimID: "claim_1", Payload: map[string]any{"claim_id": "claim_1", "resolver_status": "resolved", "business_refs": []any{map[string]any{"ref_id": "object:kn_demo:sales_order", "ref_type": "object", "source_system": "bkn", "validity": "available", "version_status": "versioned", "visibility": "visible"}}}},
			{EventID: "evt_recommended", EventType: "action.recommended", InteractionID: "interaction_1", OperationID: "operation_action", CausationID: "evt_claim", ClaimID: "claim_1", Payload: map[string]any{"action_instance_id": "action_1", "action_type": "create_task", "target_refs": []any{"object:kn_demo:sales_order"}, "reason_hash": "sha256:reason", "status": "recommended"}},
			{EventID: "evt_requested", EventType: "action.approval_requested", InteractionID: "interaction_1", OperationID: "operation_action", CausationID: "evt_recommended", ClaimID: "claim_1", Payload: map[string]any{"action_instance_id": "action_1", "policy_ref": "policy:review", "status": "approval_requested"}},
			{EventID: "evt_approved", EventType: "action.approved", InteractionID: "interaction_1", OperationID: "operation_action", CausationID: "evt_requested", ClaimID: "claim_1", Payload: map[string]any{"action_instance_id": "action_1", "actor_ref": "account:reviewer", "policy_decision_ref": "decision:1", "status": "approved"}},
			{EventID: "evt_executed", EventType: "action.executed", InteractionID: "interaction_1", OperationID: "operation_action", CausationID: "evt_approved", ClaimID: "claim_1", Payload: map[string]any{"action_instance_id": "action_1", "tool_ref": "tool:create_task", "status": "ok"}},
			{EventID: "evt_result", EventType: "action.result_recorded", InteractionID: "interaction_1", OperationID: "operation_action", CausationID: "evt_executed", ClaimID: "claim_1", Payload: map[string]any{"action_instance_id": "action_1", "result_hash": "sha256:result", "task_ref": "task:1", "status": "created"}},
		},
	}
	response, found, err := New(&fakeStore{traces: []evidencevo.NormalizedTrace{trace}}).GetBusinessGraphByTraceID(context.Background(), trace.TraceID, evidencevo.EvidenceQueryOptions{})
	if err != nil || !found {
		t.Fatalf("query failed: found=%v err=%v", found, err)
	}
	wantNodes := []string{"interaction:interaction_1", "event:evt_data", "evidence:resource:sales", "business:object:kn_demo:sales_order", "claim:claim_1", "action:action_1:recommended", "action:action_1:result_recorded"}
	for _, id := range wantNodes {
		if !graphHasNode(response.Data.Nodes, id) {
			t.Fatalf("missing five-stage node %s: %+v", id, response.Data.Nodes)
		}
	}
	wantEdges := []string{"caused_by", "supports", "uses_business_ref", "recommends", "transitions_to"}
	for _, edgeType := range wantEdges {
		if !graphHasEdgeType(response.Data.Edges, edgeType) {
			t.Fatalf("missing semantic edge %s: %+v", edgeType, response.Data.Edges)
		}
	}

	trace.Events[2].Payload["visibility"] = "hidden"
	hidden, found, err := New(&fakeStore{traces: []evidencevo.NormalizedTrace{trace}}).GetBusinessGraphByTraceID(context.Background(), trace.TraceID, evidencevo.EvidenceQueryOptions{})
	if err != nil || !found {
		t.Fatalf("hidden claim query failed: found=%v err=%v", found, err)
	}
	for _, node := range hidden.Data.Nodes {
		if node.ClaimID == "claim_1" || strings.HasPrefix(node.ID, "claim:claim_1") || strings.HasPrefix(node.ID, "action:action_1") {
			t.Fatalf("hidden claim leaked through node: %+v", node)
		}
	}
	for _, edge := range hidden.Data.Edges {
		if strings.HasPrefix(edge.SourceID, "claim:claim_1") || strings.HasPrefix(edge.TargetID, "claim:claim_1") || strings.HasPrefix(edge.SourceID, "action:action_1") || strings.HasPrefix(edge.TargetID, "action:action_1") {
			t.Fatalf("hidden claim leaked through edge: %+v", edge)
		}
	}
}

func graphHasNode(nodes []evidencevo.BusinessGraphNode, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func businessNodeByID(t *testing.T, nodes []evidencevo.BusinessGraphNode, id string) evidencevo.BusinessGraphNode {
	t.Helper()
	for _, node := range nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("business graph node %s not found: %+v", id, nodes)
	return evidencevo.BusinessGraphNode{}
}

func graphHasEdgeType(edges []evidencevo.BusinessGraphEdge, edgeType string) bool {
	for _, edge := range edges {
		if edge.EdgeType == edgeType {
			return true
		}
	}
	return false
}

func TestGetSnapshotPreviewByTraceIDReturnsGovernedManifest(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{queryTrace("trace_snapshot_001", "req_snapshot_001")}}
	service := New(store)

	response, found, err := service.GetSnapshotPreviewByTraceID(context.Background(), "trace_snapshot_001", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected snapshot preview to be found")
	}
	if response.TraceID != "trace_snapshot_001" || response.RequestID != "req_snapshot_001" {
		t.Fatalf("unexpected identity: %+v", response)
	}
	if response.SnapshotRef.Mode != "preview" || response.SnapshotRef.URI != "" {
		t.Fatalf("preview must not expose a persisted object storage uri: %+v", response.SnapshotRef)
	}
	if response.Manifest.ArtifactCount != 3 || response.Manifest.ClaimCount != 1 || response.Manifest.EvidenceRefCount != 1 || response.Manifest.BusinessRefCount != 1 {
		t.Fatalf("unexpected manifest counts: %+v", response.Manifest)
	}
	if response.Manifest.ComplianceStatus != "preview/non-production compliance" || response.Manifest.DLPClassification != "metadata-only" {
		t.Fatalf("unexpected governance fields: %+v", response.Manifest)
	}
	if !strings.HasPrefix(response.Manifest.ArtifactHash, "sha256:") || !strings.HasPrefix(response.Manifest.ManifestHash, "sha256:") {
		t.Fatalf("expected sha256 hashes in manifest: %+v", response.Manifest)
	}
	if response.VisibilitySummary.HiddenRefCount != 1 || response.VisibilitySummary.AuthorizedRefCount != 2 {
		t.Fatalf("unexpected visibility summary: %+v", response.VisibilitySummary)
	}
}

func TestGetEvidenceChainByTraceIDNotFound(t *testing.T) {
	service := New(&fakeStore{})

	_, found, err := service.GetEvidenceChainByTraceID(context.Background(), "missing", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected not found")
	}
}

func TestGetEvidenceNodeByTraceIDReturnsVisibleClaim(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{queryTrace("trace_node_001", "req_node_001")}}
	service := New(store)

	response, found, err := service.GetEvidenceNodeByTraceID(context.Background(), "trace_node_001", "claim:claim_visible", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected evidence node to be found")
	}
	if response.NodeID != "claim:claim_visible" || response.NodeType != "claim" || response.TraceID != "trace_node_001" {
		t.Fatalf("unexpected node response: %+v", response)
	}
	if response.Data["claim_id"] != "claim_visible" || response.Visibility != "visible" {
		t.Fatalf("unexpected node data: %+v", response)
	}
}

func TestGetEvidenceNodeByRequestIDDoesNotReturnHiddenRef(t *testing.T) {
	store := &fakeStore{traces: []evidencevo.NormalizedTrace{queryTrace("trace_node_002", "req_node_002")}}
	service := New(store)

	_, found, err := service.GetEvidenceNodeByRequestID(context.Background(), "req_node_002", "evidence_ref:row:hidden", evidencevo.EvidenceQueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("hidden evidence ref must not be returned as node detail")
	}
}

func hasValidationCode(validationErrors evidencevo.ValidationErrors, code string) bool {
	for _, validationError := range validationErrors {
		if validationError.Code == code {
			return true
		}
	}
	return false
}

func hasValidationPath(validationErrors evidencevo.ValidationErrors, field string) bool {
	for _, validationError := range validationErrors {
		if strings.HasSuffix(validationError.Path, "."+field) {
			return true
		}
	}
	return false
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test fixture: %v", err)
	}
	return body
}

func twoPointOneBatch(events []map[string]any) map[string]any {
	return map[string]any{
		"bkn.trace.schema.version": "2.1.0",
		"trace": map[string]any{
			"trace_id":            "11111111111111111111111111111111",
			"traceparent":         "00-11111111111111111111111111111111-1000000000000001-01",
			"bkn.request.id":      "req_biz_001",
			"bkn.conversation.id": "agent:thread_supply_chain",
			"bkn.tenant.id":       "tenant_e2e",
			"business_domain":     "supplychain_e2e",
			"bkn.account.id":      "account_e2e_admin",
			"bkn.account.type":    "user",
		},
		"events": events,
	}
}

func batchWithPayloadField(t *testing.T, key string, value any) map[string]any {
	t.Helper()
	batch := twoPointOneBatch(validTwoPointOneEvents())
	batch["events"].([]map[string]any)[1]["payload"].(map[string]any)[key] = value
	return batch
}

func validTwoPointOneEvents() []map[string]any {
	traceFields := func(eventID, eventType, operationName string, payload map[string]any) map[string]any {
		return map[string]any{
			"event_id": eventID, "event_type": eventType, "bkn.trace.schema.version": "2.1.0",
			"observed_at": "2026-07-25T08:00:00.000000000Z", "emitted_at": "2026-07-25T08:00:00.001000000Z",
			"producer_module": "bkn-agent", "trace_id": "11111111111111111111111111111111",
			"span_id": "1000000000000001", "bkn.request.id": "req_biz_001", "bkn.operation.name": operationName,
			"interaction_id": "int_001", "attempt": 1, "payload": payload,
		}
	}
	interaction := traceFields("evt_interaction", "agent.interaction.started", "agent.run", map[string]any{
		"intent_hash": testHash("1"), "mode": "task", "agent_id": "supplychain-assistant",
	})
	dataQuery := traceFields("evt_data", "data.query.observed", "data.query", map[string]any{
		"query_hash": testHash("2"), "query_type": "aggregate", "row_count": 12,
		"resource_refs": []any{"resource:forecast"}, "field_refs": []any{"field:forecast:forecast_month"},
		"truncated": false, "version_status": "versioned",
	})
	dataQuery["operation_id"] = "op_data_001"
	dataQuery["causation_event_id"] = "evt_interaction"
	retrieval := traceFields("evt_retrieval", "retrieval.completed", "retrieval.search", map[string]any{
		"query_hash": testHash("3"), "candidate_count": 3, "truncated": false, "source_refs": []any{"schema:forecast"}, "version_status": "versioned",
	})
	retrieval["operation_id"] = "op_retrieval_001"
	retrieval["causation_event_id"] = "evt_interaction"
	knowledge := traceFields("evt_knowledge", "knowledge.read.observed", "knowledge.read", map[string]any{
		"kn_id": "supplychain", "read_kind": "object_relation_schema", "version_status": "versioned", "business_refs": []any{"object:supplychain:forecast"},
	})
	knowledge["operation_id"] = "op_knowledge_001"
	knowledge["causation_event_id"] = "evt_retrieval"
	model := traceFields("evt_model", "model.call.observed", "model.chat", map[string]any{
		"model_name": "test-model", "model_provider": "openai-compatible", "status": "ok",
		"input_token_count": 10, "output_token_count": 5, "prompt_hash": testHash("4"), "output_hash": testHash("5"),
	})
	model["operation_id"] = "op_model_001"
	model["causation_event_id"] = "evt_data"
	claim := traceFields("evt_claim", "claim.created", "agent.claim.create", map[string]any{
		"claim_id": "claim_001", "claim_type": "answer", "claim_hash": testHash("6"),
		"source_event_ids": []any{"evt_data", "evt_model"}, "operation_ids": []any{"op_data_001", "op_model_001"},
		"version_status": "versioned", "visibility": "visible",
	})
	claim["causation_event_id"] = "evt_model"
	claim["claim_id"] = "claim_001"
	evidenceRefs := traceFields("evt_evidence", "evidence.refs.created", "agent.evidence.link", map[string]any{
		"claim_id": "claim_001", "evidence_refs": []any{map[string]any{
			"ref_id": "resource:forecast", "ref_type": "data_resource", "source_system": "vega",
			"validity": "observed", "version_status": "versioned", "visibility": "visible",
		}},
	})
	evidenceRefs["operation_id"] = "op_data_001"
	evidenceRefs["causation_event_id"] = "evt_claim"
	evidenceRefs["claim_id"] = "claim_001"
	businessRefs := traceFields("evt_business", "business.refs.resolved", "agent.business.resolve", map[string]any{
		"claim_id": "claim_001", "resolver_status": "resolved", "business_refs": []any{map[string]any{
			"ref_id": "object:supplychain:forecast", "ref_type": "object", "source_system": "bkn",
			"validity": "available", "version_status": "versioned", "visibility": "visible",
		}},
	})
	businessRefs["operation_id"] = "op_knowledge_001"
	businessRefs["causation_event_id"] = "evt_claim"
	businessRefs["claim_id"] = "claim_001"
	action := func(id, kind, cause string, payload map[string]any) map[string]any {
		event := traceFields(id, kind, kind, payload)
		event["operation_id"] = "op_action_001"
		event["causation_event_id"] = cause
		event["claim_id"] = "claim_001"
		return event
	}
	recommended := action("evt_action_recommended", "action.recommended", "evt_claim", map[string]any{
		"action_instance_id": "action_001", "action_type": "create_forecast_monitor", "target_refs": []any{"monitor:forecast"}, "reason_hash": testHash("9"), "status": "recommended",
	})
	requested := action("evt_action_requested", "action.approval_requested", "evt_action_recommended", map[string]any{
		"action_instance_id": "action_001", "policy_ref": "policy:monitor", "status": "approval_requested",
	})
	approved := action("evt_action_approved", "action.approved", "evt_action_requested", map[string]any{
		"action_instance_id": "action_001", "actor_ref": "account:admin", "policy_decision_ref": "decision:allow", "status": "approved",
	})
	executed := action("evt_action_executed", "action.executed", "evt_action_approved", map[string]any{
		"action_instance_id": "action_001", "invocation_ref": "tool:create_monitor", "status": "ok",
	})
	result := action("evt_action_result", "action.result_recorded", "evt_action_executed", map[string]any{
		"action_instance_id": "action_001", "result_hash": testHash("a"), "task_ref": "monitor:001", "status": "created",
	})
	return []map[string]any{interaction, dataQuery, retrieval, knowledge, model, claim, evidenceRefs, businessRefs, recommended, requested, approved, executed, result}
}

func testHash(hexDigit string) string {
	return "sha256:" + strings.Repeat(hexDigit, 64)
}

func removeEventTypes(events []map[string]any, eventTypes ...string) []map[string]any {
	removed := map[string]struct{}{}
	for _, eventType := range eventTypes {
		removed[eventType] = struct{}{}
	}
	result := make([]map[string]any, 0, len(events))
	for _, event := range events {
		if _, ok := removed[event["event_type"].(string)]; !ok {
			result = append(result, event)
		}
	}
	return result
}

func cloneEventByType(t *testing.T, events []map[string]any, eventType string) map[string]any {
	t.Helper()
	for _, event := range events {
		if event["event_type"] == eventType {
			body := mustJSON(t, event)
			var clone map[string]any
			if err := json.Unmarshal(body, &clone); err != nil {
				t.Fatalf("clone event: %v", err)
			}
			return clone
		}
	}
	t.Fatalf("event type %s not found", eventType)
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func queryTrace(traceID, requestID string) evidencevo.NormalizedTrace {
	return evidencevo.NormalizedTrace{
		TraceID:   traceID,
		RequestID: requestID,
		Events: []evidencevo.EvidenceEvent{
			{
				EventType: "claim.created",
				Payload: map[string]any{
					"claim_id":       "claim_visible",
					"claim_type":     "finding",
					"claim_hash":     "sha256:claim",
					"visibility":     "visible",
					"version_status": "versioned",
				},
			},
			{
				EventType: "evidence.refs.created",
				Payload: map[string]any{
					"claim_id": "claim_visible",
					"evidence_refs": []any{
						map[string]any{"ref_id": "row:visible", "ref_type": "row_ref", "visibility": "visible"},
						map[string]any{"ref_id": "row:hidden", "ref_type": "row_ref", "visibility": "hidden"},
					},
				},
			},
			{
				EventType: "business.refs.resolved",
				Payload: map[string]any{
					"claim_id":      "claim_visible",
					"business_refs": []any{map[string]any{"ref_id": "object:kn_demo:customer", "ref_type": "object", "visibility": "visible", "version_status": "versioned"}},
				},
			},
		},
	}
}

func evidenceChainTraceWithUnauthorizedRef(traceID, requestID string) evidencevo.NormalizedTrace {
	trace := queryTrace(traceID, requestID)
	trace.Events[1].Payload["evidence_refs"] = []any{
		map[string]any{"ref_id": "row:visible", "ref_type": "row_ref", "visibility": "visible"},
		map[string]any{"ref_id": "row:unauthorized", "ref_type": "row_ref", "visibility": "unauthorized", "policy_decision_ref": "policy:deny:2", "redaction_reason": "row_scope_denied"},
	}
	return trace
}

func businessGraphTraceWithGovernance(traceID, requestID string) evidencevo.NormalizedTrace {
	trace := queryTrace(traceID, requestID)
	trace.Events[2].Payload["business_refs"] = []any{
		map[string]any{"ref_id": "object:kn_demo:customer", "ref_type": "object", "visibility": "visible", "version_status": "versioned"},
		map[string]any{"ref_id": "object:kn_demo:hidden", "ref_type": "object", "visibility": "hidden"},
		map[string]any{"ref_id": "object:kn_demo:deleted", "ref_type": "object", "visibility": "unresolved"},
	}
	return trace
}

func businessGraphTraceWithUnauthorizedAndUnresolvedRefs(traceID, requestID string) evidencevo.NormalizedTrace {
	trace := queryTrace(traceID, requestID)
	trace.Events[2].Payload["business_refs"] = []any{
		map[string]any{"ref_id": "object:kn_demo:customer", "ref_type": "object", "visibility": "visible", "version_status": "versioned"},
		map[string]any{"ref_id": "object:kn_demo:unauthorized", "ref_type": "object", "visibility": "unauthorized", "policy_decision_ref": "policy:deny:1", "redaction_reason": "tenant_scope_denied"},
		map[string]any{"ref_id": "object:kn_demo:unresolved", "ref_type": "object", "visibility": "unresolved", "failure_status": "resolver_not_found"},
	}
	return trace
}

func businessGraphTraceWithBusinessBeforeClaim(traceID, requestID string) evidencevo.NormalizedTrace {
	trace := queryTrace(traceID, requestID)
	trace.Events[0], trace.Events[2] = trace.Events[2], trace.Events[0]
	return trace
}

func businessGraphTraceWithDuplicateRefs(traceID, requestID string) evidencevo.NormalizedTrace {
	trace := queryTrace(traceID, requestID)
	trace.Events[2].Payload["business_refs"] = []any{
		map[string]any{"ref_id": "object:kn_demo:customer", "ref_type": "object", "visibility": "visible", "version_status": "versioned"},
		map[string]any{"ref_id": "object:kn_demo:customer", "ref_type": "object", "visibility": "visible", "version_status": "versioned"},
	}
	trace.Events = append(trace.Events, trace.Events[2])
	return trace
}

func businessGraphTraceWithHiddenClaim(traceID, requestID string) evidencevo.NormalizedTrace {
	trace := queryTrace(traceID, requestID)
	trace.Events[0].Payload["visibility"] = "hidden"
	return trace
}

func validBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000001",
    "bkn.request.id": "req_phase2_001",
    "traceparent": "00-8c0d0000000000000000000000000001-1f12000000000001-01",
    "bkn.tenant.id": "tenant_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_claim",
      "event_type": "claim.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "8c0d0000000000000000000000000001",
      "span_id": "1f12000000000001",
      "bkn.request.id": "req_phase2_001",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_001",
        "claim_type": "answer",
        "claim_hash": "sha256:claim",
        "visibility": "visible",
        "version_status": "versioned"
      }
    },
    {
      "event_id": "evt_evidence",
      "event_type": "evidence.refs.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.002000000Z",
      "emitted_at": "2026-07-22T04:00:00.003000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "8c0d0000000000000000000000000001",
      "span_id": "1f12000000000001",
      "bkn.request.id": "req_phase2_001",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_001",
        "evidence_refs": [{"ref_id": "eref_001"}]
      }
    },
    {
      "event_id": "evt_business",
      "event_type": "business.refs.resolved",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.004000000Z",
      "emitted_at": "2026-07-22T04:00:00.005000000Z",
      "producer_module": "bkn-trace",
      "trace_id": "8c0d0000000000000000000000000001",
      "span_id": "1f12000000000001",
      "bkn.request.id": "req_phase2_001",
      "bkn.operation.name": "bkn_trace.resolve_business_refs",
      "payload": {
        "claim_id": "claim_001",
        "business_refs": [{"ref_id": "bref_001"}]
      }
    }
  ]
}`
}

func missingClaimIDBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000002",
    "bkn.request.id": "req_phase2_002",
    "traceparent": "00-8c0d0000000000000000000000000002-1f12000000000002-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_claim_missing",
      "event_type": "claim.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "8c0d0000000000000000000000000002",
      "span_id": "1f12000000000002",
      "bkn.request.id": "req_phase2_002",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_type": "answer",
        "claim_hash": "sha256:claim",
        "visibility": "visible",
        "version_status": "versioned"
      }
    }
  ]
}`
}

func toolEventsBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000006",
    "bkn.request.id": "req_phase2_tool_006",
    "traceparent": "00-8c0d0000000000000000000000000006-1f12000000000006-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_tool_called",
      "event_type": "tool.called",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "bkn-agent",
      "trace_id": "8c0d0000000000000000000000000006",
      "span_id": "1f12000000000006",
      "bkn.request.id": "req_phase2_tool_006",
      "bkn.operation.name": "bkn.agent.tool.call",
      "payload": {
        "tool_id": "tool_query_object",
        "tool_name": "query_object_instance",
        "toolbox_id": "box_contextloader",
        "args_hash": "sha256:args",
        "visibility": "visible",
        "version_status": "unversioned"
      }
    },
    {
      "event_id": "evt_tool_result",
      "event_type": "tool.result.observed",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.002000000Z",
      "emitted_at": "2026-07-22T04:00:00.003000000Z",
      "producer_module": "bkn-agent",
      "trace_id": "8c0d0000000000000000000000000006",
      "span_id": "1f12000000000006",
      "bkn.request.id": "req_phase2_tool_006",
      "bkn.operation.name": "bkn.agent.tool.call",
      "payload": {
        "tool_id": "tool_query_object",
        "tool_name": "query_object_instance",
        "toolbox_id": "box_contextloader",
        "result_hash": "sha256:result",
        "result_length": 123,
        "status": "success",
        "visibility": "visible",
        "version_status": "unversioned"
      }
    }
  ]
}`
}

func sensitiveBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000003",
    "bkn.request.id": "req_phase2_003",
    "traceparent": "00-8c0d0000000000000000000000000003-1f12000000000003-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_sensitive",
      "event_type": "evidence.refs.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "vega-data",
      "trace_id": "8c0d0000000000000000000000000003",
      "span_id": "1f12000000000003",
      "bkn.request.id": "req_phase2_003",
      "bkn.operation.name": "data.query.execute",
      "payload": {
        "claim_id": "claim_003",
        "evidence_refs": [{"ref_id": "eref_003"}],
        "raw_sql": "select email from customer"
      }
    }
  ]
}`
}

func unknownClaimIDBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000004",
    "bkn.request.id": "req_phase2_004",
    "traceparent": "00-8c0d0000000000000000000000000004-1f12000000000004-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_unknown_claim",
      "event_type": "evidence.refs.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "8c0d0000000000000000000000000004",
      "span_id": "1f12000000000004",
      "bkn.request.id": "req_phase2_004",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_DOES_NOT_EXIST",
        "evidence_refs": [{"ref_id": "eref_004"}]
      }
    }
  ]
}`
}

func rawToolPayloadBatch() string {
	return strings.Replace(toolEventsBatch(), `"result_hash": "sha256:result",`, `"raw_tool_result": "customer email is alice@example.com",`, 1)
}

func emptyEventsBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "8c0d0000000000000000000000000005",
    "bkn.request.id": "req_phase2_005",
    "traceparent": "00-8c0d0000000000000000000000000005-1f12000000000005-01",
    "business_domain": "bd_demo",
    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": []
}`
}
