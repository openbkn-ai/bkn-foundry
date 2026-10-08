// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package tracecontrolintegration

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysnapshot"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// memoryControlWriter records persistence calls made by the real HTTP handler.
type memoryControlWriter struct {
	lease                icapturepolicy.EndpointLease
	ack                  icapturepolicy.ExpectedAcknowledgement
	registeredHeartbeats int
	publisherClosureAcks int
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

func (w *memoryControlWriter) UpsertEndpointLease(_ context.Context, lease icapturepolicy.EndpointLease) error {

	w.lease = lease
	return nil
}

func (w *memoryControlWriter) RegisterEvidencePublisherHeartbeat(_ context.Context, lease icapturepolicy.EndpointLease) error {
	w.registeredHeartbeats++
	w.lease = lease
	return nil
}

func (w *memoryControlWriter) RecordAcknowledgement(_ context.Context, ack icapturepolicy.ExpectedAcknowledgement) error {
	w.ack = ack
	return nil
}

func (w *memoryControlWriter) RecordEvidencePublisherAcknowledgement(_ context.Context, ack icapturepolicy.ExpectedAcknowledgement, _ time.Time) error {
	w.publisherClosureAcks++
	w.ack = ack
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestUnsignedUnauthenticatedGoPublisherAgainstServerHandler checks the wire
// contract between actual Go clients and actual internal handlers without
// listening on a socket or depending on a running database.
func TestUnsignedUnauthenticatedGoPublisherAgainstServerHandler(t *testing.T) {
	ctx := context.Background()
	writer := &memoryControlWriter{}
	handler := httphandler.NewCapturePolicyHandlerWithInternal(capturepolicysvc.ReaderFunc(func(context.Context) (capturepolicysvc.Snapshot, error) {
		return capturepolicysvc.Snapshot{Revision: 42, DesiredState: capturepolicysvc.StateDisabled, EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 41, Operation: capturepolicysvc.Operation{ID: "op-42", Phase: capturepolicysvc.PhaseDisabling, RequestedState: capturepolicysvc.StateDisabled}}, nil
	}), nil, nil, capturepolicysnapshot.Builder{TTL: time.Minute}, writer)
	handler.SetAdmissionBudgetReader(capturePolicyBudgetReader{})
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "" {
			t.Fatal("internal request carried authorization")
		}
		recorder := httptest.NewRecorder()
		switch {
		case strings.HasSuffix(r.URL.Path, "/policy"):
			handler.GetInternalTraceEvidencePolicy(recorder, r)
		case strings.HasSuffix(r.URL.Path, "/configuration"):
			handler.GetTraceEvidenceConfiguration(recorder, r)
		case strings.HasSuffix(r.URL.Path, "/endpoints:heartbeat"):
			handler.HeartbeatInternalTraceEvidenceEndpoint(recorder, r)
		case strings.HasSuffix(r.URL.Path, ":publisher-ack"):
			handler.AcknowledgeInternalTraceEvidenceOperation(recorder, r)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return recorder.Result(), nil
	})}
	policy, err := evidencepublisher.NewPolicyClient(evidencepublisher.PolicyClientConfig{BaseURL: "http://internal:8081", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := policy.Read(ctx)
	if err != nil || snapshot.Revision != 42 || snapshot.EvidenceAdmission != "disabled" {
		t.Fatalf("policy = %+v, %v", snapshot, err)
	}
	configuration, err := evidencepublisher.NewConfigurationClient(evidencepublisher.ConfigurationClientConfig{BaseURL: "http://internal:8081", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	operation, ok, err := configuration.OperationForRevision(ctx, snapshot.Revision)
	if err != nil || !ok || operation.ID != "op-42" {
		t.Fatalf("configuration = %+v, %t, %v", operation, ok, err)
	}
	control, err := evidencepublisher.NewControlClient(evidencepublisher.ControlClientConfig{BaseURL: "http://internal:8081", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	workload := "spiffe://cluster.local/ns/openbkn/sa/bkn-backend"
	boot := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if err := control.Heartbeat(ctx, workload, boot, 42); err != nil {
		t.Fatal(err)
	}
	if writer.registeredHeartbeats != 1 || writer.lease.EndpointKind != "evidence_publisher" || writer.lease.WorkloadIdentity != workload || writer.lease.InstanceID != workload+"#"+boot {
		t.Fatalf("lease = %+v", writer.lease)
	}
	ack := evidencepublisher.DrainResult{ProducerInstanceID: workload + "#" + boot, CapturePolicyRevision: "42", LastAcceptedSequence: 7, Published: 5, Dropped: 2, QueueEmpty: true}
	if err := control.Acknowledge(ctx, operation, ack, time.Now()); err != nil {
		t.Fatal(err)
	}
	if writer.publisherClosureAcks != 1 || writer.ack.EndpointKind != "evidence_publisher" || writer.ack.WorkloadIdentity != workload || writer.ack.AckState != "disabled" {
		t.Fatalf("ack = %+v", writer.ack)
	}
	if requests != 4 {
		t.Fatalf("requests = %d", requests)
	}
}
