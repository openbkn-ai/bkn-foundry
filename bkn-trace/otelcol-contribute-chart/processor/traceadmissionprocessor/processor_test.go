// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full
// license text.

package traceadmissionprocessor

import (
	"bytes"
	"context"
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
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRefreshLogsPolicyFailureWithoutSensitiveDetails(t *testing.T) {
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy": {status: http.StatusBadGateway, body: []byte(`{"token":"must-not-log"}`)},
	}}
	p, _ := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config",
		WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	core, logs := observer.New(zap.InfoLevel)
	setTestLogger(p, zap.New(core))
	if err := p.refresh(context.Background()); err == nil {
		t.Fatal("refresh unexpectedly succeeded")
	}
	entries := logs.All()
	var policyLog *observer.LoggedEntry
	for i := range entries {
		if entries[i].ContextMap()["stage"] == "policy" {
			policyLog = &entries[i]
		}
	}
	if policyLog == nil || len(policyLog.Context) < 2 || policyLog.Context[1].Integer != http.StatusBadGateway {
		t.Fatalf("unexpected control failure log: %+v", entries)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Message, "must-not-log") || strings.Contains(entry.Message, "secret") {
			t.Fatalf("sensitive detail leaked into log: %+v", entry)
		}
	}
}

func TestRefreshLogsHeartbeatAndAckFailures(t *testing.T) {
	snapshot := traceadmissionsvc.Snapshot{ContractVersion: "TraceEvidencePolicySnapshotV1",
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeDisabled, EvidenceAdmission: traceadmissionsvc.ModeDisabled,
		IssuedAt: time.Date(2026, 9, 25, 7, 59, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC),
	}
	for _, test := range []struct {
		name, heartbeatURL, ackURL string
		wantStage                  string
	}{
		{name: "heartbeat", heartbeatURL: "https://safe.internal/heartbeat", wantStage: "heartbeat"},
		{name: "ack", ackURL: "https://safe.internal/operations/", wantStage: "ack"},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &scriptedTransport{responses: map[string]scriptedResponse{
				"https://safe.internal/policy":               {status: http.StatusOK, body: mustJSON(snapshot)},
				"https://safe.internal/config":               {status: http.StatusOK, body: frozenConfigurationBody(42, "op-42")},
				"https://safe.internal/heartbeat":            {status: http.StatusServiceUnavailable},
				"https://safe.internal/operations/op-42:ack": {status: http.StatusServiceUnavailable},
			}}
			config := Config{
				PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config",
				HeartbeatURL: test.heartbeatURL, AckURLBase: test.ackURL,
				WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
			}
			p, _ := newTestProcessor(t, config, transport)
			core, logs := observer.New(zap.InfoLevel)
			setTestLogger(p, zap.New(core))
			if err := p.refresh(context.Background()); err == nil {
				t.Fatal("refresh unexpectedly succeeded")
			}
			var found bool
			for _, entry := range logs.All() {
				if entry.ContextMap()["stage"] == test.wantStage && entry.Context[1].Integer == http.StatusServiceUnavailable {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s failure log: %+v", test.wantStage, logs.All())
			}
		})
	}
}

func TestProcessorCallsInternalEndpointsWithoutToken(t *testing.T) {
	snapshot := traceadmissionsvc.Snapshot{ContractVersion: "TraceEvidencePolicySnapshotV1",
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeDisabled, EvidenceAdmission: traceadmissionsvc.ModeDisabled,
		IssuedAt: time.Date(2026, 9, 25, 7, 59, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC),
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy": {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config": {status: http.StatusOK, body: frozenConfigurationBody(42, "")},
	}}
	p, _ := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config",
		WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	core, logs := observer.New(zap.InfoLevel)
	setTestLogger(p, zap.New(core))
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(logs.All()) != 0 {
		t.Fatalf("normal auth-style fallback emitted failure logs: %+v", logs.All())
	}
	if len(transport.requests) != 2 {
		t.Fatalf("request count = %d, want policy + config", len(transport.requests))
	}
}

func TestProcessorUsesFrozenPolicySnapshotAndFailsClosedForLegacyField(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	snapshot := traceadmissionsvc.Snapshot{ContractVersion: "TraceEvidencePolicySnapshotV1",
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeEnabled, EvidenceAdmission: traceadmissionsvc.ModeEnabled,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute),
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy": {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config": {status: http.StatusOK, body: frozenConfigurationBody(42, "op-42")},
	}}
	p, calls := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config",
		WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
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

func TestProcessorUsesUnauthenticatedHeartbeatAndAck(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	snapshot := traceadmissionsvc.Snapshot{ContractVersion: "TraceEvidencePolicySnapshotV1",
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeDisabled, EvidenceAdmission: traceadmissionsvc.ModeDisabled,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute),
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy":               {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config":               {status: http.StatusOK, body: frozenConfigurationBody(42, "op-42")},
		"https://safe.internal/heartbeat":            {status: http.StatusNoContent},
		"https://safe.internal/operations/op-42:ack": {status: http.StatusNoContent},
	}}
	p, _ := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config",
		HeartbeatURL: "https://safe.internal/heartbeat", AckURLBase: "https://safe.internal/operations/",
		WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(transport.requests) != 4 {
		t.Fatalf("request count = %d, want policy + config + heartbeat + ack", len(transport.requests))
	}
	for _, request := range transport.requests {
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("request %s unexpected authorization", request.URL.Path)
		}
	}
	var heartbeat heartbeatRequest
	if err := json.Unmarshal(transport.requestBody("/heartbeat"), &heartbeat); err != nil {
		t.Fatal(err)
	}
	if heartbeat.EndpointKind != "trace_gateway" || heartbeat.WorkloadIdentity != "trace-gateway" || heartbeat.InstanceID != "trace-gateway#boot-42" {
		t.Fatalf("unexpected internal heartbeat identity: %+v", heartbeat)
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
	if string(wire["contract_version"]) != `"TraceGatewayAcknowledgementV1"` {
		t.Fatalf("frozen ACK contract_version = %s", wire["contract_version"])
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
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	snapshot := traceadmissionsvc.Snapshot{ContractVersion: "TraceEvidencePolicySnapshotV1",
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeDisabled, EvidenceAdmission: traceadmissionsvc.ModeDisabled,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute),
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy":               {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config":               {status: http.StatusOK, body: frozenConfigurationBody(42, "op-42")},
		"https://safe.internal/operations/op-42:ack": {status: http.StatusNoContent},
	}}
	p, _ := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config",
		AckURLBase:       "https://safe.internal/operations/",
		WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.operationID() != "op-42" {
		t.Fatalf("operation candidate = %q, want op-42", p.operationID())
	}
	if len(transport.requests) != 3 {
		t.Fatalf("request count = %d, want policy + config + ack", len(transport.requests))
	}
}

func TestProcessorRejectsLegacyConfigurationReadModel(t *testing.T) {
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/config": {status: http.StatusOK, body: []byte(`{"revision":42,"operation":{"id":"op-42","phase":"enabling"}}`)},
	}}
	p, _ := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config",
		WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	if _, err := p.pullActiveOperation(context.Background(), 42); err == nil {
		t.Fatal("legacy configuration read model was accepted")
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

func TestProcessorDropsWhenSnapshotExpires(t *testing.T) {
	clock := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	snapshot := traceadmissionsvc.Snapshot{ContractVersion: "TraceEvidencePolicySnapshotV1",
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeEnabled, EvidenceAdmission: traceadmissionsvc.ModeEnabled,
		IssuedAt: clock.Add(-time.Second), ExpiresAt: clock.Add(time.Second),
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy": {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config": {status: http.StatusOK, body: frozenConfigurationBody(42, "op-42")},
	}}
	p, calls := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config",
		WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	p.now = func() time.Time { return clock }
	p.gateway = traceadmissionsvc.NewGateway(traceadmissionsvc.GatewayConfig{Now: func() time.Time { return clock }})
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
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	snapshot := traceadmissionsvc.Snapshot{ContractVersion: "TraceEvidencePolicySnapshotV1",
		Revision: 42, TraceAdmission: traceadmissionsvc.ModeEnabled, EvidenceAdmission: traceadmissionsvc.ModeEnabled,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute),
	}
	transport := &scriptedTransport{responses: map[string]scriptedResponse{
		"https://safe.internal/policy": {status: http.StatusOK, body: mustJSON(snapshot)},
		"https://safe.internal/config": {status: http.StatusOK, body: frozenConfigurationBody(41, "op-41")},
	}}
	p, _ := newTestProcessor(t, Config{
		PolicyURL: "https://safe.internal/policy", ConfigurationURL: "https://safe.internal/config",
		WorkloadIdentity: "trace-gateway", ProcessBootID: "boot-42",
	}, transport)
	p.now = func() time.Time { return now }
	p.gateway = traceadmissionsvc.NewGateway(traceadmissionsvc.GatewayConfig{Now: p.now})
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, request := range transport.requests {
		if strings.HasSuffix(request.URL.Path, ":ack") {
			t.Fatalf("ACK sent for mismatched configuration revision: %s", request.URL.Path)
		}
	}
}

func frozenConfigurationBody(revision uint64, operationID string) []byte {
	return mustJSON(map[string]any{
		"kind": "configuration_get", "desired_state": "disabled", "effective_state": "disabled",
		"policy_revision": revision, "last_stable_revision": revision - 1, "active_operation_id": operationID,
		"heartbeat_interval_seconds": 10, "lease_ttl_seconds": 30,
		"admission_budget": map[string]any{
			"contract_version": "AdmissionBudgetV1", "profile": "default",
			"sampled_at": "2026-09-25T08:00:00Z", "fresh_until": "2026-09-25T08:01:00Z",
			"measurements": []any{
				map[string]any{"metric": "trace_opensearch_capacity", "source": "opensearch", "sample_time": "2026-09-25T08:00:00Z", "value": 0.5, "threshold": 0.8, "fresh": true},
				map[string]any{"metric": "trace_opensearch_heap", "source": "opensearch", "sample_time": "2026-09-25T08:00:00Z", "value": 0.5, "threshold": 0.8, "fresh": true},
				map[string]any{"metric": "trace_collector_queue", "source": "collector", "sample_time": "2026-09-25T08:00:00Z", "value": 0.5, "threshold": 0.8, "fresh": true},
				map[string]any{"metric": "trace_storage_connection_pool", "source": "mariadb", "sample_time": "2026-09-25T08:00:00Z", "value": 0.5, "threshold": 0.8, "fresh": true},
			},
		},
	})
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
	p.gateway = traceadmissionsvc.NewGateway(traceadmissionsvc.GatewayConfig{Now: p.now})
	return p, &forwarded
}

func setTestLogger(p *traceAdmissionProcessor, logger *zap.Logger) {
	p.logger = logger
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
