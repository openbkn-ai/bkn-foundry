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
	"errors"
	"fmt"
	"io"
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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iauthorizationscope"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

type capturePolicyInternalWriter struct {
	lease                  icapturepolicy.EndpointLease
	ack                    icapturepolicy.ExpectedAcknowledgement
	leaseUpserts           int
	registeredHeartbeats   int
	registeredHeartbeatErr error
	publisherClosureAcks   int
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
	return nil
}

func (w *capturePolicyInternalWriter) RecordEvidencePublisherAcknowledgement(_ context.Context, ack icapturepolicy.ExpectedAcknowledgement) error {
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

func newTraceEvidenceWorkloadAuth(
	t *testing.T,
	profile evidencevo.AccessProfile,
	introspection string,
) (*EvidenceHandler, *fakeAccessScopeResolver) {
	t.Helper()
	resolver := &fakeAccessScopeResolver{profile: profile}
	auth := NewEvidenceHandlerWithSecurityConfig(nil, EvidenceHandlerSecurityConfig{
		HydraAdminURL: "http://hydra.test",
		QueryHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPost || request.URL.Path != "/admin/oauth2/introspect" {
				t.Fatalf("unexpected OAuth introspection request: %s %s", request.Method, request.URL.Path)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(introspection))}, nil
		})},
		AuthorizationScopeResolver: resolver,
	})
	return auth, resolver
}

func traceEvidenceAppProfile(principalID string, permissions ...evidencevo.Permission) evidencevo.AccessProfile {
	return evidencevo.AccessProfile{ActorID: principalID, EffectiveSubjectID: principalID, ApplicationPrincipalID: principalID, AccountActive: true, Permissions: permissions}
}

func traceEvidenceAppIntrospection(principalID string) string {
	return `{"active":true,"sub":"` + principalID + `","client_id":"` + principalID + `","ext":{"visitor_type":"app"}}`
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

func TestInternalTracePolicyAcceptsBearerOnlyWorkload(t *testing.T) {
	auth, resolver := newTraceEvidenceWorkloadAuth(t, traceEvidenceAppProfile("trace-gateway"), traceEvidenceAppIntrospection("trace-gateway"))
	signer := capturepolicysnapshot.Signer{PrivateKey: ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)), KeyID: "capture-2026", Audience: "cluster-a", TTL: time.Minute}
	policy := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled, EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 42, Operation: capturepolicysvc.Operation{ID: "op-42", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateEnabled}}, nil
	}), nil, nil, signer, nil)
	handler := auth.InternalLifecycle(auth.RequireTrustedServicePrincipal(policy.GetInternalTraceEvidencePolicy))
	request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/internal/trace-evidence/policy", nil)
	request.Header.Set("Authorization", "Bearer gateway-token")
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if resolver.calls != 1 || resolver.trustedIdentity.ApplicationPrincipalID != "trace-gateway" {
		t.Fatalf("Bearer identity not resolved: calls=%d identity=%+v", resolver.calls, resolver.trustedIdentity)
	}
	if request.Header.Get("x-account-id") != "trace-gateway" || request.Header.Get("x-account-type") != "app" {
		t.Fatalf("OAuth identity headers not derived from token: %v", request.Header)
	}
}

func TestInternalTraceHeartbeatAcceptsBearerOnlyWorkload(t *testing.T) {
	profile := traceEvidenceAppProfile("trace-gateway", evidencevo.Permission{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointTraceGateway, Operations: []string{"heartbeat"}})
	auth, resolver := newTraceEvidenceWorkloadAuth(t, profile, traceEvidenceAppIntrospection("trace-gateway"))
	writer := &capturePolicyInternalWriter{}
	policy := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateEnabled}, nil
	}), nil, nil, nil, writer)
	handler := auth.InternalLifecycle(auth.RequireTrustedServicePrincipal(policy.HeartbeatInternalTraceEvidenceEndpoint))
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat", strings.NewReader(`{"instance_id":"trace-gateway#boot-1","process_boot_id":"boot-1","observed_revision":42,"ready":true}`))
	request.Header.Set("Authorization", "Bearer gateway-token")
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", response.Code, response.Body.String())
	}
	if resolver.calls != 1 || resolver.trustedIdentity.ApplicationPrincipalID != "trace-gateway" {
		t.Fatalf("Bearer identity not resolved: calls=%d identity=%+v", resolver.calls, resolver.trustedIdentity)
	}
	if writer.lease.EndpointKind != icapturepolicy.EndpointTraceGateway || writer.lease.WorkloadIdentity != "trace-gateway" {
		t.Fatalf("heartbeat not bound to verified grant: %+v", writer.lease)
	}
}

func TestInternalTraceWorkloadRejectsMissingAndInactiveBearer(t *testing.T) {
	for _, test := range []struct {
		name, introspection string
		includeToken        bool
	}{
		{name: "missing bearer", introspection: traceEvidenceAppIntrospection("trace-gateway")},
		{name: "inactive bearer", introspection: `{"active":false}`, includeToken: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth, resolver := newTraceEvidenceWorkloadAuth(t, traceEvidenceAppProfile("trace-gateway"), test.introspection)
			nextCalled := false
			handler := auth.InternalLifecycle(auth.RequireTrustedServicePrincipal(func(w http.ResponseWriter, _ *http.Request) { nextCalled = true; w.WriteHeader(http.StatusNoContent) }))
			request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/internal/trace-evidence/policy", nil)
			request.Header.Set("x-account-id", "trace-gateway")
			request.Header.Set("x-account-type", "app")
			if test.includeToken {
				request.Header.Set("Authorization", "Bearer inactive-token")
			}
			response := httptest.NewRecorder()
			handler(response, request)
			if response.Code != http.StatusUnauthorized || nextCalled {
				t.Fatalf("absent/inactive bearer must fail: status=%d nextCalled=%v body=%s", response.Code, nextCalled, response.Body.String())
			}
			if resolver.calls != 0 {
				t.Fatalf("unverified identity reached resolver: %d calls", resolver.calls)
			}
		})
	}
}

func TestInternalTraceWorkloadRejectsForgedIdentityHeaders(t *testing.T) {
	for _, test := range []struct{ name, header, value string }{
		{name: "account id", header: "x-account-id", value: "other-app"},
		{name: "account type", header: "x-account-type", value: "user"},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth, resolver := newTraceEvidenceWorkloadAuth(t, traceEvidenceAppProfile("trace-gateway"), traceEvidenceAppIntrospection("trace-gateway"))
			nextCalled := false
			handler := auth.InternalLifecycle(auth.RequireTrustedServicePrincipal(func(w http.ResponseWriter, _ *http.Request) { nextCalled = true; w.WriteHeader(http.StatusNoContent) }))
			request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/internal/trace-evidence/policy", nil)
			request.Header.Set("Authorization", "Bearer gateway-token")
			request.Header.Set(test.header, test.value)
			response := httptest.NewRecorder()
			handler(response, request)
			if response.Code != http.StatusUnauthorized || nextCalled {
				t.Fatalf("forged identity must fail: status=%d nextCalled=%v body=%s", response.Code, nextCalled, response.Body.String())
			}
			if resolver.calls != 0 {
				t.Fatalf("mismatched identity reached resolver: %d calls", resolver.calls)
			}
		})
	}
}

func TestInternalTraceGatewayAckRejectsPublisherOnlyBearer(t *testing.T) {
	profile := traceEvidenceAppProfile("trace-gateway", evidencevo.Permission{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointEvidencePublisher, Operations: []string{"heartbeat"}})
	auth, resolver := newTraceEvidenceWorkloadAuth(t, profile, traceEvidenceAppIntrospection("trace-gateway"))
	writer := &capturePolicyInternalWriter{}
	policy := NewCapturePolicyHandlerWithInternal(traceGatewayAckReader("op-43", 43, capturepolicysvc.PhaseDisabling), nil, nil, nil, writer)
	handler := auth.InternalLifecycle(auth.RequireTrustedServicePrincipal(policy.AcknowledgeInternalTraceEvidenceOperation))
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-43:ack", strings.NewReader(`{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"trace-gateway#boot-1","workload_identity":"trace-gateway","process_boot_id":"boot-1","capture_policy_revision":43,"admission_state":"disabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"complete","exported":16,"dropped":2,"unaccounted":0}}`))
	request.Header.Set("Authorization", "Bearer gateway-token")
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusForbidden || writer.ack.OperationID != "" {
		t.Fatalf("publisher-only principal acknowledged as gateway: status=%d ack=%+v body=%s", response.Code, writer.ack, response.Body.String())
	}
	if resolver.calls != 1 || resolver.trustedIdentity.ApplicationPrincipalID != "trace-gateway" {
		t.Fatalf("Bearer identity not resolved before endpoint auth: calls=%d identity=%+v", resolver.calls, resolver.trustedIdentity)
	}
}

func TestInternalTraceWorkloadResolverFailuresWriteSingleResponse(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "denied", err: iauthorizationscope.ErrDenied},
		{name: "unavailable", err: iauthorizationscope.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth, resolver := newTraceEvidenceWorkloadAuth(t, traceEvidenceAppProfile("trace-gateway"), traceEvidenceAppIntrospection("trace-gateway"))
			resolver.err = test.err
			handler := auth.InternalLifecycle(auth.RequireTrustedServicePrincipal(func(http.ResponseWriter, *http.Request) {
				t.Fatal("resolver failure reached workload handler")
			}))
			request := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/internal/trace-evidence/policy", nil)
			request.Header.Set("Authorization", "Bearer gateway-token")
			response := httptest.NewRecorder()

			handler(response, request)

			var envelope struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("resolver failure must produce one JSON response, got %q: %v", response.Body.String(), err)
			}
			if response.Code != http.StatusUnauthorized || envelope.Code != "QUERY_ACCESS_DENIED" {
				t.Fatalf("unexpected resolver failure response: status=%d body=%s", response.Code, response.Body.String())
			}
		})
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

func TestInternalTraceGatewayAckRejectsPublisherOnlyPrincipalWithoutIdentityMismatch(t *testing.T) {
	writer := &capturePolicyInternalWriter{}
	handler := NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{}, nil
	}), nil, nil, nil, writer)
	profile := capturePolicyWorkloadProfile()
	profile.Permissions = []evidencevo.Permission{{ResourceType: "trace_evidence_endpoint", ResourceID: icapturepolicy.EndpointEvidencePublisher, Operations: []string{"heartbeat"}}}
	request := capturePolicyWorkloadRequest(http.MethodPost, "/api/agent-observability/v1/internal/trace-evidence/operations/op-43:ack", `{"contract_version":"TraceGatewayAcknowledgementV1","gateway_instance_id":"spiffe://cluster-a/ns/openbkn/sa/otelcol#boot-1","workload_identity":"spiffe://cluster-a/ns/openbkn/sa/otelcol","process_boot_id":"boot-1","capture_policy_revision":43,"admission_state":"enabled","ready":true,"acknowledged_at":"2026-09-22T08:01:10Z","queue_disposition":{"state":"not_applicable","exported":0,"dropped":0,"unaccounted":0}}`, profile)
	response := httptest.NewRecorder()
	handler.AcknowledgeInternalTraceEvidenceOperation(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for publisher-only principal without identity mismatch", response.Code)
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
	if snapshot.ContractVersion != traceadmissionsvc.ContractVersion || snapshot.Revision != 42 || snapshot.TraceAdmission != traceadmissionsvc.ModeEnabled || snapshot.EvidenceAdmission != traceadmissionsvc.ModeEnabled || !strings.HasPrefix(snapshot.Signature, "ed25519:") {
		t.Fatalf("unexpected signed snapshot: %+v", snapshot)
	}
	var wire map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if _, legacy := wire["admission_mode"]; legacy {
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
