// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysnapshot"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceadmissionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

type capturePolicyInternalWriter struct {
	lease icapturepolicy.EndpointLease
	ack   icapturepolicy.ExpectedAcknowledgement
}

func (w *capturePolicyInternalWriter) UpsertEndpointLease(_ context.Context, lease icapturepolicy.EndpointLease) error {
	w.lease = lease
	return nil
}

func (w *capturePolicyInternalWriter) RecordAcknowledgement(_ context.Context, ack icapturepolicy.ExpectedAcknowledgement) error {
	w.ack = ack
	return nil
}

func capturePolicyWorkloadRequest(method, url, body string, profile evidencevo.AccessProfile) *http.Request {
	scope := evidencevo.QueryScope{AccountID: "svc-account", AccountType: "service", AccessProfile: &profile}
	request := httptest.NewRequest(method, url, bytes.NewBufferString(body))
	return request.WithContext(context.WithValue(request.Context(), trustedQueryScopeContextKey{}, scope))
}

func capturePolicyWorkloadProfile() evidencevo.AccessProfile {
	return evidencevo.AccessProfile{
		ApplicationPrincipalID: "spiffe://cluster-a/ns/openbkn/sa/otelcol",
		AccountActive:          true,
		Permissions:            []evidencevo.Permission{{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointTraceGateway, Operations: []string{"heartbeat"}}},
	}
}

func TestInternalTraceEvidenceHeartbeatBindsEndpointKindToVerifiedGrant(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", `{"instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","process_boot_id":"boot-1","observed_revision":42,"ready":true}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if writer.lease.EndpointKind != icapturepolicy.EndpointTraceGateway || writer.lease.WorkloadIdentity != "spiffe://cluster-a/ns/openbkn/sa/otelcol" {
		t.Fatalf("heartbeat was not bound to verified gateway grant: %+v", writer.lease)
	}
}

func TestInternalTraceEvidenceHeartbeatRejectsUnboundEndpointKind(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	profile := capturePolicyWorkloadProfile()
	profile.Permissions = nil
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", `{"instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","process_boot_id":"boot-1","observed_revision":42,"ready":true}`, profile)
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when verified principal has no endpoint grant", response.Code)
	}
}

func TestInternalTraceEvidenceHeartbeatRejectsCallerSuppliedEndpointKind(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", `{"endpoint_kind":"evidence_publisher","instance_id":"publisher#boot-1","process_boot_id":"boot-1","observed_revision":42,"ready":true}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when endpoint_kind is caller-supplied", response.Code)
	}
}

func TestInternalTraceGatewayAckRejectsOmittedRequiredQueueCount(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return capturepolicysvc.Snapshot{}, nil }), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-gap:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/otelcol","process_boot_id":"boot-1","capture_policy_revision":42,"admission_state":"disabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"gap","exported":16,"dropped":2,"gap_reason":"collector_restarted"}}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when required unaccounted field is omitted", response.Code)
	}
}

func TestInternalTraceEvidenceHeartbeatRequiresEndpointGrant(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	profile := capturePolicyWorkloadProfile()
	profile.Permissions = nil
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", `{"instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","process_boot_id":"boot-1","observed_revision":42,"ready":true}`, profile)
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestInternalTraceEvidenceHeartbeatRejectsBootIdentityMismatch(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", `{"instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-other","process_boot_id":"boot-1","observed_revision":42,"ready":true}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestInternalTraceGatewayAckConsumesFrozenContractAndBindsIdentity(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{}, nil
	}), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-42:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/otelcol","process_boot_id":"boot-1","capture_policy_revision":42,"admission_state":"disabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"complete","exported":16,"dropped":2,"unaccounted":0}}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if writer.ack.OperationID != "op-42" || writer.ack.EndpointKind != icapturepolicy.EndpointTraceGateway || writer.ack.TraceDisposition != icapturepolicy.DispositionComplete || writer.ack.ExportedCount == nil || *writer.ack.ExportedCount != 16 {
		t.Fatalf("unexpected persisted acknowledgement: %+v", writer.ack)
	}
}

func TestInternalTraceGatewayAckRejectsWorkloadIdentityMismatch(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return capturepolicysvc.Snapshot{}, nil }), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-42:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/other#boot-1","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/other","process_boot_id":"boot-1","capture_policy_revision":42,"admission_state":"enabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"not_applicable","exported":0,"dropped":0,"unaccounted":0}}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestInternalTraceGatewayAckAcceptsFrozenGapDisposition(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return capturepolicysvc.Snapshot{}, nil }), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-gap:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/otelcol","process_boot_id":"boot-1","capture_policy_revision":42,"admission_state":"disabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"gap","exported":16,"dropped":2,"unaccounted":null,"gap_reason":"collector_restarted"}}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusNoContent || writer.ack.TraceDisposition != icapturepolicy.DispositionGap || writer.ack.GapReason != "collector_restarted" {
		t.Fatalf("status = %d, body = %s, ack = %+v", response.Code, response.Body.String(), writer.ack)
	}
}

func TestInternalTraceGatewayAckRejectsInvalidGapReason(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return capturepolicysvc.Snapshot{}, nil }), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-gap:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/otelcol","process_boot_id":"boot-1","capture_policy_revision":42,"admission_state":"disabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"gap","exported":16,"dropped":2,"unaccounted":null,"gap_reason":"made_up"}}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestInternalTraceGatewayAckRejectsGapWithoutReason(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return capturepolicysvc.Snapshot{}, nil }), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-gap:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/otelcol","process_boot_id":"boot-1","capture_policy_revision":42,"admission_state":"disabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"gap","exported":16,"dropped":2,"unaccounted":null}}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestInternalTraceGatewayAckRequiresGatewayCapabilityAndExactBootBinding(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return capturepolicysvc.Snapshot{}, nil }), nil, nil, nil, writer)
	profile := capturePolicyWorkloadProfile()
	profile.Permissions = []evidencevo.Permission{{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointEvidencePublisher, Operations: []string{"heartbeat"}}}
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-42:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-other","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/otelcol","process_boot_id":"boot-1","capture_policy_revision":42,"admission_state":"enabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"not_applicable","exported":0,"dropped":0,"unaccounted":0}}`, profile)
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestInternalEvidencePublisherAckConsumesSession1FixtureShape(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{
			Revision: 41, DesiredState: capturepolicysvc.StateDisabled, EffectiveState: capturepolicysvc.StateEnabled,
			LastStableRevision: 40,
			Operation:          capturepolicysvc.Operation{ID: "op-publisher", Phase: capturepolicysvc.PhaseDisabling, RequestedState: capturepolicysvc.StateDisabled},
		}, nil
	}), nil, nil, nil, writer)
	profile := capturePolicyWorkloadProfile()
	profile.ApplicationPrincipalID = "spiffe://cluster.local/ns/openbkn/sa/bkn-backend"
	profile.Permissions = []evidencevo.Permission{{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointEvidencePublisher, Operations: []string{"heartbeat"}}}
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-publisher:publisher-ack", `{"producer_instance_id":"spiffe://cluster.local/ns/openbkn/sa/bkn-backend#aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","capture_policy_revision":41,"last_accepted_sequence":7,"published":5,"dropped":2,"queue_empty":true,"acknowledged_at":"2026-09-22T08:00:10.000Z"}`, profile)
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if writer.ack.EndpointKind != icapturepolicy.EndpointEvidencePublisher || writer.ack.AckState != icapturepolicy.AckDisabled || writer.ack.EvidenceDisposition != icapturepolicy.DispositionComplete || writer.ack.PublishedCount == nil || *writer.ack.PublishedCount != 5 {
		t.Fatalf("unexpected publisher acknowledgement: %+v", writer.ack)
	}
}

func TestInternalEvidencePublisherAckUsesReadyStateForEnabledPolicy(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	reader := capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{
			Revision: 42, DesiredState: capturepolicysvc.StateEnabled, EffectiveState: capturepolicysvc.StateDisabled,
			LastStableRevision: 41,
			Operation:          capturepolicysvc.Operation{ID: "op-enable", Phase: capturepolicysvc.PhaseEnabling, RequestedState: capturepolicysvc.StateEnabled},
		}, nil
	})
	handler := NewCapturePolicyHandlerWithInternal(reader, nil, nil, nil, writer)
	profile := capturePolicyWorkloadProfile()
	profile.ApplicationPrincipalID = "spiffe://cluster.local/ns/openbkn/sa/bkn-backend"
	profile.Permissions = []evidencevo.Permission{{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointEvidencePublisher, Operations: []string{"heartbeat"}}}
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-enable:publisher-ack", `{"producer_instance_id":"spiffe://cluster.local/ns/openbkn/sa/bkn-backend#boot-42","capture_policy_revision":42,"last_accepted_sequence":0,"published":0,"dropped":0,"queue_empty":true,"acknowledged_at":"2026-09-22T08:00:10.000Z"}`, profile)
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if writer.ack.AckState != icapturepolicy.AckReady || writer.ack.EvidenceDisposition != icapturepolicy.DispositionNotApplicable {
		t.Fatalf("enabled policy did not produce a ready publisher acknowledgement: %+v", writer.ack)
	}
}

func TestSession1PublisherAcknowledgementFixtureCompatibility(t *testing.T) {
	path := os.Getenv("BKN_DOCS_EVIDENCE_PUBLISHER_ACK_FIXTURE")
	if path == "" {
		t.Skip("set BKN_DOCS_EVIDENCE_PUBLISHER_ACK_FIXTURE to the committed Session 1 fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if got := fmt.Sprintf("%x", digest[:]); got != "73cb43f2869de66bca070e4de9928f2a3c47116c9509ed51608076b6b94e84e9" {
		t.Fatalf("fixture digest = %s, contract digest changed", got)
	}
	var fixture struct {
		ContractSHA      string `json:"contract_sha"`
		Acknowledgements []struct {
			Ack evidencePublisherAcknowledgement `json:"ack"`
		} `json:"acknowledgements"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.ContractSHA != "0016ad359b11d162e04bb11a78784c33fad0ec8d" || len(fixture.Acknowledgements) != 2 {
		t.Fatalf("unexpected fixture envelope: %+v", fixture)
	}
	for _, item := range fixture.Acknowledgements {
		if item.Ack.ProducerInstanceID == "" || item.Ack.PolicyRevision == 0 || item.Ack.LastAcceptedSequence != item.Ack.Published+item.Ack.Dropped || !item.Ack.QueueEmpty {
			t.Fatalf("fixture ack is not consumable by the Session 1 adapter: %+v", item.Ack)
		}
	}
}

func TestInternalTracePolicySnapshotUsesDedicatedSigner(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	signer := capturepolicysnapshot.Signer{PrivateKey: privateKey, KeyID: "capture-2026", Audience: "cluster-a", TTL: time.Minute, Now: func() time.Time { return now }}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled, EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 42, Operation: capturepolicysvc.Operation{ID: "op-42", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateEnabled}}, nil
	}), nil, nil, signer, nil)
	request := capturePolicyWorkloadRequest(http.MethodGet, "/api/agent-observability/v1/internal/trace-evidence/policy", "", capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.GetInternalTraceEvidencePolicy(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var snapshot traceadmissionsvc.SignedSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Revision != 42 || snapshot.Signature == "" {
		t.Fatalf("unexpected signed snapshot: %+v", snapshot)
	}
}

type capturePolicyCommanderFunc func(context.Context, capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error)

func (f capturePolicyCommanderFunc) Request(ctx context.Context, request capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error) {
	return f(ctx, request)
}

type capturePolicyReconcilerFunc func(context.Context) (bool, error)

func (f capturePolicyReconcilerFunc) Reconcile(ctx context.Context) (bool, error) { return f(ctx) }

type historicalCapturePolicyReader struct {
	snapshot   capturepolicysvc.Snapshot
	operations map[string]capturepolicysvc.Operation
}

func (r historicalCapturePolicyReader) Read(context.Context) (capturepolicysvc.Snapshot, error) {
	return r.snapshot, nil
}
func (r historicalCapturePolicyReader) ReadOperation(_ context.Context, id string) (capturepolicysvc.Operation, error) {
	operation, ok := r.operations[id]
	if !ok {
		return capturepolicysvc.Operation{}, capturepolicysvc.ErrOperationNotFound
	}
	return operation, nil
}

func TestCapturePolicyHandlerReturnsTruthfulStateModel(t *testing.T) {
	handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{
			Revision:       9,
			DesiredState:   capturepolicysvc.StateEnabled,
			EffectiveState: capturepolicysvc.StateEnabling,
			Operation: capturepolicysvc.Operation{
				ID: "op-9", Phase: capturepolicysvc.PhaseEnabling, RequestedState: capturepolicysvc.StateEnabled,
			},
		}, nil
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/trace-evidence-configuration", nil)
	response := httptest.NewRecorder()
	handler.GetTraceEvidenceConfiguration(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["desired_state"] != string(capturepolicysvc.StateEnabled) || payload["effective_state"] != string(capturepolicysvc.StateEnabling) {
		t.Fatalf("truthful state fields missing: %v", payload)
	}
	operation, ok := payload["operation"].(map[string]any)
	if !ok || operation["phase"] != string(capturepolicysvc.PhaseEnabling) {
		t.Fatalf("operation phase missing: %v", payload["operation"])
	}
}

func TestCapturePolicyHandlerRejectsNonGet(t *testing.T) {
	handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{}, nil
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/trace-evidence-configuration", nil)
	response := httptest.NewRecorder()
	handler.GetTraceEvidenceConfiguration(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", response.Code)
	}
}

func TestCapturePolicyHandlerAcceptsRevisionGuardedChange(t *testing.T) {
	called := false
	handler := NewCapturePolicyHandler(
		capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
			return capturepolicysvc.Snapshot{}, nil
		}),
		capturePolicyCommanderFunc(func(_ context.Context, request capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error) {
			called = true
			if request.ExpectedRevision != 9 || request.DesiredState != capturepolicysvc.StateDisabled {
				t.Fatalf("unexpected change request: %+v", request)
			}
			return capturepolicysvc.Snapshot{
				Revision: 10, DesiredState: capturepolicysvc.StateDisabled, EffectiveState: capturepolicysvc.StateDisabling,
				LastStableRevision: 9, Operation: capturepolicysvc.Operation{ID: "op-10", Phase: capturepolicysvc.PhaseDisabling, RequestedState: capturepolicysvc.StateDisabled, ExpectedRevision: 9},
			}, nil
		}),
	)
	request := httptest.NewRequest(http.MethodPut, "/api/agent-observability/v1/trace-evidence-configuration", bytes.NewBufferString(`{"desired_state":"disabled","expected_revision":9}`))
	response := httptest.NewRecorder()
	handler.HandleTraceEvidenceConfiguration(response, request)
	if response.Code != http.StatusAccepted || !called {
		t.Fatalf("status = %d, called=%v, body=%s", response.Code, called, response.Body.String())
	}
}

func TestCapturePolicyPermissionMiddlewareSeparatesReadAndWrite(t *testing.T) {
	base := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	handler := &EvidenceHandler{}
	readOnly := evidencevo.QueryScope{AccessProfile: &evidencevo.AccessProfile{AccountActive: true, Permissions: []evidencevo.Permission{{ResourceType: "trace_evidence_configuration", ResourceID: "global", Operations: []string{"read"}}}}}
	readRequest := httptest.NewRequest(http.MethodGet, "/api/observability/v1/trace-evidence-configuration", nil)
	readRequest = readRequest.WithContext(context.WithValue(readRequest.Context(), trustedQueryScopeContextKey{}, readOnly))
	readResponse := httptest.NewRecorder()
	handler.RequireTraceEvidenceConfigurationPermission(base)(readResponse, readRequest)
	if readResponse.Code != http.StatusNoContent {
		t.Fatalf("read permission status = %d, want 204", readResponse.Code)
	}
	writeRequest := httptest.NewRequest(http.MethodPut, "/api/observability/v1/trace-evidence-configuration", nil)
	writeRequest = writeRequest.WithContext(context.WithValue(writeRequest.Context(), trustedQueryScopeContextKey{}, readOnly))
	writeResponse := httptest.NewRecorder()
	handler.RequireTraceEvidenceConfigurationPermission(base)(writeResponse, writeRequest)
	if writeResponse.Code != http.StatusForbidden {
		t.Fatalf("read-only PUT status = %d, want 403", writeResponse.Code)
	}
}

func TestCapturePolicyPermissionMiddlewareRequiresReconcileCapability(t *testing.T) {
	base := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	handler := &EvidenceHandler{}
	readOnly := evidencevo.QueryScope{AccessProfile: &evidencevo.AccessProfile{AccountActive: true, Permissions: []evidencevo.Permission{{ResourceType: "trace_evidence_configuration", ResourceID: "global", Operations: []string{"read"}}}}}
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/trace-evidence-operations/op-1:reconcile", nil).WithContext(context.WithValue(context.Background(), trustedQueryScopeContextKey{}, readOnly))
	response := httptest.NewRecorder()
	handler.RequireTraceEvidenceConfigurationPermission(base)(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read-only reconcile status = %d, want 403", response.Code)
	}
	reconcile := evidencevo.QueryScope{AccessProfile: &evidencevo.AccessProfile{AccountActive: true, Permissions: []evidencevo.Permission{{ResourceType: "trace_evidence_configuration", ResourceID: "global", Operations: []string{"reconcile"}}}}}
	request = httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/trace-evidence-operations/op-1:reconcile", nil).WithContext(context.WithValue(context.Background(), trustedQueryScopeContextKey{}, reconcile))
	response = httptest.NewRecorder()
	handler.RequireTraceEvidenceConfigurationPermission(base)(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("reconcile permission status = %d, want 204", response.Code)
	}
}

func TestCapturePolicyHandlerReadsOperationFromAuthoritativeSnapshot(t *testing.T) {
	handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 10, DesiredState: capturepolicysvc.StateDisabled, EffectiveState: capturepolicysvc.StateDisabling, LastStableRevision: 9, Operation: capturepolicysvc.Operation{ID: "op-10", Phase: capturepolicysvc.PhaseDisabling, RequestedState: capturepolicysvc.StateDisabled, ExpectedRevision: 9}}, nil
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/trace-evidence-operations/op-10", nil)
	response := httptest.NewRecorder()
	handler.GetTraceEvidenceOperation(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
}

func TestCapturePolicyHandlerParsesReconcileActionSuffix(t *testing.T) {
	called := false
	reader := capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 2, DesiredState: capturepolicysvc.StateDisabled, EffectiveState: capturepolicysvc.StateDisabling, LastStableRevision: 1, Operation: capturepolicysvc.Operation{ID: "op-2", Phase: capturepolicysvc.PhaseDisabling, RequestedState: capturepolicysvc.StateDisabled, ExpectedRevision: 1}}, nil
	})
	handler := NewCapturePolicyHandlerWithReconciler(reader, nil, capturePolicyReconcilerFunc(func(context.Context) (bool, error) { called = true; return true, nil }))
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/trace-evidence-operations/op-2:reconcile", nil)
	response := httptest.NewRecorder()
	handler.ReconcileTraceEvidenceOperation(response, request)
	if response.Code != http.StatusAccepted || !called {
		t.Fatalf("reconcile status=%d called=%v body=%s", response.Code, called, response.Body.String())
	}
}

func TestCapturePolicyHandlerReadsHistoricalOperationAfterNewOperation(t *testing.T) {
	reader := historicalCapturePolicyReader{
		snapshot: capturepolicysvc.Snapshot{Revision: 3, DesiredState: capturepolicysvc.StateDisabled, EffectiveState: capturepolicysvc.StateDisabling, LastStableRevision: 1, Operation: capturepolicysvc.Operation{ID: "op-2", Phase: capturepolicysvc.PhaseDisabling, RequestedState: capturepolicysvc.StateDisabled, ExpectedRevision: 2}},
		operations: map[string]capturepolicysvc.Operation{
			"op-1": {ID: "op-1", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateDisabled, ExpectedRevision: 1},
			"op-2": {ID: "op-2", Phase: capturepolicysvc.PhaseDisabling, RequestedState: capturepolicysvc.StateDisabled, ExpectedRevision: 2},
		},
	}
	handler := NewCapturePolicyHandler(reader)
	request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/trace-evidence-operations/op-1", nil)
	response := httptest.NewRecorder()
	handler.GetTraceEvidenceOperation(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("historical operation status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var operation capturepolicysvc.Operation
	if err := json.Unmarshal(response.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	if operation.ID != "op-1" || operation.Phase != capturepolicysvc.PhaseSucceeded {
		t.Fatalf("unexpected historical operation: %+v", operation)
	}
}
