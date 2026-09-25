package evidencepublisher

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPublisherRuntimeUsesVerifiedSnapshotAndAcknowledgesDisabledBoundary(t *testing.T) {
	now := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enabled := policySnapshotForTest(now)
	disabled := policySnapshotForTest(now)
	disabled.Revision = 43
	disabled.TraceAdmission = "disabled"
	disabled.EvidenceAdmission = "disabled"
	reenabled := policySnapshotForTest(now)
	reenabled.Revision = 44
	policyReads := 0
	heartbeats := []string{}
	acks := []string{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case traceEvidencePolicyPath:
			policyReads++
			if policyReads == 1 {
				return policyResponse(signedPolicySnapshot(t, privateKey, enabled)), nil
			}
			if policyReads == 2 {
				return policyResponse(signedPolicySnapshot(t, privateKey, disabled)), nil
			}
			return policyResponse(signedPolicySnapshot(t, privateKey, reenabled)), nil
		case "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat":
			body, _ := io.ReadAll(request.Body)
			heartbeats = append(heartbeats, string(body))
			return noContentResponse(), nil
		case traceEvidenceConfigurationPath:
			// desired/effective are not admission inputs: the signed snapshot is.
			return configurationResponse(`{"kind":"configuration_get","desired_state":"enabled","effective_state":"enabled","policy_revision":43,"active_operation_id":"op-43"}`), nil
		case "/api/agent-observability/v1/internal/trace-evidence/operations/op-43:publisher-ack":
			body, _ := io.ReadAll(request.Body)
			acks = append(acks, string(body))
			return noContentResponse(), nil
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL)
			return nil, nil
		}
	})}
	policy, err := NewPolicyClient(PolicyClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token"), Verifier: PolicyVerifierConfig{AudienceClusterID: "cluster-a", CurrentKeyID: "key-1", CurrentPublicKey: publicKey, Now: func() time.Time { return now }}})
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := NewConfigurationClient(ConfigurationClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token")})
	if err != nil {
		t.Fatal(err)
	}
	control, err := NewControlClient(ControlClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token")})
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	runtime, err := NewPublisherRuntime(context.Background(), PublisherRuntimeConfig{Publisher: publisherTestConfig(), Sender: sender, Policy: policy, Configuration: configuration, Control: control, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewPublisherRuntime() error = %v", err)
	}
	if result := runtime.TryPublish(publisherTestEvent()); result.Disposition != Accepted {
		t.Fatalf("TryPublish() = %+v", result)
	}
	if got := runtime.publisher.SnapshotQueue()[0].Header("capture_policy_revision"); got != "42" {
		t.Fatalf("capture_policy_revision = %q; want 42", got)
	}
	if err := runtime.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if result := runtime.TryPublish(publisherTestEvent()); result.Disposition != Dropped || result.Reason != ReasonPublisherClosing {
		t.Fatalf("disabled TryPublish() = %+v", result)
	}
	if err := runtime.Refresh(context.Background()); err != nil {
		t.Fatalf("rollback Refresh() error = %v", err)
	}
	if result := runtime.TryPublish(publisherTestEvent()); result.Disposition != Accepted {
		t.Fatalf("reenabled TryPublish() = %+v", result)
	}
	if got := runtime.publisher.SnapshotQueue()[0].Header("capture_policy_revision"); got != "44" {
		t.Fatalf("reenabled capture_policy_revision = %q; want 44", got)
	}
	if len(sender.records) != 1 || len(heartbeats) != 3 || len(acks) != 1 {
		t.Fatalf("records=%d heartbeats=%d acks=%d", len(sender.records), len(heartbeats), len(acks))
	}
	var ack struct {
		CapturePolicyRevision uint64 `json:"capture_policy_revision"`
		LastAcceptedSequence  uint64 `json:"last_accepted_sequence"`
		Published             uint64 `json:"published"`
		Dropped               uint64 `json:"dropped"`
		QueueEmpty            bool   `json:"queue_empty"`
	}
	if err := json.Unmarshal([]byte(acks[0]), &ack); err != nil {
		t.Fatal(err)
	}
	if ack.CapturePolicyRevision != 43 || ack.LastAcceptedSequence != 1 || ack.Published != 1 || ack.Dropped != 0 || !ack.QueueEmpty {
		t.Fatalf("ack = %+v", ack)
	}
}

func TestPublisherRuntimeDoesNotRequireStaticPolicyRevision(t *testing.T) {
	now := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case traceEvidencePolicyPath:
			return policyResponse(signedPolicySnapshot(t, privateKey, policySnapshotForTest(now))), nil
		case "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat":
			return noContentResponse(), nil
		default:
			t.Fatalf("unexpected request: %s", request.URL)
			return nil, nil
		}
	})}
	policy, _ := NewPolicyClient(PolicyClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("token"), Verifier: PolicyVerifierConfig{AudienceClusterID: "cluster-a", CurrentKeyID: "key-1", CurrentPublicKey: publicKey, Now: func() time.Time { return now }}})
	configuration, _ := NewConfigurationClient(ConfigurationClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("token")})
	control, _ := NewControlClient(ControlClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("token")})
	config := publisherTestConfig()
	config.CapturePolicyRevision = ""
	runtime, err := NewPublisherRuntime(context.Background(), PublisherRuntimeConfig{Publisher: config, Sender: &fakeSender{}, Policy: policy, Configuration: configuration, Control: control, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewPublisherRuntime() error = %v", err)
	}
	if result := runtime.TryPublish(publisherTestEvent()); result.Disposition != Accepted {
		t.Fatalf("TryPublish() = %+v", result)
	}
	if got := runtime.publisher.SnapshotQueue()[0].Header("capture_policy_revision"); got != "42" {
		t.Fatalf("capture_policy_revision = %q; want 42", got)
	}
}

func TestPublisherRuntimeStartsStableDisabledWithoutOperationOrAck(t *testing.T) {
	now := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := policySnapshotForTest(now)
	snapshot.TraceAdmission, snapshot.EvidenceAdmission = "disabled", "disabled"
	configurationReads, acknowledgements := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case traceEvidencePolicyPath:
			return policyResponse(signedPolicySnapshot(t, privateKey, snapshot)), nil
		case "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat":
			return noContentResponse(), nil
		case traceEvidenceConfigurationPath:
			configurationReads++
			return configurationResponse(`{"kind":"configuration_get","policy_revision":42,"active_operation_id":null}`), nil
		case "/api/agent-observability/v1/internal/trace-evidence/operations/":
			acknowledgements++
			t.Fatal("stable disabled runtime must not ACK without an active operation")
			return nil, nil
		default:
			t.Fatalf("unexpected request: %s", request.URL)
			return nil, nil
		}
	})}
	policy, _ := NewPolicyClient(PolicyClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("token"), Verifier: PolicyVerifierConfig{AudienceClusterID: "cluster-a", CurrentKeyID: "key-1", CurrentPublicKey: publicKey, Now: func() time.Time { return now }}})
	configuration, _ := NewConfigurationClient(ConfigurationClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("token")})
	control, _ := NewControlClient(ControlClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("token")})
	runtime, err := NewPublisherRuntime(context.Background(), PublisherRuntimeConfig{Publisher: publisherTestConfig(), Sender: &fakeSender{}, Policy: policy, Configuration: configuration, Control: control, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewPublisherRuntime() error = %v", err)
	}
	if err := runtime.Refresh(context.Background()); err != nil {
		t.Fatalf("stable disabled Refresh() error = %v", err)
	}
	if configurationReads != 2 || acknowledgements != 0 {
		t.Fatalf("configuration reads=%d acknowledgements=%d", configurationReads, acknowledgements)
	}
	if result := runtime.TryPublish(publisherTestEvent()); result.Disposition != Dropped || result.Reason != ReasonPublisherClosing {
		t.Fatalf("TryPublish() = %+v", result)
	}
}

func TestPublisherRuntimeFailsClosedWhenCachedSnapshotExpires(t *testing.T) {
	now := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := policySnapshotForTest(now)
	snapshot.ExpiresAt = now.Add(time.Second)
	policyReads := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case traceEvidencePolicyPath:
			policyReads++
			if policyReads > 1 {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
			}
			return policyResponse(signedPolicySnapshot(t, privateKey, snapshot)), nil
		case "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat":
			return noContentResponse(), nil
		default:
			t.Fatalf("unexpected request: %s", request.URL)
			return nil, nil
		}
	})}
	policy, err := NewPolicyClient(PolicyClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token"), Verifier: PolicyVerifierConfig{AudienceClusterID: "cluster-a", CurrentKeyID: "key-1", CurrentPublicKey: publicKey, Now: func() time.Time { return now }}})
	if err != nil {
		t.Fatal(err)
	}
	control, err := NewControlClient(ControlClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token")})
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := NewConfigurationClient(ConfigurationClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token")})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewPublisherRuntime(context.Background(), PublisherRuntimeConfig{Publisher: publisherTestConfig(), Sender: &fakeSender{}, Policy: policy, Configuration: configuration, Control: control, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if result := runtime.TryPublish(publisherTestEvent()); result.Disposition != Dropped || result.Reason != ReasonPublisherUnavailable {
		t.Fatalf("expired TryPublish() = %+v", result)
	}
	now = now.Add(-1500 * time.Millisecond)
	if err := runtime.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh() error = nil; want policy read failure")
	}
	if runtime.LastRefreshError() == nil {
		t.Fatal("LastRefreshError() = nil after failed refresh")
	}
	if result := runtime.TryPublish(publisherTestEvent()); result.Disposition != Dropped || result.Reason != ReasonPublisherUnavailable {
		t.Fatalf("failed-refresh TryPublish() = %+v", result)
	}
}

func noContentResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}
}
