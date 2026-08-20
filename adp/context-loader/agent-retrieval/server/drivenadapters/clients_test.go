// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// mockLogger is a mock logger for tests.
type mockLogger struct{}

func (m *mockLogger) Debug(args ...interface{})                                  {}
func (m *mockLogger) Debugf(format string, args ...interface{})                  {}
func (m *mockLogger) Info(args ...interface{})                                   {}
func (m *mockLogger) Infof(format string, args ...interface{})                   {}
func (m *mockLogger) Warn(args ...interface{})                                   {}
func (m *mockLogger) Warnf(format string, args ...interface{})                   {}
func (m *mockLogger) Error(args ...interface{})                                  {}
func (m *mockLogger) Errorf(format string, args ...interface{})                  {}
func (m *mockLogger) WithContext(ctx context.Context) interfaces.Logger          { return m }
func (m *mockLogger) WithField(key string, value interface{}) interfaces.Logger  { return m }
func (m *mockLogger) WithFields(fields map[string]interface{}) interfaces.Logger { return m }

// jsonBytes renders a fixture as the raw response body the client now receives.
func jsonBytes(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// mockHTTPClient is a mock HTTP client for tests.
type mockHTTPClient struct {
	handlerFunc         func(ctx context.Context, method, url string, header map[string]string, body interface{}) (int, interface{}, error)
	postNoUnmarshalFunc func(ctx context.Context, url string, header map[string]string, body interface{}) (int, []byte, error)
	// bytesFunc serves PostBytes/GetBytes with a raw body. Tests that care about
	// the exact JSON text — the big-integer cases — must set it, because handing
	// back a Go value instead would re-introduce the very interface{} hop the
	// production code was changed to avoid.
	bytesFunc func(ctx context.Context, method, url string, header map[string]string, body interface{}) (int, []byte, error)
}

func (m *mockHTTPClient) Get(ctx context.Context, rawURL string, params url.Values, header map[string]string) (statusCode int, resp interface{}, err error) {
	return m.handlerFunc(ctx, "GET", rawURL, header, nil)
}

func (m *mockHTTPClient) Post(ctx context.Context, url string, header map[string]string, body interface{}) (statusCode int, resp interface{}, err error) {
	return m.handlerFunc(ctx, "POST", url, header, body)
}

func (m *mockHTTPClient) Put(ctx context.Context, url string, header map[string]string, body interface{}) (statusCode int, resp interface{}, err error) {
	return m.handlerFunc(ctx, "PUT", url, header, body)
}

func (m *mockHTTPClient) Delete(ctx context.Context, url string, header map[string]string) (statusCode int, resp interface{}, err error) {
	return m.handlerFunc(ctx, "DELETE", url, header, nil)
}

// doBytes backs PostBytes/GetBytes. With no bytesFunc set it falls back to
// handlerFunc and marshals its result, so the pre-existing tests keep working.
func (m *mockHTTPClient) doBytes(ctx context.Context, method, rawURL string, header map[string]string, body interface{}) (int, []byte, error) {
	if m.bytesFunc != nil {
		return m.bytesFunc(ctx, method, rawURL, header, body)
	}
	code, resp, err := m.handlerFunc(ctx, method, rawURL, header, body)
	if err != nil || resp == nil {
		return code, nil, err
	}
	raw, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return code, nil, marshalErr
	}
	return code, raw, nil
}

func (m *mockHTTPClient) PostBytes(ctx context.Context, rawURL string, header map[string]string, body interface{}) (int, []byte, error) {
	return m.doBytes(ctx, "POST", rawURL, header, body)
}

func (m *mockHTTPClient) GetBytes(ctx context.Context, rawURL string, params url.Values, header map[string]string) (int, []byte, error) {
	return m.doBytes(ctx, "GET", rawURL, header, nil)
}

func (m *mockHTTPClient) GetNoUnmarshal(ctx context.Context, rawURL string, params url.Values, header map[string]string) (statusCode int, resp []byte, err error) {
	return 200, nil, nil
}

func (m *mockHTTPClient) PostNoUnmarshal(ctx context.Context, url string, header map[string]string, body interface{}) (statusCode int, resp []byte, err error) {
	if m.postNoUnmarshalFunc != nil {
		return m.postNoUnmarshalFunc(ctx, url, header, body)
	}
	return 200, nil, nil
}

func (m *mockHTTPClient) PutNoUnmarshal(ctx context.Context, url string, header map[string]string, body interface{}) (statusCode int, resp []byte, err error) {
	return 200, nil, nil
}

func (m *mockHTTPClient) DeleteNoUnmarshal(ctx context.Context, url string, header map[string]string) (statusCode int, resp []byte, err error) {
	return 200, nil, nil
}

func (m *mockHTTPClient) Patch(ctx context.Context, url string, header map[string]string, body interface{}) (statusCode int, resp interface{}, err error) {
	return m.handlerFunc(ctx, "PATCH", url, header, body)
}

func (m *mockHTTPClient) PatchNoUnmarshal(ctx context.Context, url string, header map[string]string, body interface{}) (statusCode int, resp []byte, err error) {
	return 200, nil, nil
}

func TestMFModelAPIClient_Chat(t *testing.T) {
	// Simulate a non-streaming response.
	response := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"message": map[string]interface{}{
					"content": "Hello World",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate the path.
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			t.Errorf("Expected path to contain /chat/completions, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Use the mock HTTPClient.
	mockHTTP := &mockHTTPClient{
		handlerFunc: func(ctx context.Context, method, url string, header map[string]string, body interface{}) (int, interface{}, error) {
			return 200, response, nil
		},
	}

	client := &mfModelAPIClient{
		logger:     &mockLogger{},
		baseURL:    server.URL + "/api/private/mf-model-api",
		httpClient: mockHTTP,
	}

	req := &interfaces.LLMChatReq{
		Model: "test-model",
		Messages: []interfaces.LLMMessage{
			{Role: "user", Content: "Hello"},
		},
		Stream: false,
	}

	content, err := client.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	expected := "Hello World"
	if content != expected {
		t.Errorf("Expected content '%s', got '%s'", expected, content)
	}
}

func TestMFModelAPIClient_Rerank(t *testing.T) {
	// Simulate a rerank response.
	response := interfaces.RerankResp{
		Results: []interfaces.RerankResult{
			{Index: 0, RelevanceScore: 0.9},
			{Index: 1, RelevanceScore: 0.7},
			{Index: 2, RelevanceScore: 0.3},
		},
	}

	mockHTTP := &mockHTTPClient{
		handlerFunc: func(ctx context.Context, method, url string, header map[string]string, body interface{}) (int, interface{}, error) {
			// Validate the path.
			if !strings.Contains(url, "/reranker") {
				t.Errorf("Expected URL to contain /reranker, got %s", url)
			}
			return 200, response, nil
		},
	}

	client := &mfModelAPIClient{
		logger:     &mockLogger{},
		baseURL:    "http://test/api/private/mf-model-api",
		httpClient: mockHTTP,
	}

	documents := []string{"doc1", "doc2", "doc3"}
	resp, err := client.Rerank(context.Background(), "test query", documents, "")
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	if len(resp.Results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(resp.Results))
	}

	if resp.Results[0].RelevanceScore != 0.9 {
		t.Errorf("Expected first score 0.9, got %f", resp.Results[0].RelevanceScore)
	}
}

func TestMFModelAPIClientRerankForksChildOperation(t *testing.T) {
	var captured map[string]string
	mockHTTP := &mockHTTPClient{handlerFunc: func(_ context.Context, _, _ string, header map[string]string, _ interface{}) (int, interface{}, error) {
		captured = header
		return http.StatusOK, interfaces.RerankResp{}, nil
	}}
	client := &mfModelAPIClient{logger: &mockLogger{}, baseURL: "http://model", httpClient: mockHTTP}
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		RequestID: "req_context_model_child_001", InteractionID: "interaction-1",
		OperationID: "parent-operation", CausationEventID: "parent-event", Attempt: 2,
		ObservedAt: "2026-07-25T08:00:00Z", ObservedAtProvided: true,
	})

	_, err := client.Rerank(ctx, "query", []string{"document"}, "model")
	if err != nil {
		t.Fatal(err)
	}
	if captured[common.HeaderBKNOperationID] == "" || captured[common.HeaderBKNOperationID] == "parent-operation" {
		t.Fatalf("downstream operation was not forked: %#v", captured)
	}
	if captured[common.HeaderBKNCausationEventID] != "parent-event" || captured[common.HeaderBKNAttempt] != "2" || captured[common.HeaderBKNEventObservedAt] != "2026-07-25T08:00:00Z" {
		t.Fatalf("business causality envelope was not propagated: %#v", captured)
	}
}
