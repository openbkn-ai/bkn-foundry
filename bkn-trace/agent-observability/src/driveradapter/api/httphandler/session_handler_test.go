package httphandler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/assemblysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
)

func TestInteractionBusinessGraphReturnsAuthorizedSemanticViewWithoutLeaseSecrets(t *testing.T) {
	t.Parallel()

	sessions := sessionstore.New()
	lifecycle := sessionsvc.New(sessions, sessionsvc.Options{})
	ledger := ledgerstore.New()
	owner := sessionvo.Owner{
		TenantID: "tenant-1", BusinessDomainID: "domain-1", ApplicationPrincipalID: "app-1",
		EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user-1",
	}
	conversation, err := lifecycle.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{
		Owner: owner, ExternalConversationKey: "thread", IdempotencyKey: "conv",
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	interaction, err := lifecycle.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{
		Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "int",
	})
	if err != nil {
		t.Fatalf("start interaction: %v", err)
	}
	envelope := json.RawMessage(`{"result":"ok"}`)
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	_, err = ledger.Commit(context.Background(), ledgervo.Event{
		EventID: "evt-http-graph", EventType: "operation.output.observed", SchemaVersion: "3.0.0",
		PayloadHash: ledgervo.CanonicalPayloadHash(envelope), Owner: owner,
		ConversationID: conversation.ID, InteractionID: interaction.ID,
		ProducerID: "agent", ProducerStreamID: "stream", ProducerEpoch: 1, ProducerSequence: 1,
		StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: envelope,
	})
	if err != nil {
		t.Fatalf("commit event: %v", err)
	}
	handler := httphandler.NewSessionHandlerWithAssembly(lifecycle, assemblysvc.NewQueryService(sessions, ledger))
	request := httptest.NewRequest(http.MethodGet,
		"/api/agent-observability/v1/interactions/"+interaction.ID+"/business-graph", nil)
	setTrustedOwnerHeaders(request)
	response := httptest.NewRecorder()

	handler.GetInteractionBusinessGraph(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "evt-http-graph") {
		t.Fatalf("unexpected business graph response %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "lease_token") {
		t.Fatalf("business graph leaked lifecycle lease secret: %s", response.Body.String())
	}
}

func TestEnsureConversationRejectsBodyIdentityFields(t *testing.T) {
	t.Parallel()

	handler := httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{}))
	body := []byte(`{
		"external_conversation_key":"cursor-thread-42",
		"idempotency_key":"ensure-42",
		"tenant_id":"forged-tenant",
		"effective_subject_id":"forged-user"
	}`)
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", bytes.NewReader(body))
	setTrustedOwnerHeaders(request)
	response := httptest.NewRecorder()

	handler.EnsureCurrentConversation(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
	var envelope lifecycleTestErrorEnvelope
	decodeLifecycleResponse(t, response, &envelope)
	if envelope.Error.Code != "conversation_required" {
		t.Fatalf("unexpected identity-field rejection: %#v", envelope.Error)
	}
}

func TestLifecycleRequestWithoutTrustedOwnerIsRejectedWithGuidance(t *testing.T) {
	t.Parallel()

	handler := httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{}))
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current",
		bytes.NewReader([]byte(`{"external_conversation_key":"thread"}`)))
	response := httptest.NewRecorder()

	handler.EnsureCurrentConversation(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Error struct {
			Code           string `json:"code"`
			RequiredAction string `json:"required_action"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if envelope.Error.Code != "permission_denied" || envelope.Error.RequiredAction != "request_authorization" {
		t.Fatalf("unexpected guided error: %#v", envelope.Error)
	}
}

func TestLifecycleValueValidationReturnsRegisteredGuidedError(t *testing.T) {
	t.Parallel()

	handler := httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{}))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/agent-observability/v1/conversations:ensure-current",
		bytes.NewReader([]byte(`{"external_conversation_key":""}`)),
	)
	setTrustedOwnerHeaders(request)
	response := httptest.NewRecorder()

	handler.EnsureCurrentConversation(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
	var envelope lifecycleTestErrorEnvelope
	decodeLifecycleResponse(t, response, &envelope)
	if envelope.Error.Code != "conversation_required" ||
		envelope.Error.RequiredAction != "bkn_start_interaction" {
		t.Fatalf("unexpected guided error: %#v", envelope.Error)
	}
}

func TestManagedLifecycleHTTPWorkflow(t *testing.T) {
	t.Parallel()

	handler := httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{}))
	mux := http.NewServeMux()
	httphandler.RegisterSessionRoutes(mux, "/api/agent-observability/v1", handler)

	conversationResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations:ensure-current",
		`{"external_conversation_key":"agent-thread-1","idempotency_key":"ensure-1"}`)
	if conversationResponse.Code != http.StatusCreated {
		t.Fatalf("ensure conversation: %d %s", conversationResponse.Code, conversationResponse.Body.String())
	}
	var conversation sessionvo.Conversation
	decodeLifecycleResponse(t, conversationResponse, &conversation)

	interactionResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations/"+conversation.ID+"/interactions",
		`{"idempotency_key":"interaction-1","lease_seconds":300}`)
	if interactionResponse.Code != http.StatusCreated {
		t.Fatalf("start interaction: %d %s", interactionResponse.Code, interactionResponse.Body.String())
	}
	var interaction sessionvo.Interaction
	decodeLifecycleResponse(t, interactionResponse, &interaction)

	operationResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations/"+conversation.ID+"/interactions/"+interaction.ID+"/operations:ensure",
		`{"operation_key":"query-orders","tool_name":"ontology-query","normalized_input_hash":"sha256:input","required":true,"lease_token":"`+
			interaction.LeaseToken+`","lease_epoch":1}`)
	if operationResponse.Code != http.StatusCreated {
		t.Fatalf("ensure operation: %d %s", operationResponse.Code, operationResponse.Body.String())
	}
	var operationResult struct {
		Operation sessionvo.Operation `json:"operation"`
		Receipt   sessionvo.Receipt   `json:"receipt"`
		Created   bool                `json:"created"`
		Execute   bool                `json:"execute"`
	}
	decodeLifecycleResponse(t, operationResponse, &operationResult)
	if !operationResult.Created || !operationResult.Execute {
		t.Fatal("new operation must report created=true and execute=true")
	}

	receiptResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/operations/"+operationResult.Operation.ID+"/attempts/1:complete",
		`{"receipt_id":"`+operationResult.Receipt.ID+`","payload_hash":"sha256:result","evidence_durability":"durable","request_id":"req-1","trace_id":"4b3d59daeff5bfbb23d46c47a5051ec9"}`)
	if receiptResponse.Code != http.StatusOK {
		t.Fatalf("complete receipt: %d %s", receiptResponse.Code, receiptResponse.Body.String())
	}

	retryOperationResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations/"+conversation.ID+"/interactions/"+interaction.ID+"/operations:ensure",
		`{"operation_key":"retry-orders","tool_name":"ontology-query","normalized_input_hash":"sha256:retry-input","required":true,"lease_token":"`+
			interaction.LeaseToken+`","lease_epoch":1}`)
	var retryOperationResult struct {
		Operation sessionvo.Operation `json:"operation"`
		Receipt   sessionvo.Receipt   `json:"receipt"`
	}
	decodeLifecycleResponse(t, retryOperationResponse, &retryOperationResult)
	failResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/operations/"+retryOperationResult.Operation.ID+"/attempts/1:fail",
		`{"receipt_id":"`+retryOperationResult.Receipt.ID+`","payload_hash":"sha256:failure","evidence_durability":"failed","retryable":true,"request_id":"req-retry","trace_id":"4b3d59daeff5bfbb23d46c47a5051ec9"}`)
	if failResponse.Code != http.StatusOK {
		t.Fatalf("fail retryable attempt: %d %s", failResponse.Code, failResponse.Body.String())
	}
	retryResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/operations/"+retryOperationResult.Operation.ID+"/attempts",
		`{"lease_token":"`+interaction.LeaseToken+`","lease_epoch":1}`)
	if retryResponse.Code != http.StatusCreated {
		t.Fatalf("start retry attempt: %d %s", retryResponse.Code, retryResponse.Body.String())
	}
	var retryResult struct {
		Operation sessionvo.Operation `json:"operation"`
		Receipt   sessionvo.Receipt   `json:"receipt"`
		Created   bool                `json:"created"`
		Execute   bool                `json:"execute"`
	}
	decodeLifecycleResponse(t, retryResponse, &retryResult)
	if retryResult.Created || retryResult.Execute ||
		retryResult.Operation.AttemptStatus != sessionvo.AttemptReady {
		t.Fatalf("new attempt must be prepared but not claimed: %#v", retryResult)
	}
	claimRetryResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations/"+conversation.ID+"/interactions/"+interaction.ID+"/operations:ensure",
		`{"operation_key":"retry-orders","tool_name":"ontology-query","normalized_input_hash":"sha256:retry-input","required":true,"lease_token":"`+
			interaction.LeaseToken+`","lease_epoch":1}`)
	var claimedRetry struct {
		Operation sessionvo.Operation `json:"operation"`
		Receipt   sessionvo.Receipt   `json:"receipt"`
		Created   bool                `json:"created"`
		Execute   bool                `json:"execute"`
	}
	decodeLifecycleResponse(t, claimRetryResponse, &claimedRetry)
	if claimedRetry.Created || !claimedRetry.Execute ||
		claimedRetry.Operation.AttemptStatus != sessionvo.AttemptPending {
		t.Fatalf("retry ensure must claim the prepared attempt: %#v", claimedRetry)
	}
	retryCompleteResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/operations/"+claimedRetry.Operation.ID+"/attempts/2:complete",
		`{"receipt_id":"`+claimedRetry.Receipt.ID+`","payload_hash":"sha256:retry-result","evidence_durability":"durable","request_id":"req-retry-complete","trace_id":"4b3d59daeff5bfbb23d46c47a5051ec9"}`)
	if retryCompleteResponse.Code != http.StatusOK {
		t.Fatalf("complete retry attempt: %d %s", retryCompleteResponse.Code, retryCompleteResponse.Body.String())
	}
	missingRequiredResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/interactions/"+interaction.ID+"/complete",
		`{"terminal_idempotency_key":"terminal-invalid","lease_token":"`+interaction.LeaseToken+`","lease_epoch":1,"completion_manifest_version":"1","completion_reason":"answer_returned","expected_operations":[{"operation_id":"`+
			operationResult.Operation.ID+`"}]}`)
	if missingRequiredResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing required manifest flag must be rejected: %d %s",
			missingRequiredResponse.Code, missingRequiredResponse.Body.String())
	}
	var missingRequiredError lifecycleTestErrorEnvelope
	decodeLifecycleResponse(t, missingRequiredResponse, &missingRequiredError)
	if missingRequiredError.Error.Code != "closure_manifest_invalid" ||
		missingRequiredError.Error.RequiredAction != "fix_closure_manifest" {
		t.Fatalf("missing required flag returned misleading guidance: %#v", missingRequiredError)
	}

	completeResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/interactions/"+interaction.ID+"/complete",
		`{"terminal_idempotency_key":"terminal-1","lease_token":"`+interaction.LeaseToken+`","lease_epoch":1,"completion_manifest_version":"1","completion_reason":"answer_returned","expected_operations":[{"operation_id":"`+
			operationResult.Operation.ID+`","required":true},{"operation_id":"`+
			retryResult.Operation.ID+`","required":true}],"expected_receipts":[{"receipt_id":"`+
			operationResult.Receipt.ID+`","required":true},{"receipt_id":"`+
			retryOperationResult.Receipt.ID+`","required":true},{"receipt_id":"`+
			retryResult.Receipt.ID+`","required":true}]}`)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete interaction: %d %s", completeResponse.Code, completeResponse.Body.String())
	}
	var completed sessionvo.Interaction
	decodeLifecycleResponse(t, completeResponse, &completed)
	if completed.ExecutionStatus != sessionvo.InteractionCompleted || completed.EvidenceStatus != sessionvo.EvidencePartial {
		t.Fatalf("unexpected completed interaction: %#v", completed)
	}

	getResponse := performLifecycleRequest(t, mux, http.MethodGet,
		"/api/agent-observability/v1/interactions/"+interaction.ID, "")
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get interaction: %d %s", getResponse.Code, getResponse.Body.String())
	}
}

func TestManagedFinishDerivesLeaseAndClosureManifest(t *testing.T) {
	t.Parallel()

	handler := httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{}))
	mux := http.NewServeMux()
	httphandler.RegisterSessionRoutes(mux, "/api/agent-observability/v1", handler)

	conversationResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations:ensure-current",
		`{"external_conversation_key":"managed-finish","idempotency_key":"ensure-managed"}`)
	var conversation sessionvo.Conversation
	decodeLifecycleResponse(t, conversationResponse, &conversation)

	interactionResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations/"+conversation.ID+"/interactions",
		`{"idempotency_key":"start-managed"}`)
	var interaction sessionvo.Interaction
	decodeLifecycleResponse(t, interactionResponse, &interaction)

	finishResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/interactions/"+interaction.ID+"/finish",
		`{"outcome":"completed","idempotency_key":"finish-managed","answer_artifact_ref":"artifact:answer","reason":"answer_returned"}`)
	if finishResponse.Code != http.StatusOK {
		t.Fatalf("managed finish: %d %s", finishResponse.Code, finishResponse.Body.String())
	}
	var finished sessionvo.Interaction
	decodeLifecycleResponse(t, finishResponse, &finished)
	if finished.ExecutionStatus != sessionvo.InteractionCompleted || finished.ClosureManifest == nil {
		t.Fatalf("unexpected managed finish: %#v", finished)
	}
	if finished.ClosureManifest.AnswerArtifactRef != "artifact:answer" ||
		len(finished.ClosureManifest.ExpectedOperations) != 0 ||
		len(finished.ClosureManifest.ExpectedReceipts) != 0 {
		t.Fatalf("closure was not derived from authoritative state: %#v", finished.ClosureManifest)
	}

	replay := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/interactions/"+interaction.ID+"/finish",
		`{"outcome":"completed","idempotency_key":"finish-managed","answer_artifact_ref":"artifact:answer","reason":"answer_returned"}`)
	if replay.Code != http.StatusOK {
		t.Fatalf("managed finish replay: %d %s", replay.Code, replay.Body.String())
	}

	changedReplay := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/interactions/"+interaction.ID+"/finish",
		`{"outcome":"completed","idempotency_key":"finish-managed","answer_artifact_ref":"artifact:changed","reason":"answer_returned"}`)
	if changedReplay.Code != http.StatusConflict {
		t.Fatalf("changed managed finish replay must conflict: %d %s", changedReplay.Code, changedReplay.Body.String())
	}
}

func TestStartInteractionConflictReturnsAuthoritativeActiveInteraction(t *testing.T) {
	t.Parallel()

	handler := httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{}))
	mux := http.NewServeMux()
	httphandler.RegisterSessionRoutes(mux, "/api/agent-observability/v1", handler)

	conversationResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations:ensure-current",
		`{"external_conversation_key":"active-recovery","idempotency_key":"ensure-active-recovery"}`)
	var conversation sessionvo.Conversation
	decodeLifecycleResponse(t, conversationResponse, &conversation)

	first := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations/"+conversation.ID+"/interactions",
		`{"idempotency_key":"start-active-1"}`)
	var active sessionvo.Interaction
	decodeLifecycleResponse(t, first, &active)

	conflict := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations/"+conversation.ID+"/interactions",
		`{"idempotency_key":"start-active-2"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("second active interaction must conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	var envelope lifecycleTestErrorEnvelope
	decodeLifecycleResponse(t, conflict, &envelope)
	if envelope.Error.Code != "interaction_in_progress" ||
		envelope.Error.RequiredAction != "bkn_finish_interaction" ||
		envelope.Error.CurrentInteractionID != active.ID {
		t.Fatalf("active interaction recovery guidance is incomplete: %#v", envelope.Error)
	}
}

func TestEnsureOperationHTTPReportsCreatedAndReplay(t *testing.T) {
	t.Parallel()

	handler := httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{}))
	mux := http.NewServeMux()
	httphandler.RegisterSessionRoutes(mux, "/api/agent-observability/v1", handler)
	conversationResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations:ensure-current",
		`{"external_conversation_key":"created-http","idempotency_key":"conversation-created-http"}`)
	var conversation sessionvo.Conversation
	decodeLifecycleResponse(t, conversationResponse, &conversation)
	interactionResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations/"+conversation.ID+"/interactions",
		`{"idempotency_key":"interaction-created-http","lease_seconds":300}`)
	var interaction sessionvo.Interaction
	decodeLifecycleResponse(t, interactionResponse, &interaction)
	body := `{"operation_key":"logical-http","tool_name":"context-loader","normalized_input_hash":"sha256:http","required":true,"lease_token":"` +
		interaction.LeaseToken + `","lease_epoch":1}`
	path := "/api/agent-observability/v1/conversations/" + conversation.ID +
		"/interactions/" + interaction.ID + "/operations:ensure"

	first := performLifecycleRequest(t, mux, http.MethodPost, path, body)
	replayed := performLifecycleRequest(t, mux, http.MethodPost, path, body)
	for label, response := range map[string]*httptest.ResponseRecorder{"first": first, "replayed": replayed} {
		if response.Code != http.StatusCreated {
			t.Fatalf("%s ensure: %d %s", label, response.Code, response.Body.String())
		}
	}
	var firstResult, replayedResult struct {
		Created   bool                `json:"created"`
		Execute   bool                `json:"execute"`
		Operation sessionvo.Operation `json:"operation"`
		Receipt   sessionvo.Receipt   `json:"receipt"`
	}
	decodeLifecycleResponse(t, first, &firstResult)
	decodeLifecycleResponse(t, replayed, &replayedResult)
	if !firstResult.Created || !firstResult.Execute ||
		replayedResult.Created || replayedResult.Execute ||
		firstResult.Operation.ID != replayedResult.Operation.ID ||
		firstResult.Receipt.ID != replayedResult.Receipt.ID {
		t.Fatalf("unexpected created/replay contract: first=%#v replay=%#v", firstResult, replayedResult)
	}
}

func TestConversationListUsesOwnerTupleAsRealQueryScope(t *testing.T) {
	t.Parallel()

	service := sessionsvc.New(sessionstore.New(), sessionsvc.Options{})
	handler := httphandler.NewSessionHandler(service)
	mux := http.NewServeMux()
	httphandler.RegisterSessionRoutes(mux, "/api/agent-observability/v1", handler)
	performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations:ensure-current",
		`{"external_conversation_key":"visible","idempotency_key":"one"}`)

	request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/conversations?limit=20", nil)
	setTrustedOwnerHeaders(request)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list conversations: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Entries []sessionvo.Conversation `json:"entries"`
	}
	decodeLifecycleResponse(t, response, &result)
	if len(result.Entries) != 1 || result.Entries[0].ExternalConversationKey != "visible" {
		t.Fatalf("unexpected owner-scoped list: %#v", result.Entries)
	}
}

func TestConversationLookupDoesNotDiscloseMissingVersusAnotherOwner(t *testing.T) {
	t.Parallel()

	handler := httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{}))
	mux := http.NewServeMux()
	httphandler.RegisterSessionRoutes(mux, "/api/agent-observability/v1", handler)
	createdResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/conversations:ensure-current",
		`{"external_conversation_key":"private","idempotency_key":"private-create"}`)
	var conversation sessionvo.Conversation
	decodeLifecycleResponse(t, createdResponse, &conversation)

	lookup := func(id string) lifecycleTestErrorEnvelope {
		request := httptest.NewRequest(http.MethodGet,
			"/api/agent-observability/v1/conversations/"+id, http.NoBody)
		setTrustedOwnerHeaders(request)
		request.Header.Set("X-BKN-Effective-Subject-ID", "other-user")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("lookup %q status = %d: %s", id, response.Code, response.Body.String())
		}
		var envelope lifecycleTestErrorEnvelope
		decodeLifecycleResponse(t, response, &envelope)
		return envelope
	}

	crossOwner := lookup(conversation.ID)
	missing := lookup("missing-conversation")
	if crossOwner.Error.Code != "resource_not_disclosed" ||
		crossOwner.Error.RequiredAction != "verify_scope_or_identifier" ||
		crossOwner.Error != missing.Error {
		t.Fatalf("lookup disclosure differs: cross-owner=%#v missing=%#v", crossOwner, missing)
	}
}

type lifecycleTestErrorEnvelope struct {
	Error struct {
		Code                 string `json:"code"`
		Message              string `json:"message"`
		RequiredAction       string `json:"required_action"`
		CurrentInteractionID string `json:"current_interaction_id"`
	} `json:"error"`
}

func setTrustedOwnerHeaders(request *http.Request) {
	request.Header.Set("X-BKN-Tenant-ID", "tenant-1")
	request.Header.Set("X-Business-Domain-ID", "domain-1")
	request.Header.Set("X-BKN-Application-Principal-ID", "app-1")
	request.Header.Set("X-BKN-Effective-Subject-Type", "user")
	request.Header.Set("X-BKN-Effective-Subject-ID", "user-1")
}

func performLifecycleRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	setTrustedOwnerHeaders(request)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeLifecycleResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}
