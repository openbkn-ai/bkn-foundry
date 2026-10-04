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

func TestDisabledCaptureStartCreatesNoConversation(t *testing.T) {
	bkntrace.SetEvidencePublisher(disabledCapturePublisher{})
	t.Cleanup(func() { bkntrace.SetEvidencePublisher(nil) })
	client := bkntrace.NewLifecycleClient("http://never.invalid", &http.Client{Transport: lifecycleAdapterRoundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("disabled start contacted Core"); return nil, nil })})
	result, err := handleLifecycleTool(client, "bkn_start_interaction")(startArtifactTestContext(), startArtifactTestRequest())
	if err != nil || result == nil || result.IsError {
		t.Fatalf("%#v %v", result, err)
	}
	value, ok := result.StructuredContent.(map[string]any)
	if !ok || value["capture_enabled"] != false || value["interaction_id"] != nil || value["conversation_id"] != nil {
		t.Fatalf("disabled start created IDs: %#v", result.StructuredContent)
	}
}

func TestDisabledCaptureBusinessRunsWithoutRecordingOrFalseGap(t *testing.T) {
	bkntrace.SetEvidencePublisher(disabledCapturePublisher{})
	t.Cleanup(func() { bkntrace.SetEvidencePublisher(nil) })
	called := 0
	next := func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		called++
		if req.GetArguments()["bkn_context"] != nil {
			t.Fatal("disabled capture propagated managed context")
		}
		if len(bkntrace.TracePartialReasons(ctx)) != 0 {
			t.Fatal("policy off was reported as a gap")
		}
		return mcpsdk.NewToolResultText("business result"), nil
	}
	ensure := func(context.Context, operationIntent) (*operationResult, *lifecycleError, error) {
		t.Fatal("disabled capture registered operation")
		return nil, nil, nil
	}
	for _, args := range []map[string]any{{"kn_id": "supply"}, {"kn_id": "supply", "bkn_context": map[string]any{"conversation_id": "conv", "interaction_id": "int"}}} {
		result, err := guardBusinessToolCall(ensure, next)(context.Background(), mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{Name: toolKeyRunCypher, Arguments: args}})
		if err != nil || result.IsError {
			t.Fatalf("%#v %v", result, err)
		}
	}
	if called != 2 {
		t.Fatalf("business calls=%d", called)
	}
}

func TestDisabledLifecycleResponseMatchesOutputSchema(t *testing.T) {
	for _, name := range []string{toolKeyStartInteraction, toolKeyFinishInteraction} {
		_, raw, _ := lifecycleToolSchemas(name)
		schema, err := compileExecutableSchema(raw)
		if err != nil {
			t.Fatal(err)
		}
		result := lifecycleCaptureDisabledResult()
		if err := schema.Validate(result.StructuredContent); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
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
