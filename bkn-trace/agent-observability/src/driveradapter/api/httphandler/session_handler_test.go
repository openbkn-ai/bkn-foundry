package httphandler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
)

func TestEnsureConversationUsesTrustedHeadersAndIgnoresBodyIdentity(t *testing.T) {
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

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	var conversation sessionvo.Conversation
	if err := json.Unmarshal(response.Body.Bytes(), &conversation); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if conversation.Owner.TenantID != "tenant-1" || conversation.Owner.EffectiveSubjectID != "user-1" {
		t.Fatalf("body identity overrode trusted headers: %#v", conversation.Owner)
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
		envelope.Error.RequiredAction != "create_conversation" {
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
	}
	decodeLifecycleResponse(t, operationResponse, &operationResult)

	receiptResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/operations/"+operationResult.Operation.ID+"/attempts/1:complete",
		`{"receipt_id":"`+operationResult.Receipt.ID+`","payload_hash":"sha256:result","evidence_durability":"durable","request_id":"req-1","trace_id":"4b3d59daeff5bfbb23d46c47a5051ec9"}`)
	if receiptResponse.Code != http.StatusOK {
		t.Fatalf("complete receipt: %d %s", receiptResponse.Code, receiptResponse.Body.String())
	}

	completeResponse := performLifecycleRequest(t, mux, http.MethodPost,
		"/api/agent-observability/v1/interactions/"+interaction.ID+"/complete",
		`{"terminal_idempotency_key":"terminal-1","lease_token":"`+interaction.LeaseToken+`","lease_epoch":1,"completion_manifest_version":"1","completion_reason":"answer_returned","expected_operations":[{"operation_id":"`+
			operationResult.Operation.ID+`","required":true}],"expected_receipts":[{"receipt_id":"`+
			operationResult.Receipt.ID+`","required":true}]}`)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete interaction: %d %s", completeResponse.Code, completeResponse.Body.String())
	}
	var completed sessionvo.Interaction
	decodeLifecycleResponse(t, completeResponse, &completed)
	if completed.ExecutionStatus != sessionvo.InteractionCompleted || completed.EvidenceStatus != sessionvo.EvidenceComplete {
		t.Fatalf("unexpected completed interaction: %#v", completed)
	}

	getResponse := performLifecycleRequest(t, mux, http.MethodGet,
		"/api/agent-observability/v1/interactions/"+interaction.ID, "")
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get interaction: %d %s", getResponse.Code, getResponse.Body.String())
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

type lifecycleTestErrorEnvelope struct {
	Error struct {
		Code           string `json:"code"`
		RequiredAction string `json:"required_action"`
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
