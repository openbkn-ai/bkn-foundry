// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

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
