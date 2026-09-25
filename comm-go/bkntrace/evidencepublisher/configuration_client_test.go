package evidencepublisher

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestConfigurationClientReturnsActiveOperationForMatchingRevision(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://trace.internal/api/agent-observability/v1/trace-evidence-configuration" {
			t.Fatalf("unexpected configuration request: %s %s", request.Method, request.URL)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer workload-token" {
			t.Fatalf("Authorization = %q", got)
		}
		return configurationResponse(`{"kind":"configuration_get","desired_state":"enabled","effective_state":"disabled","policy_revision":42,"last_stable_revision":41,"active_operation_id":"op-42","heartbeat_interval_seconds":10,"lease_ttl_seconds":30,"admission_budget":{"contract_version":"AdmissionBudgetV1","profile":"default","sampled_at":"2026-09-22T08:00:00Z","fresh_until":"2026-09-22T08:01:00Z","measurements":[{"metric":"trace_opensearch_capacity","source":"opensearch-cluster-health","sample_time":"2026-09-22T08:00:00Z","value":0.58,"threshold":0.8,"fresh":true}]}}`), nil
	})}
	configuration, err := NewConfigurationClient(ConfigurationClientConfig{
		BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token"),
	})
	if err != nil {
		t.Fatalf("NewConfigurationClient() error = %v", err)
	}
	operation, allowed, err := configuration.OperationForRevision(context.Background(), 42)
	if err != nil || !allowed || operation.ID != "op-42" || operation.Revision != 42 {
		t.Fatalf("OperationForRevision() = %+v, %t, %v", operation, allowed, err)
	}
}

func TestConfigurationClientDoesNotAllowAckForDifferentRevision(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return configurationResponse(`{"kind":"configuration_get","policy_revision":41,"active_operation_id":"op-41"}`), nil
	})}
	configuration, err := NewConfigurationClient(ConfigurationClientConfig{
		BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation, allowed, callErr := configuration.OperationForRevision(context.Background(), 42); callErr != nil || allowed || operation.ID != "" {
		t.Fatalf("OperationForRevision() = %+v, %t, %v; want no ACK candidate", operation, allowed, callErr)
	}
}

func TestConfigurationClientDoesNotAllowAckWithoutActiveOperation(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return configurationResponse(`{"kind":"configuration_get","policy_revision":42,"active_operation_id":null}`), nil
	})}
	configuration, err := NewConfigurationClient(ConfigurationClientConfig{
		BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, allowed, callErr := configuration.OperationForRevision(context.Background(), 42); callErr != nil || allowed {
		t.Fatalf("OperationForRevision() = allowed=%t err=%v; want no ACK candidate", allowed, callErr)
	}
}

func TestConfigurationClientReturnsTokenFailureWithoutControlRequest(t *testing.T) {
	configuration, err := NewConfigurationClient(ConfigurationClientConfig{
		BaseURL: "https://trace.internal",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("configuration endpoint called after token failure")
			return nil, nil
		})},
		TokenSource: failingTokenSource{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, allowed, callErr := configuration.OperationForRevision(context.Background(), 42); callErr == nil || allowed {
		t.Fatalf("OperationForRevision() = allowed=%t err=%v; want token failure and no ACK", allowed, callErr)
	}
}

func configurationResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

type staticTokenSource string

func (s staticTokenSource) Token(context.Context) (string, error) { return string(s), nil }

type failingTokenSource struct{}

func (failingTokenSource) Token(context.Context) (string, error) { return "", context.DeadlineExceeded }
