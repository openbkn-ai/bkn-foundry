// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysnapshot"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceadmissionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

type capturePolicyInternalWriter struct {
	lease                  icapturepolicy.EndpointLease
	ack                    icapturepolicy.ExpectedAcknowledgement
	leaseUpserts           int
	registeredHeartbeats   int
	registeredHeartbeatErr error
	publisherClosureAcks   int
	recordErr              error
}

type validatingGatewayWriter struct{ capturePolicyInternalWriter }

func (w *validatingGatewayWriter) RecordAcknowledgement(_ context.Context, ack icapturepolicy.ExpectedAcknowledgement) error {
	w.ack = ack
	return ack.Validate()
}

type capturePolicyBudgetReader struct{}

func (capturePolicyBudgetReader) ReadAdmissionBudget(context.Context) (capturepolicysvc.AdmissionBudget, error) {
	now := time.Now().UTC()
	return capturepolicysvc.AdmissionBudget{
		ContractVersion: "AdmissionBudgetV1", Profile: "default", SampledAt: now, FreshUntil: now.Add(time.Minute),
		Measurements: []capturepolicysvc.AdmissionMeasurement{
			{Metric: "trace_opensearch_capacity", Source: "opensearch", SampleTime: now, Value: 0.5, Threshold: 0.8, Fresh: true},
			{Metric: "trace_opensearch_heap", Source: "opensearch", SampleTime: now, Value: 0.5, Threshold: 0.8, Fresh: true},
			{Metric: "trace_collector_queue", Source: "collector", SampleTime: now, Value: 0.5, Threshold: 0.8, Fresh: true},
			{Metric: "trace_storage_connection_pool", Source: "mariadb", SampleTime: now, Value: 0.5, Threshold: 0.8, Fresh: true},
		},
	}, nil
}

type countingCapturePolicyBudgetReader struct {
	calls  int
	budget capturepolicysvc.AdmissionBudget
	err    error
}

func (reader *countingCapturePolicyBudgetReader) ReadAdmissionBudget(context.Context) (capturepolicysvc.AdmissionBudget, error) {
	reader.calls++
	return reader.budget, reader.err
}

func marshalJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (w *capturePolicyInternalWriter) UpsertEndpointLease(_ context.Context, lease icapturepolicy.EndpointLease) error {
	w.leaseUpserts++
	w.lease = lease
	return nil
}

func (w *capturePolicyInternalWriter) RegisterEvidencePublisherHeartbeat(_ context.Context, lease icapturepolicy.EndpointLease) error {
	w.registeredHeartbeats++
	w.lease = lease
	return w.registeredHeartbeatErr
}

func (w *capturePolicyInternalWriter) RecordAcknowledgement(_ context.Context, ack icapturepolicy.ExpectedAcknowledgement) error {
	w.ack = ack
	return w.recordErr
}

func (w *capturePolicyInternalWriter) RecordEvidencePublisherAcknowledgement(_ context.Context, ack icapturepolicy.ExpectedAcknowledgement, _ time.Time) error {
	w.publisherClosureAcks++
	w.ack = ack
	return nil
}

func TestCapturePolicyControlWriterIncludesEvidencePublisherHeartbeat(t *testing.T) {
	var writer CapturePolicyControlWriter = &capturePolicyInternalWriter{}
	lease := icapturepolicy.EndpointLease{EndpointKind: icapturepolicy.EndpointEvidencePublisher}
	if err := writer.RegisterEvidencePublisherHeartbeat(context.Background(), lease); err != nil {
		t.Fatalf("RegisterEvidencePublisherHeartbeat() error = %v", err)
	}
}

func capturePolicyWorkloadRequest(method, url, body string, profile evidencevo.AccessProfile) *http.Request {
	if strings.HasSuffix(url, "endpoints:heartbeat") {
		var payload map[string]any
		if json.Unmarshal([]byte(body), &payload) == nil {
			payload["workload_identity"] = profile.ApplicationPrincipalID
			if _, supplied := payload["endpoint_kind"]; !supplied {
				kind := icapturepolicy.EndpointTraceGateway
				for _, permission := range profile.Permissions {
					if permission.ResourceID == icapturepolicy.EndpointEvidencePublisher {
						kind = icapturepolicy.EndpointEvidencePublisher
					}
				}
				payload["endpoint_kind"] = kind
			}
			encoded, _ := json.Marshal(payload)
			body = string(encoded)
		}
	}
	return httptest.NewRequest(method, url, bytes.NewBufferString(body))
}

func capturePolicyWorkloadProfile() evidencevo.AccessProfile {
	return evidencevo.AccessProfile{
		ApplicationPrincipalID: "spiffe://cluster-a/ns/openbkn/sa/otelcol",
		AccountActive:          true,
		Permissions:            []evidencevo.Permission{{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointTraceGateway, Operations: []string{"heartbeat"}}},
	}
}

func traceEvidenceAppProfile(principalID string, permissions ...evidencevo.Permission) evidencevo.AccessProfile {
	return evidencevo.AccessProfile{ActorID: principalID, EffectiveSubjectID: principalID, ApplicationPrincipalID: principalID, AccountActive: true, Permissions: permissions}
}

func traceGatewayAckReader(operationID string, revision uint64, phase capturepolicysvc.Phase) capturepolicysvc.ReaderFunc {
	return func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{
			Revision: revision, DesiredState: capturepolicysvc.StateDisabled,
			EffectiveState: capturepolicysvc.StateDisabled, LastStableRevision: revision,
			Operation: capturepolicysvc.Operation{ID: operationID, Phase: phase, RequestedState: capturepolicysvc.StateDisabled},
		}, nil
	}
}

type traceGatewayAckRaceReader struct{}

func (traceGatewayAckRaceReader) Read(context.Context) (capturepolicysvc.Snapshot, error) {
	return capturepolicysvc.Snapshot{
		Revision: 42, DesiredState: capturepolicysvc.StateDisabled,
		EffectiveState: capturepolicysvc.StateDisabled, LastStableRevision: 42,
		Operation: capturepolicysvc.Operation{ID: "op-race", Phase: capturepolicysvc.PhaseDisabling, RequestedState: capturepolicysvc.StateDisabled},
	}, nil
}

func (traceGatewayAckRaceReader) ReadOperation(context.Context, string) (capturepolicysvc.Operation, error) {
	return capturepolicysvc.Operation{ID: "op-race", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateDisabled}, nil
}

func TestInternalTraceEvidenceHeartbeatUsesBodyEndpointKind(t *testing.T) {
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
	if writer.registeredHeartbeats != 0 || writer.leaseUpserts != 1 {
		t.Fatalf("Gateway heartbeat must remain lease-only: registered=%d lease-only=%d", writer.registeredHeartbeats, writer.leaseUpserts)
	}
}

func TestInternalTraceEvidencePublisherHeartbeatRegistersAtomically(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	profile := traceEvidenceAppProfile("bkn-backend", evidencevo.Permission{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointEvidencePublisher, Operations: []string{"heartbeat"}})
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", `{"instance_id":"bkn-backend#boot-7","process_boot_id":"boot-7","observed_revision":42,"ready":true}`, profile)
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if writer.registeredHeartbeats != 1 || writer.leaseUpserts != 0 {
		t.Fatalf("Publisher heartbeat must use the atomic lease+registration writer: registered=%d lease-only=%d", writer.registeredHeartbeats, writer.leaseUpserts)
	}
	if writer.lease.EndpointKind != icapturepolicy.EndpointEvidencePublisher || writer.lease.InstanceID != "bkn-backend#boot-7" || writer.lease.ObservedRevision != 42 || !writer.lease.Ready {
		t.Fatalf("registered heartbeat was not bound to verified identity and revision: %+v", writer.lease)
	}
}

func TestInternalTraceEvidencePublisherHeartbeatDoesNotRegisterWhenNotReady(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	profile := traceEvidenceAppProfile("bkn-backend", evidencevo.Permission{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointEvidencePublisher, Operations: []string{"heartbeat"}})
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", `{"instance_id":"bkn-backend#boot-7","process_boot_id":"boot-7","observed_revision":42,"ready":false}`, profile)
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if writer.registeredHeartbeats != 0 || writer.leaseUpserts != 1 || writer.lease.Ready {
		t.Fatalf("not-ready heartbeat must update only the non-ready lease: registered=%d lease-only=%d lease=%+v", writer.registeredHeartbeats, writer.leaseUpserts, writer.lease)
	}
}

func TestInternalTraceEvidencePublisherHeartbeatDoesNotFallbackWhenRegistrationFails(t *testing.T) {
	writer := &capturePolicyInternalWriter{registeredHeartbeatErr: errors.New("registration rejected")}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	profile := traceEvidenceAppProfile("bkn-backend", evidencevo.Permission{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointEvidencePublisher, Operations: []string{"heartbeat"}})
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", `{"instance_id":"bkn-backend#boot-7","process_boot_id":"boot-7","observed_revision":42,"ready":true}`, profile)
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusConflict || writer.registeredHeartbeats != 1 || writer.leaseUpserts != 0 {
		t.Fatalf("failed atomic registration must fail closed without lease-only fallback: status=%d registered=%d lease-only=%d body=%s", response.Code, writer.registeredHeartbeats, writer.leaseUpserts, response.Body.String())
	}
}

func TestInternalTraceEvidenceHeartbeatRejectsInconsistentBodyIdentity(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", `{"endpoint_kind":"evidence_publisher","instance_id":"publisher#boot-1","process_boot_id":"boot-1","observed_revision":42,"ready":true}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for inconsistent body identity", response.Code)
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

func TestInternalTraceEvidenceHeartbeatRejectsBootIdentityMismatch(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", `{"instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-other","process_boot_id":"boot-1","observed_revision":42,"ready":true}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestInternalTraceGatewayAckConsumesFrozenContractAndBindsIdentity(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(traceGatewayAckReader("op-42", 42, capturepolicysvc.PhaseDisabling), nil, nil, nil, writer)
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

func TestInternalTraceGatewayEnabledAckMapsRequiredWireZerosToNotApplicableStoreFields(t *testing.T) {
	writer := &validatingGatewayWriter{}
	handler := NewCapturePolicyHandlerWithInternal(traceGatewayAckReader("op-enable", 43, capturepolicysvc.PhaseEnabling), nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-enable:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/otelcol","process_boot_id":"boot-1","capture_policy_revision":43,"admission_state":"enabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"not_applicable","exported":0,"dropped":0,"unaccounted":0}}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if writer.ack.AckState != icapturepolicy.AckReady || writer.ack.TraceDisposition != icapturepolicy.DispositionNotApplicable || writer.ack.ExportedCount != nil || writer.ack.DroppedCount != nil || writer.ack.UnaccountedCount != nil {
		t.Fatalf("enabled ACK persisted with non-applicable queue counters: %+v", writer.ack)
	}
}

func TestInternalTraceGatewayAckRechecksOperationAfterConfigurationCandidate(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(traceGatewayAckRaceReader{}, nil, nil, nil, writer)
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-race:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/otelcol","process_boot_id":"boot-1","capture_policy_revision":42,"admission_state":"disabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"complete","exported":16,"dropped":2,"unaccounted":0}}`, capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 after operation became terminal", response.Code)
	}
	if writer.ack.OperationID != "" {
		t.Fatalf("terminal operation must not be persisted: %+v", writer.ack)
	}
}

func TestInternalTraceGatewayAckConsumesAuthoritativeDisabledCompleteFixture(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(traceGatewayAckReader("op-43", 43, capturepolicysvc.PhaseDisabling), nil, nil, nil, writer)
	fixture, err := os.ReadFile("testdata/gateway-ack-disabled-complete.json")
	if err != nil {
		t.Fatal(err)
	}
	profile := capturePolicyWorkloadProfile()
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-43:ack", string(fixture), profile)
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if writer.ack.OperationID != "op-43" || writer.ack.PolicyRevision != 43 || writer.ack.InstanceID != "spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-0f14" || writer.ack.TraceDisposition != icapturepolicy.DispositionComplete || writer.ack.UnaccountedCount == nil || *writer.ack.UnaccountedCount != 0 {
		t.Fatalf("authoritative complete fixture was not persisted faithfully: %+v", writer.ack)
	}
}

func TestInternalTraceGatewayAckConsumesAuthoritativeDisabledGapFixture(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(traceGatewayAckReader("op-43", 43, capturepolicysvc.PhaseDisabling), nil, nil, nil, writer)
	fixture, err := os.ReadFile("testdata/gateway-ack-disabled-gap.json")
	if err != nil {
		t.Fatal(err)
	}
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-43:ack", string(fixture), capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusNoContent || writer.ack.TraceDisposition != icapturepolicy.DispositionGap || writer.ack.GapReason != "collector_restarted" || writer.ack.UnaccountedCount != nil {
		t.Fatalf("authoritative gap fixture was not persisted faithfully: status=%d body=%s ack=%+v", response.Code, response.Body.String(), writer.ack)
	}
}

func TestTraceGatewayAcknowledgementFixturesRemainPinnedToFrozenContract(t *testing.T) {
	// The local copies are byte-for-byte copies of the authoritative bkn-docs
	// fixtures. Pinning their digests prevents a locally convenient DTO/fixture
	// approximation from silently becoming a second wire contract.
	fixtures := map[string]string{
		"testdata/gateway-ack-disabled-complete.json": "45d4e0e8274cef04c269de5c39dac4f721471257a780946c46e5ddff84e592de",
		"testdata/gateway-ack-disabled-gap.json":      "8f8db7a72fe4329794b19fae4daf557c30b880d6b283bc2c5e405ed241c0c67b",
	}
	for path, want := range fixtures {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if got := fmt.Sprintf("%x", digest[:]); got != want {
			t.Fatalf("fixture %s digest = %s, want frozen digest %s", path, got, want)
		}
	}
	// When the authoritative docs checkout is available, also verify the
	// schema blob. CI for this repo need not vendor bkn-docs to run the test.
	if schemaPath := os.Getenv("BKN_DOCS_TRACE_GATEWAY_ACK_SCHEMA"); schemaPath != "" {
		data, err := os.ReadFile(schemaPath)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if got := fmt.Sprintf("%x", digest[:]); got != "573818a53d10980cfd5d4bf53d2ae0d3417e22892c64fb821ffb8fcb823757d6" {
			t.Fatalf("authoritative schema digest = %s, contract schema changed", got)
		}
	}
}

func TestInternalTraceGatewayAckRejectsAdditionalPropertiesFromFrozenFixture(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{}, nil
	}), nil, nil, nil, writer)
	fixture, err := os.ReadFile("testdata/gateway-ack-disabled-complete.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(fixture, &payload); err != nil {
		t.Fatal(err)
	}
	payload["unexpected"] = true
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-43:ack", marshalJSON(t, payload), capturePolicyWorkloadProfile())
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for additional property", response.Code)
	}
}

func TestInternalTraceGatewayAckAcceptsFrozenGapDisposition(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(traceGatewayAckReader("op-gap", 42, capturepolicysvc.PhaseDisabling), nil, nil, nil, writer)
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

func TestInternalTraceGatewayAckRequiresExactBootBinding(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return capturepolicysvc.Snapshot{}, nil }), nil, nil, nil, writer)
	profile := capturePolicyWorkloadProfile()
	profile.Permissions = []evidencevo.Permission{{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointEvidencePublisher, Operations: []string{"heartbeat"}}}
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-42:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-other","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/otelcol","process_boot_id":"boot-1","capture_policy_revision":42,"admission_state":"enabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"not_applicable","exported":0,"dropped":0,"unaccounted":0}}`, profile)
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusBadRequest {
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
	if writer.publisherClosureAcks != 1 || writer.ack.EndpointKind != icapturepolicy.EndpointEvidencePublisher || writer.ack.AckState != icapturepolicy.AckDisabled || writer.ack.EvidenceDisposition != icapturepolicy.DispositionComplete || writer.ack.PublishedCount == nil || *writer.ack.PublishedCount != 5 {
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
	if writer.publisherClosureAcks != 0 || writer.ack.AckState != icapturepolicy.AckReady || writer.ack.EvidenceDisposition != icapturepolicy.DispositionNotApplicable {
		t.Fatalf("enabled policy did not produce a ready publisher acknowledgement: %+v", writer.ack)
	}
	writer.recordErr = icapturepolicy.ErrAcknowledgementNotExpected
	request = capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-enable:publisher-ack", `{"producer_instance_id":"spiffe://cluster.local/ns/openbkn/sa/bkn-backend#boot-42","capture_policy_revision":42,"last_accepted_sequence":0,"published":0,"dropped":0,"queue_empty":true,"acknowledged_at":"2026-09-22T08:00:10.000Z"}`, profile)
	response = httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "EVIDENCE_PUBLISHER_ACK_NOT_EXPECTED") {
		t.Fatalf("frozen-set nonmember status = %d, body = %s", response.Code, response.Body.String())
	}
	writer.recordErr = icapturepolicy.ErrExpectedSetConflict
	request = capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-enable:publisher-ack", `{"producer_instance_id":"spiffe://cluster.local/ns/openbkn/sa/bkn-backend#boot-42","capture_policy_revision":42,"last_accepted_sequence":0,"published":0,"dropped":0,"queue_empty":true,"acknowledged_at":"2026-09-22T08:00:10.000Z"}`, profile)
	response = httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "INVALID_EVIDENCE_PUBLISHER_ACKNOWLEDGEMENT") {
		t.Fatalf("other ACK conflict status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestInternalEvidencePublisherAckRejectsCompletedOperation(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{
			Revision: 41, DesiredState: capturepolicysvc.StateDisabled, EffectiveState: capturepolicysvc.StateDisabled,
			LastStableRevision: 40,
			Operation:          capturepolicysvc.Operation{ID: "op-publisher", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateDisabled},
		}, nil
	}), nil, nil, nil, writer)
	profile := capturePolicyWorkloadProfile()
	profile.ApplicationPrincipalID = "spiffe://cluster.local/ns/openbkn/sa/bkn-backend"
	profile.Permissions = []evidencevo.Permission{{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointEvidencePublisher, Operations: []string{"heartbeat"}}}
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-publisher:publisher-ack", `{"producer_instance_id":"spiffe://cluster.local/ns/openbkn/sa/bkn-backend#boot-1","capture_policy_revision":41,"last_accepted_sequence":7,"published":5,"dropped":2,"queue_empty":true,"acknowledged_at":"2026-09-22T08:00:10.000Z"}`, profile)
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusConflict || writer.publisherClosureAcks != 0 {
		t.Fatalf("status = %d, closure ACKs = %d, body = %s", response.Code, writer.publisherClosureAcks, response.Body.String())
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

func TestInternalTracePolicySnapshotUnsignedWithoutAuthentication(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	builder := capturepolicysnapshot.Builder{TTL: time.Minute, Now: func() time.Time { return now }}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled, EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 42, Operation: capturepolicysvc.Operation{ID: "op-42", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateEnabled}}, nil
	}), nil, nil, builder, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/internal/trace-evidence/policy", nil)
	response := httptest.NewRecorder()
	handler.GetInternalTraceEvidencePolicy(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var snapshot traceadmissionsvc.Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.ContractVersion != traceadmissionsvc.ContractVersion || snapshot.Revision != 42 || snapshot.TraceAdmission != traceadmissionsvc.ModeEnabled || snapshot.EvidenceAdmission != traceadmissionsvc.ModeEnabled {
		t.Fatalf("unexpected unsigned snapshot: %+v", snapshot)
	}
	var wire map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if _, legacy := wire["signature"]; legacy {
		t.Fatal("legacy admission_mode field leaked into TraceEvidencePolicySnapshotV1")
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
	handler.SetAdmissionBudgetReader(capturePolicyBudgetReader{})

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
	if payload["kind"] != "configuration_get" || payload["desired_state"] != string(capturepolicysvc.StateEnabled) || payload["effective_state"] != string(capturepolicysvc.StateDisabled) || payload["policy_revision"] != float64(9) || payload["active_operation_id"] != "op-9" {
		t.Fatalf("truthful state fields missing: %v", payload)
	}
	if _, legacy := payload["revision"]; legacy {
		t.Fatal("legacy revision field leaked into configuration_get")
	}
	if _, legacy := payload["operation"]; legacy {
		t.Fatal("legacy operation object leaked into configuration_get")
	}
}

func TestCapturePolicyHandlerFailsClosedWithoutAdmissionBudget(t *testing.T) {
	handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{
			Revision: 1, DesiredState: capturepolicysvc.StateEnabled, EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 1,
			Operation: capturepolicysvc.Operation{ID: "op-1", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateEnabled},
		}, nil
	}))
	response := httptest.NewRecorder()
	handler.GetTraceEvidenceConfiguration(response, httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/trace-evidence-configuration", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "POLICY_RECONCILER_UNAVAILABLE" {
		t.Fatalf("error code = %v, want POLICY_RECONCILER_UNAVAILABLE", payload["code"])
	}
}

func TestCapturePolicyHandlerRejectsIncompleteAdmissionBudget(t *testing.T) {
	budget, err := (capturePolicyBudgetReader{}).ReadAdmissionBudget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	budget.Measurements = budget.Measurements[:1]
	handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 1, DesiredState: capturepolicysvc.StateEnabled, EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 1, Operation: capturepolicysvc.Operation{ID: "op-1", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateEnabled}}, nil
	}))
	handler.SetAdmissionBudgetReader(&countingCapturePolicyBudgetReader{budget: budget})
	response := httptest.NewRecorder()
	handler.GetTraceEvidenceConfiguration(response, httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/trace-evidence-configuration", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", response.Code, response.Body.String())
	}
}

func TestCapturePolicyHandlerReturnsOverThresholdBudgetForRead(t *testing.T) {
	budget, err := (capturePolicyBudgetReader{}).ReadAdmissionBudget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	budget.Measurements[0].Value = 0.95
	handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 1, DesiredState: capturepolicysvc.StateEnabled, EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 1, Operation: capturepolicysvc.Operation{ID: "op-1", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateEnabled}}, nil
	}))
	handler.SetAdmissionBudgetReader(&countingCapturePolicyBudgetReader{budget: budget})
	response := httptest.NewRecorder()
	handler.GetTraceEvidenceConfiguration(response, httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/trace-evidence-configuration", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var payload capturepolicysvc.ConfigurationGetResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AdmissionBudget.Measurements[0].Value != 0.95 {
		t.Fatalf("GET did not preserve over-threshold measurement: %+v", payload.AdmissionBudget.Measurements[0])
	}
}

func TestCapturePolicyHandlerOmitsTerminalActiveOperationID(t *testing.T) {
	handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{
			Revision: 10, DesiredState: capturepolicysvc.StateDisabled, EffectiveState: capturepolicysvc.StateDisabled, LastStableRevision: 10,
			Operation: capturepolicysvc.Operation{ID: "op-10", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateDisabled},
		}, nil
	}))
	handler.SetAdmissionBudgetReader(capturePolicyBudgetReader{})
	response := httptest.NewRecorder()
	handler.GetTraceEvidenceConfiguration(response, httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/trace-evidence-configuration", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["active_operation_id"]; ok {
		t.Fatalf("terminal operation must not be advertised as active: %v", payload)
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

func TestCapturePolicyHandlerAuditsOnlyAcceptedTrustedChange(t *testing.T) {
	before := capturepolicysvc.Snapshot{Revision: 9, DesiredState: capturepolicysvc.StateEnabled, EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 9, Operation: capturepolicysvc.Operation{ID: "op-9", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateEnabled, ExpectedRevision: 8}}
	after := capturepolicysvc.Snapshot{Revision: 10, DesiredState: capturepolicysvc.StateDisabled, EffectiveState: capturepolicysvc.StateEnabled, Operation: capturepolicysvc.Operation{ID: "op-10"}}
	handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return before, nil }), capturePolicyCommanderFunc(func(context.Context, capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error) {
		return after, nil
	}))
	calls := 0
	handler.SetAuditRequestedObserver(func(_ context.Context, actorID, actorType, actorName, effectiveSubjectID string, previous, accepted capturepolicysvc.Snapshot) {
		calls++
		if actorID != "user-1" || actorType != "user" || actorName != "Trusted operator" || effectiveSubjectID != "delegated-user" || previous.Revision != 9 || accepted.Operation.ID != "op-10" {
			t.Fatalf("wrong audit identity or state: %s %s %+v %+v", actorID, actorType, previous, accepted)
		}
	})
	request := httptest.NewRequest(http.MethodPut, "/api/agent-observability/v1/trace-evidence-configuration", bytes.NewBufferString(`{"desired_state":"disabled","expected_revision":9}`))
	request = request.WithContext(context.WithValue(request.Context(), trustedQueryScopeContextKey{}, evidencevo.QueryScope{AccountID: "delegated-user", AccountType: "user", AccessProfile: &evidencevo.AccessProfile{ActorID: "user-1", ActorNameSnapshot: "Trusted operator", EffectiveSubjectID: "delegated-user"}}))
	response := httptest.NewRecorder()
	handler.HandleTraceEvidenceConfiguration(response, request)
	if response.Code != http.StatusAccepted || calls != 1 {
		t.Fatalf("status=%d audit calls=%d", response.Code, calls)
	}

	request = httptest.NewRequest(http.MethodPut, "/api/agent-observability/v1/trace-evidence-configuration", bytes.NewBufferString(`{"desired_state":"disabled","expected_revision":9}`))
	response = httptest.NewRecorder()
	handler.HandleTraceEvidenceConfiguration(response, request)
	if response.Code != http.StatusAccepted || calls != 1 {
		t.Fatalf("untrusted identity emitted audit: status=%d calls=%d", response.Code, calls)
	}
}

func TestCapturePolicyHandlerRequiresAdmissionBudgetToEnable(t *testing.T) {
	called := false
	handler := NewCapturePolicyHandler(
		capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return capturepolicysvc.Snapshot{}, nil }),
		capturePolicyCommanderFunc(func(context.Context, capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error) {
			called = true
			return capturepolicysvc.Snapshot{}, nil
		}),
	)
	request := httptest.NewRequest(http.MethodPut, "/api/agent-observability/v1/trace-evidence-configuration", bytes.NewBufferString(`{"desired_state":"enabled","expected_revision":9}`))
	response := httptest.NewRecorder()
	handler.HandleTraceEvidenceConfiguration(response, request)
	if response.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("enable without budget status=%d called=%v body=%s", response.Code, called, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "POLICY_RECONCILER_UNAVAILABLE" {
		t.Fatalf("error code=%v, want POLICY_RECONCILER_UNAVAILABLE", payload["code"])
	}
}

func TestCapturePolicyHandlerDoesNotReadAdmissionBudgetToDisable(t *testing.T) {
	calls := 0
	handler := NewCapturePolicyHandler(
		capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return capturepolicysvc.Snapshot{}, nil }),
		capturePolicyCommanderFunc(func(_ context.Context, request capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error) {
			if request.DesiredState != capturepolicysvc.StateDisabled {
				t.Fatalf("unexpected request: %+v", request)
			}
			return capturepolicysvc.Snapshot{Revision: 10, DesiredState: capturepolicysvc.StateDisabled, EffectiveState: capturepolicysvc.StateDisabling, LastStableRevision: 9, Operation: capturepolicysvc.Operation{ID: "op-10", Phase: capturepolicysvc.PhaseDisabling, RequestedState: capturepolicysvc.StateDisabled, ExpectedRevision: 9}}, nil
		}),
	)
	handler.SetAdmissionBudgetReader(&countingCapturePolicyBudgetReader{err: errors.New("budget must not be read")})
	reader := handler.budget.(*countingCapturePolicyBudgetReader)
	request := httptest.NewRequest(http.MethodPut, "/api/agent-observability/v1/trace-evidence-configuration", bytes.NewBufferString(`{"desired_state":"disabled","expected_revision":9}`))
	response := httptest.NewRecorder()
	handler.HandleTraceEvidenceConfiguration(response, request)
	calls = reader.calls
	if response.Code != http.StatusAccepted || calls != 0 {
		t.Fatalf("disable status=%d budget_calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

func TestCapturePolicyHandlerMapsAdmissionBudgetFailuresToFrozenErrors(t *testing.T) {
	validBudget, err := (capturePolicyBudgetReader{}).ReadAdmissionBudget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		budget     capturepolicysvc.AdmissionBudget
		readErr    error
		wantStatus int
		wantCode   string
	}{
		{name: "missing measurement", budget: func() capturepolicysvc.AdmissionBudget {
			copy := validBudget
			copy.Measurements = copy.Measurements[:1]
			return copy
		}(), wantStatus: http.StatusUnprocessableEntity, wantCode: "ADMISSION_BUDGET_EXCEEDED"},
		{name: "stale", budget: func() capturepolicysvc.AdmissionBudget {
			copy := validBudget
			copy.FreshUntil = time.Now().UTC().Add(-time.Minute)
			return copy
		}(), wantStatus: http.StatusUnprocessableEntity, wantCode: "ADMISSION_BUDGET_EXCEEDED"},
		{name: "threshold", budget: func() capturepolicysvc.AdmissionBudget {
			copy := validBudget
			copy.Measurements[0].Value = 0.9
			return copy
		}(), wantStatus: http.StatusUnprocessableEntity, wantCode: "ADMISSION_BUDGET_EXCEEDED"},
		{name: "provider unavailable", readErr: errors.New("opensearch unavailable"), wantStatus: http.StatusServiceUnavailable, wantCode: "POLICY_RECONCILER_UNAVAILABLE"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			handler := NewCapturePolicyHandler(
				capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) { return capturepolicysvc.Snapshot{}, nil }),
				capturePolicyCommanderFunc(func(context.Context, capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error) {
					called = true
					return capturepolicysvc.Snapshot{}, nil
				}),
			)
			handler.SetAdmissionBudgetReader(&countingCapturePolicyBudgetReader{budget: testCase.budget, err: testCase.readErr})
			request := httptest.NewRequest(http.MethodPut, "/api/agent-observability/v1/trace-evidence-configuration", bytes.NewBufferString(`{"desired_state":"enabled","expected_revision":9}`))
			response := httptest.NewRecorder()
			handler.HandleTraceEvidenceConfiguration(response, request)
			if response.Code != testCase.wantStatus || called {
				t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["code"] != testCase.wantCode {
				t.Fatalf("error code=%v, want %s", payload["code"], testCase.wantCode)
			}
		})
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

func TestInternalTraceHeartbeatWithoutAuthentication(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(nil, nil, nil, nil, writer)
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", strings.NewReader(`{"endpoint_kind":"evidence_publisher","workload_identity":"bkn-agent","instance_id":"bkn-agent#boot-1","process_boot_id":"boot-1","observed_revision":42,"ready":true}`))
	response := httptest.NewRecorder()
	handler.HeartbeatInternalTraceEvidenceEndpoint(response, request)
	if response.Code != http.StatusNoContent || writer.lease.WorkloadIdentity != "bkn-agent" || writer.registeredHeartbeats != 1 {
		t.Fatalf("status = %d, lease = %+v, body = %s", response.Code, writer.lease, response.Body.String())
	}
}

func TestInternalHeartbeatValidatesBodyStateWithoutAuthentication(t *testing.T) {
	for _, body := range []string{
		`{"endpoint_kind":"unknown","workload_identity":"publisher","instance_id":"publisher#boot-1","process_boot_id":"boot-1","observed_revision":42}`,
		`{"endpoint_kind":"evidence_publisher","instance_id":"publisher#boot-1","process_boot_id":"boot-1","observed_revision":42}`,
		`{"endpoint_kind":"evidence_publisher","workload_identity":"publisher","instance_id":"publisher#boot-1","process_boot_id":"boot-1","observed_revision":0}`,
		`{"endpoint_kind":"evidence_publisher","workload_identity":"publisher","instance_id":"publisher#boot-2","process_boot_id":"boot-1","observed_revision":42}`,
	} {
		writer := &capturePolicyInternalWriter{}
		handler := NewCapturePolicyHandlerWithInternal(nil, nil, nil, nil, writer)
		response := httptest.NewRecorder()
		handler.HeartbeatInternalTraceEvidenceEndpoint(response, httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest || writer.leaseUpserts != 0 || writer.registeredHeartbeats != 0 {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
}

func TestCapturePolicyBudgetFailuresIncludeSafeDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		err                   error
		reason, metric, field string
	}{
		{name: "source", err: capturepolicysvc.AdmissionSourceError("trace_collector_queue", errors.New("private dependency response")), reason: "source_unavailable", metric: "trace_collector_queue"},
		{name: "startup configuration", err: (capturepolicysvc.AdmissionBudgetThresholds{}).Validate(), reason: "invalid_configuration", field: "BKN_TRACE_ADMISSION_OPENSEARCH_CAPACITY_THRESHOLD"},
		{name: "missing metric", err: &capturepolicysvc.AdmissionBudgetError{Reason: "metric_missing", Metric: "trace_collector_queue", Fields: []string{"otelcol_exporter_queue_size"}}, reason: "metric_missing", metric: "trace_collector_queue", field: "otelcol_exporter_queue_size"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				handler := NewCapturePolicyHandler(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
					return capturepolicysvc.Snapshot{Revision: 1, DesiredState: capturepolicysvc.StateEnabled, EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 1, Operation: capturepolicysvc.Operation{ID: "op-1", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateEnabled}}, nil
				}), capturePolicyCommanderFunc(func(context.Context, capturepolicysvc.ChangeRequest) (capturepolicysvc.Snapshot, error) {
					t.Fatal("budget failure must not issue a command")
					return capturepolicysvc.Snapshot{}, nil
				}))
				handler.SetAdmissionBudgetReader(AdmissionBudgetReaderFunc(func(context.Context) (capturepolicysvc.AdmissionBudget, error) {
					return capturepolicysvc.AdmissionBudget{}, tc.err
				}))
				response := httptest.NewRecorder()
				request := httptest.NewRequest(method, "/api/agent-observability/v1/trace-evidence-configuration", strings.NewReader(`{"desired_state":"enabled","expected_revision":1}`))
				handler.HandleTraceEvidenceConfiguration(response, request)
				if response.Code != 503 {
					t.Fatalf("status=%d: %s", response.Code, response.Body.String())
				}
				var payload struct {
					Code    string                                `json:"code"`
					TraceID string                                `json:"trace_id"`
					Details capturepolicysvc.AdmissionBudgetError `json:"details"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Code != "POLICY_RECONCILER_UNAVAILABLE" || payload.Details.Reason != tc.reason || payload.Details.Metric != tc.metric {
					t.Fatalf("unexpected diagnostic: %s", response.Body.String())
				}
				if tc.field != "" && (len(payload.Details.Fields) == 0 || payload.Details.Fields[0] != tc.field) {
					t.Fatalf("configuration field lost: %s", response.Body.String())
				}
				if payload.TraceID == "" || payload.TraceID != response.Header().Get("x-trace-id") {
					t.Fatalf("trace ID lost: %s", response.Body.String())
				}
				if strings.Contains(response.Body.String(), "private dependency response") {
					t.Fatalf("raw cause exposed: %s", response.Body.String())
				}
			})
		}
	}
}

func TestRepeatedBudgetFailuresLogOnlyAtDebug(t *testing.T) {
	original := slog.Default()
	defer slog.SetDefault(original)
	for _, level := range []slog.Level{slog.LevelInfo, slog.LevelDebug} {
		t.Run(level.String(), func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: level})))
			for i := 0; i < 3; i++ {
				response := httptest.NewRecorder()
				response.Header().Set("x-trace-id", "budget-request")
				request := httptest.NewRequest(http.MethodGet, "/internal/trace-evidence/configuration", nil)
				writeAdmissionBudgetUnavailable(response, request, capturepolicysvc.AdmissionSourceError("trace_collector_queue", errors.New("collector unavailable")))
				if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "trace_collector_queue") {
					t.Fatalf("response diagnostics lost: %s", response.Body.String())
				}
			}
			if level == slog.LevelInfo && logs.Len() != 0 {
				t.Fatalf("poll failures flooded normal logs: %s", logs.String())
			}
			if level == slog.LevelDebug && !strings.Contains(logs.String(), "trace_id=budget-request") {
				t.Fatalf("debug trace diagnostic missing: %s", logs.String())
			}
		})
	}
}
