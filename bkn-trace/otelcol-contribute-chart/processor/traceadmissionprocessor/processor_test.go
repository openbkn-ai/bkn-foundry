// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full
// license text.

package traceadmissionprocessor

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceadmissionsvc"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor"
)

func TestProcessorUsesFrozenPolicySnapshotAndFailsClosedForLegacyField(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	snapshot, err := traceadmissionsvc.SignSnapshot(traceadmissionsvc.SignedSnapshot{
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeEnabled, EvidenceAdmission: traceadmissionsvc.ModeEnabled,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute), KeyID: "k1", Audience: "cluster-a",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy": {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config": {status: http.StatusOK, body: []byte(`{"revision":42,"operation":{"id":"op-42","phase":"enabling"}}`)},
		"https://safe.internal/token":  {status: http.StatusOK, body: []byte(`{"access_token":"token-1","token_type":"Bearer","expires_in":300}`)},
	}}
	p, calls := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config", TokenURL: "https://safe.internal/token",
		ClientID: "trace-gateway", ClientSecret: "secret", Audience: "cluster-a", CurrentKeyID: "k1",
		CurrentPublicKey: base64.RawStdEncoding.EncodeToString(publicKey), WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.operationID() != "op-42" || p.revision() != 42 {
		t.Fatalf("operation/revision = %q/%d", p.operationID(), p.revision())
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatal("disabled/legacy test helper unexpectedly forwarded spans")
	}
	legacy := map[string]any{"revision": 43, "admission_mode": "enabled", "issued_at": now.Add(-time.Second), "expires_at": now.Add(time.Minute), "key_id": "k1", "audience_cluster_id": "cluster-a", "signature": "ed25519:legacy"}
	transport.responses["https://safe.internal/policy"] = scriptedResponse{status: http.StatusOK, body: mustJSON(legacy)}
	if err := p.refresh(context.Background()); err == nil {
		t.Fatal("legacy admission_mode snapshot was accepted")
	}
}

func TestProcessorUsesClientCredentialsAndSendsBearerOnHeartbeatAndAck(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	snapshot, err := traceadmissionsvc.SignSnapshot(traceadmissionsvc.SignedSnapshot{
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeDisabled, EvidenceAdmission: traceadmissionsvc.ModeDisabled,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute), KeyID: "k1", Audience: "cluster-a",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy":               {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config":               {status: http.StatusOK, body: []byte(`{"revision":42,"operation":{"id":"op-42","phase":"disabling"}}`)},
		"https://safe.internal/token":                {status: http.StatusOK, body: []byte(`{"access_token":"token-1","token_type":"Bearer","expires_in":300}`)},
		"https://safe.internal/heartbeat":            {status: http.StatusNoContent},
		"https://safe.internal/operations/op-42:ack": {status: http.StatusNoContent},
	}}
	p, _ := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config", TokenURL: "https://safe.internal/token",
		HeartbeatURL: "https://safe.internal/heartbeat", AckURLBase: "https://safe.internal/operations/",
		ClientID: "trace-gateway", ClientSecret: "secret", Audience: "cluster-a", CurrentKeyID: "k1",
		CurrentPublicKey: base64.RawStdEncoding.EncodeToString(publicKey), WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(transport.requests) != 5 {
		t.Fatalf("request count = %d, want token + policy + config + heartbeat + ack", len(transport.requests))
	}
	for _, request := range transport.requests {
		if request.URL.Path != "/token" && request.Header.Get("Authorization") != "Bearer token-1" {
			t.Fatalf("request %s missing bearer authorization", request.URL.Path)
		}
	}
	var tokenRequest *http.Request
	for _, request := range transport.requests {
		if request.URL.Path == "/token" {
			tokenRequest = request
			break
		}
	}
	if tokenRequest == nil || tokenRequest.Method != http.MethodPost {
		t.Fatal("client_credentials token request was not issued")
	}
	clientID, clientSecret, basicOK := tokenRequest.BasicAuth()
	if !basicOK || clientID != "trace-gateway" || clientSecret != "secret" || string(transport.requestBody("/token")) != "grant_type=client_credentials" {
		t.Fatalf("unexpected client_credentials request: auth=%q/%q basic=%v body=%q", clientID, clientSecret, basicOK, transport.requestBody("/token"))
	}
	var ack TraceGatewayAcknowledgementV1
	if err := json.Unmarshal(transport.requestBody("/operations/op-42:ack"), &ack); err != nil {
		t.Fatal(err)
	}
	if ack.CapturePolicyRevision != 42 || ack.AdmissionState != "disabled" || ack.QueueDisposition.State != "gap" || ack.QueueDisposition.Unaccounted != nil {
		t.Fatalf("unexpected ACK: %+v", ack)
	}
	assertFrozenGatewayAckJSON(t, transport.requestBody("/operations/op-42:ack"))
}

func assertFrozenGatewayAckJSON(t *testing.T, body []byte) {
	t.Helper()
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"contract_version": true, "gateway_instance_id": true, "workload_identity": true,
		"process_boot_id": true, "capture_policy_revision": true, "admission_state": true,
		"ready": true, "acknowledged_at": true, "queue_disposition": true,
	}
	if len(wire) != len(want) {
		t.Fatalf("frozen ACK field count = %d, want %d: %s", len(wire), len(want), string(body))
	}
	for key := range wire {
		if !want[key] {
			t.Fatalf("frozen ACK emitted non-contract field %q: %s", key, string(body))
		}
	}
	var disposition map[string]json.RawMessage
	if err := json.Unmarshal(wire["queue_disposition"], &disposition); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"state", "exported", "dropped", "unaccounted", "gap_reason"} {
		if _, ok := disposition[key]; !ok {
			t.Fatalf("frozen gap ACK omitted queue_disposition.%s: %s", key, string(body))
		}
	}
	if string(disposition["unaccounted"]) != "null" {
		t.Fatalf("gap ACK must encode required nullable unaccounted as null, got %s", disposition["unaccounted"])
	}
}

func TestProcessorUsesFrozenConfigurationActiveOperationCandidate(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	snapshot, err := traceadmissionsvc.SignSnapshot(traceadmissionsvc.SignedSnapshot{
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeDisabled, EvidenceAdmission: traceadmissionsvc.ModeDisabled,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute), KeyID: "k1", Audience: "cluster-a",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy":               {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config":               {status: http.StatusOK, body: []byte(`{"kind":"configuration_get","desired_state":"disabled","effective_state":"disabled","policy_revision":42,"last_stable_revision":41,"active_operation_id":"op-42","heartbeat_interval_seconds":10,"lease_ttl_seconds":30,"admission_budget":{"contract_version":"AdmissionBudgetV1","profile":"default","sampled_at":"2026-09-25T08:00:00Z","fresh_until":"2026-09-25T08:01:00Z","measurements":[{"metric":"trace_opensearch_capacity","source":"opensearch","sample_time":"2026-09-25T08:00:00Z","value":0.5,"threshold":0.8,"fresh":true}]}}`)},
		"https://safe.internal/token":                {status: http.StatusOK, body: []byte(`{"access_token":"token-1","token_type":"Bearer","expires_in":300}`)},
		"https://safe.internal/operations/op-42:ack": {status: http.StatusNoContent},
	}}
	p, _ := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config", TokenURL: "https://safe.internal/token",
		AckURLBase: "https://safe.internal/operations/", ClientID: "trace-gateway", ClientSecret: "secret", Audience: "cluster-a", CurrentKeyID: "k1",
		CurrentPublicKey: base64.RawStdEncoding.EncodeToString(publicKey), WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.operationID() != "op-42" {
		t.Fatalf("operation candidate = %q, want op-42", p.operationID())
	}
	if len(transport.requests) != 4 {
		t.Fatalf("request count = %d, want token + policy + config + ack", len(transport.requests))
	}
}

func TestFactorySupportsTracesOnly(t *testing.T) {
	factory := NewFactory()
	if factory.Type() != component.MustNewType("traceadmission") {
		t.Fatalf("factory type = %s", factory.Type())
	}
	if _, err := factory.CreateLogs(context.Background(), processor.Settings{}, factory.CreateDefaultConfig(), mustLogsConsumer(t)); err == nil {
		t.Fatal("trace admission processor unexpectedly supports logs")
	}
}

func TestProcessorDropsWhenSignedSnapshotExpires(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	snapshot, err := traceadmissionsvc.SignSnapshot(traceadmissionsvc.SignedSnapshot{
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeEnabled, EvidenceAdmission: traceadmissionsvc.ModeEnabled,
		IssuedAt: clock.Add(-time.Second), ExpiresAt: clock.Add(time.Second), KeyID: "k1", Audience: "cluster-a",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy": {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config": {status: http.StatusOK, body: []byte(`{"revision":42,"operation":{"id":"op-42","phase":"enabling"}}`)},
		"https://safe.internal/token":  {status: http.StatusOK, body: []byte(`{"access_token":"token-1","token_type":"Bearer","expires_in":300}`)},
	}}
	p, calls := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config", TokenURL: "https://safe.internal/token",
		ClientID: "trace-gateway", ClientSecret: "secret", Audience: "cluster-a", CurrentKeyID: "k1",
		CurrentPublicKey: base64.RawStdEncoding.EncodeToString(publicKey), WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	p.now = func() time.Time { return clock }
	p.gateway = traceadmissionsvc.NewGateway(traceadmissionsvc.GatewayConfig{Audience: "cluster-a", CurrentKeyID: "k1", CurrentKey: publicKey, Now: func() time.Time { return clock }})
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Second)
	if err := p.ConsumeTraces(context.Background(), testTraces(2)); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("expired snapshot forwarded spans: %d", atomic.LoadInt32(calls))
	}
}

func TestProcessorDoesNotAckWhenConfigurationRevisionDoesNotMatchSnapshot(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	snapshot, err := traceadmissionsvc.SignSnapshot(traceadmissionsvc.SignedSnapshot{
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeEnabled, EvidenceAdmission: traceadmissionsvc.ModeEnabled,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute), KeyID: "k1", Audience: "cluster-a",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy": {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config": {status: http.StatusOK, body: []byte(`{"revision":41,"operation":{"id":"op-41","phase":"enabling"}}`)},
		"https://safe.internal/token":  {status: http.StatusOK, body: []byte(`{"access_token":"token-1","token_type":"Bearer","expires_in":300}`)},
	}}
	p, _ := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config", TokenURL: "https://safe.internal/token",
		ClientID: "trace-gateway", ClientSecret: "secret", Audience: "cluster-a", CurrentKeyID: "k1",
		CurrentPublicKey: base64.RawStdEncoding.EncodeToString(publicKey), WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	p.now = func() time.Time { return now }
	p.gateway = traceadmissionsvc.NewGateway(traceadmissionsvc.GatewayConfig{Audience: "cluster-a", CurrentKeyID: "k1", CurrentKey: publicKey, Now: p.now})
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, request := range transport.requests {
		if strings.HasSuffix(request.URL.Path, ":ack") {
			t.Fatalf("ACK sent for mismatched configuration revision: %s", request.URL.Path)
		}
	}
}

func newTestProcessor(t *testing.T, config Config, transport *scriptedTransport) (*traceAdmissionProcessor, *int32) {
	t.Helper()
	var forwarded int32
	next, err := consumer.NewTraces(func(context.Context, ptrace.Traces) error {
		atomic.AddInt32(&forwarded, 1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := newProcessorWithClient(config, next, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC) }
	publicKey, err := decodePublicKey(config.CurrentPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	p.gateway = traceadmissionsvc.NewGateway(traceadmissionsvc.GatewayConfig{
		Audience: config.Audience, CurrentKeyID: config.CurrentKeyID, CurrentKey: ed25519.PublicKey(publicKey),
		Now: p.now,
	})
	return p, &forwarded
}

func testTraces(spans int) ptrace.Traces {
	traces := ptrace.NewTraces()
	ss := traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty()
	for i := 0; i < spans; i++ {
		ss.Spans().AppendEmpty()
	}
	return traces
}

type scriptedResponse struct {
	status int
	body   []byte
}

type scriptedTransport struct {
	responses map[string]scriptedResponse
	requests  []*http.Request
	bodies    map[string][]byte
}

func (s *scriptedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	s.requests = append(s.requests, request.Clone(request.Context()))
	if s.bodies == nil {
		s.bodies = map[string][]byte{}
	}
	var body []byte
	if request.Body != nil {
		body, _ = io.ReadAll(request.Body)
	}
	s.bodies[request.URL.Path] = body
	response, ok := s.responses[request.URL.String()]
	if !ok {
		response = s.responses[request.URL.Scheme+"://"+request.URL.Host+request.URL.Path]
	}
	if response.status == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return &http.Response{StatusCode: response.status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(response.body)), Request: request}, nil
}

func (s *scriptedTransport) requestBody(path string) []byte { return s.bodies[path] }

func mustLogsConsumer(t *testing.T) consumer.Logs {
	t.Helper()
	logs, err := consumer.NewLogs(func(context.Context, plog.Logs) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return logs
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}
