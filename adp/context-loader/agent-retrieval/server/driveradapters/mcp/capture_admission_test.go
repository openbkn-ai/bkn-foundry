package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

type disabledCapturePublisher struct{}

func (disabledCapturePublisher) CaptureDisabled() bool { return true }
func (disabledCapturePublisher) TryPublish(evidencepublisher.Event) evidencepublisher.PublishResult {
	panic("disabled publisher")
}

func TestDisabledCaptureStartStillCreatesManagedInteraction(t *testing.T) {
	bkntrace.SetEvidencePublisher(disabledCapturePublisher{})
	t.Cleanup(func() { bkntrace.SetEvidencePublisher(nil) })
	var calls []string
	client := bkntrace.NewLifecycleClient("http://core.test", &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.URL.Path)
		switch req.URL.Path {
		case "/api/agent-observability/v1/conversations:ensure-current":
			return lifecycleAdapterJSONResponse(http.StatusCreated, bkntrace.Conversation{ConversationID: "conv-committed", Status: "active"}), nil
		case "/api/agent-observability/v1/conversations/conv-committed/interactions":
			return lifecycleAdapterJSONResponse(http.StatusCreated, bkntrace.Interaction{ConversationID: "conv-committed", InteractionID: "int-real", ExecutionStatus: "active"}), nil
		default:
			t.Fatalf("unexpected Core path: %s", req.URL.Path)
			return nil, nil
		}
	})})
	request := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Name: toolKeyStartInteraction, Arguments: map[string]any{
		"conversation_mode": "new", "question": "business question", "agent_name": "test-agent",
	}}}
	result, err := handleLifecycleTool(client, toolKeyStartInteraction)(startArtifactTestContext(), request)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("%#v %v", result, err)
	}
	value, ok := result.StructuredContent.(map[string]any)
	if !ok || value["interaction_id"] != "int-real" || value["conversation_id"] != "conv-committed" ||
		value["capture_enabled"] != false || value["trace_recorded"] != false ||
		value["code"] != "capture_disabled" || value["required_action"] != nil || len(calls) != 2 {
		t.Fatalf("disabled capture lost managed IDs: %#v calls=%v", result.StructuredContent, calls)
	}
	_, raw, _ := lifecycleToolSchemas(toolKeyStartInteraction)
	schema, err := compileExecutableSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(normalized); err != nil {
		t.Fatalf("disabled start output schema: %v", err)
	}
}

func TestDisabledCaptureBusinessRunsWithoutRecordingOrFalseGap(t *testing.T) {
	bkntrace.SetEvidencePublisher(disabledCapturePublisher{})
	t.Cleanup(func() { bkntrace.SetEvidencePublisher(nil) })
	called, ensured := 0, 0
	next := func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		called++
		if req.GetArguments()["bkn_context"] != nil {
			t.Fatal("disabled capture passed managed context to downstream business tool")
		}
		if len(bkntrace.TracePartialReasons(ctx)) != 0 {
			t.Fatal("policy off was reported as a gap")
		}
		return mcpsdk.NewToolResultText("business result"), nil
	}
	ensure := func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
		ensured++
		t.Fatal("disabled capture registered operation")
		return nil, nil, nil
	}
	missing := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Name: toolKeyRunCypher, Arguments: map[string]any{"kn_id": "supply"}}}
	result, err := guardBusinessToolCall(ensure, next)(context.Background(), missing)
	if err != nil || result == nil || !result.IsError || called != 0 || ensured != 0 {
		t.Fatalf("missing context executed: %#v %v", result, err)
	}
	managed := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Name: toolKeyRunCypher, Arguments: map[string]any{"kn_id": "supply", "bkn_context": map[string]any{"conversation_id": "conv", "interaction_id": "int"}}}}
	result, err = guardBusinessToolCall(ensure, next)(context.Background(), managed)
	if err != nil || result == nil || result.IsError || called != 1 || ensured != 0 {
		t.Fatalf("managed call: %#v %v calls=%d ensured=%d", result, err, called, ensured)
	}
}

func TestCoreCaptureDisabledStillRunsBusinessWithRequiredContext(t *testing.T) {
	bkntrace.SetEvidencePublisher(nil)
	t.Cleanup(func() { bkntrace.SetEvidencePublisher(nil) })
	called := 0
	handler := guardBusinessToolCall(
		func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
			return nil, &lifecycleError{Code: "capture_disabled"}, nil
		},
		func(_ context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			called++
			if req.GetArguments()["bkn_context"] != nil {
				t.Fatal("managed context reached downstream tool after Core disabled capture")
			}
			return mcpsdk.NewToolResultText("business result"), nil
		},
	)
	request := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Name: toolKeyRunCypher, Arguments: map[string]any{
		"kn_id": "supply", "bkn_context": map[string]any{"conversation_id": "conv", "interaction_id": "int"},
	}}}
	result, err := handler(context.Background(), request)
	if err != nil || result == nil || result.IsError || called != 1 {
		t.Fatalf("Core disabled capture blocked business: %#v %v calls=%d", result, err, called)
	}
}

type switchingCapturePublisher struct{ disabled bool }

func (p *switchingCapturePublisher) CaptureDisabled() bool { return p.disabled }
func (*switchingCapturePublisher) TryPublish(evidencepublisher.Event) evidencepublisher.PublishResult {
	panic("cutoff must not publish")
}

func TestCaptureDisabledBetweenStartAndQuestionDoesNotReportOutage(t *testing.T) {
	publisher := &switchingCapturePublisher{}
	bkntrace.SetEvidencePublisher(publisher)
	t.Cleanup(func() { bkntrace.SetEvidencePublisher(nil) })
	t.Setenv("BKN_TRACE_ARTIFACT_ENDPOINT", "http://never.invalid")
	client := bkntrace.NewLifecycleClient("http://core.test", &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(*http.Request) (*http.Response, error) {
		publisher.disabled = true
		return lifecycleAdapterJSONResponse(http.StatusCreated, map[string]any{"conversation_id": "conv-committed", "interaction_id": "int-cutoff", "execution_status": "active"}), nil
	})})
	result, err := handleLifecycleTool(client, toolKeyStartInteraction)(startArtifactTestContext(), startArtifactTestRequest())
	if err != nil || result == nil || result.IsError {
		t.Fatalf("%#v %v", result, err)
	}
	if result.Meta != nil && result.Meta.AdditionalFields[traceAvailabilityMetaKey] != nil {
		t.Fatalf("policy cutoff became an outage: %#v", result.Meta)
	}
	if result.StructuredContent.(map[string]any)["interaction_id"] != "int-cutoff" {
		t.Fatal("lost already accepted identity")
	}
}

func TestCaptureDisabledDuringAnswerAllowsExistingFinish(t *testing.T) {
	bkntrace.SetEvidencePublisher(nil)
	finished := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent-observability/v1/interactions/int-cutoff":
			if err := json.NewEncoder(w).Encode(map[string]any{"conversation_id": "conv-committed", "interaction_id": "int-cutoff", "execution_status": "active", "updated_at": time.Now().UTC()}); err != nil {
				t.Error(err)
			}
		case "/api/agent-observability/v1/evidence/artifacts":
			w.WriteHeader(http.StatusConflict)
			if err := json.NewEncoder(w).Encode(map[string]any{"code": "CAPTURE_DISABLED", "message": "capture is disabled"}); err != nil {
				t.Error(err)
			}
		case "/api/agent-observability/v1/interactions/int-cutoff/finish":
			finished++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["answer_artifact_ref"] != nil {
				t.Error("fabricated answer artifact")
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"conversation_id": "conv-committed", "interaction_id": "int-cutoff", "execution_status": "completed", "evidence_status": "partial"}); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	t.Setenv("BKN_TRACE_ARTIFACT_ENDPOINT", backend.URL+"/api/agent-observability/v1/evidence/artifacts")
	t.Setenv("BKN_TRACE_ARTIFACT_TOKEN", "test-credential")
	result, err := handleLifecycleTool(bkntrace.NewLifecycleClient(backend.URL, backend.Client()), toolKeyFinishInteraction)(startArtifactTestContext(), mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Arguments: map[string]any{"interaction_id": "int-cutoff", "outcome": "completed", "answer": "real business result"}}})
	if err != nil || result == nil || result.IsError || finished != 1 {
		t.Fatalf("finish=%d %#v %v", finished, result, err)
	}
	if result.StructuredContent.(map[string]any)["execution_status"] != "completed" {
		t.Fatalf("policy stop reported as failure: %#v", result)
	}
}
