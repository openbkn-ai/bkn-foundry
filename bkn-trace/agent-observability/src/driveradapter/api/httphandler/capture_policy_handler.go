// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceadmissionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

type CapturePolicySigner interface {
	Sign(uint64, capturepolicysvc.State) (traceadmissionsvc.SignedSnapshot, error)
}

type CapturePolicyControlWriter interface {
	UpsertEndpointLease(context.Context, icapturepolicy.EndpointLease) error
	RecordAcknowledgement(context.Context, icapturepolicy.ExpectedAcknowledgement) error
}

type EvidencePublisherHeartbeatWriter interface {
	RegisterEvidencePublisherHeartbeat(context.Context, icapturepolicy.EndpointLease) error
}

type AdmissionBudgetReader interface {
	ReadAdmissionBudget(context.Context) (capturepolicysvc.AdmissionBudget, error)
}

// CapturePolicyHandler exposes the frozen Trace/Evidence control-plane
// read/write model. Bootstrap owns the route and Access Profile middleware.
type CapturePolicyHandler struct {
	service    *capturepolicysvc.Service
	commander  capturepolicysvc.Commander
	reconciler interface {
		Reconcile(context.Context) (bool, error)
	}
	signer CapturePolicySigner
	writer CapturePolicyControlWriter
	budget AdmissionBudgetReader
}

func (h *CapturePolicyHandler) SetAdmissionBudgetReader(reader AdmissionBudgetReader) {
	if h != nil {
		h.budget = reader
	}
}

func NewCapturePolicyHandler(reader capturepolicysvc.Reader, commanders ...capturepolicysvc.Commander) *CapturePolicyHandler {
	var commander capturepolicysvc.Commander
	if len(commanders) > 0 {
		commander = commanders[0]
	}
	return &CapturePolicyHandler{service: capturepolicysvc.New(reader), commander: commander}
}

func NewCapturePolicyHandlerWithReconciler(reader capturepolicysvc.Reader, commander capturepolicysvc.Commander, reconciler interface {
	Reconcile(context.Context) (bool, error)
}) *CapturePolicyHandler {
	return &CapturePolicyHandler{service: capturepolicysvc.New(reader), commander: commander, reconciler: reconciler}
}

func NewCapturePolicyHandlerWithInternal(reader capturepolicysvc.Reader, commander capturepolicysvc.Commander, reconciler interface {
	Reconcile(context.Context) (bool, error)
}, signer CapturePolicySigner, writer CapturePolicyControlWriter) *CapturePolicyHandler {
	return &CapturePolicyHandler{service: capturepolicysvc.New(reader), commander: commander, reconciler: reconciler, signer: signer, writer: writer}
}

// HandleTraceEvidenceConfiguration dispatches the stable configuration
// contract. GET is the truthful read model; PUT creates a guarded operation
// and returns 202 while effective state converges asynchronously.
//
// @Summary Change the unified Trace/Evidence capture configuration
// @Description Requires the existing trace_evidence_configuration:global write permission. The requested state is asynchronous; effective_state reports the last observed runtime state.
// @Tags trace-evidence
// @Accept json
// @Produce json
// @Param request body capturepolicysvc.ChangeRequest true "Desired state and expected policy revision"
// @Success 202 {object} rdto.TraceEvidenceConfigurationResponse
// @Failure 400 {object} rdto.ErrorResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 403 {object} rdto.ErrorResponse
// @Failure 409 {object} rdto.ErrorResponse
// @Failure 422 {object} rdto.ErrorResponse
// @Failure 503 {object} rdto.ErrorResponse
// @Security BearerAuth
// @Router /trace-evidence-configuration [put]
func (h *CapturePolicyHandler) HandleTraceEvidenceConfiguration(w http.ResponseWriter, r *http.Request) {
	if r != nil && r.Method == http.MethodGet {
		h.GetTraceEvidenceConfiguration(w, r)
		return
	}
	ensureResponseTraceID(w, r)
	if r == nil || r.Method != http.MethodPut {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET and PUT are supported"})
		return
	}
	if h == nil || h.commander == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "POLICY_RECONCILER_UNAVAILABLE", Message: "capture policy command path is not configured"})
		return
	}
	var request capturepolicysvc.ChangeRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedRevision == 0 || (request.DesiredState != capturepolicysvc.StateEnabled && request.DesiredState != capturepolicysvc.StateDisabled) {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_CONFIGURATION_REQUEST", Message: "desired_state and expected_revision are required"})
		return
	}
	if request.DesiredState == capturepolicysvc.StateEnabled {
		if h.budget == nil {
			writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "POLICY_RECONCILER_UNAVAILABLE", Message: "admission budget is not available"})
			return
		}
		budget, budgetErr := h.budget.ReadAdmissionBudget(contextWithRequest(r))
		if budgetErr != nil {
			status, code := http.StatusServiceUnavailable, "POLICY_RECONCILER_UNAVAILABLE"
			if errors.Is(budgetErr, capturepolicysvc.ErrAdmissionBudgetExceeded) {
				status, code = http.StatusUnprocessableEntity, "ADMISSION_BUDGET_EXCEEDED"
			}
			writeJSON(w, r, status, rdto.ErrorResponse{Code: code, Message: "admission budget does not permit enabling Trace/Evidence"})
			return
		}
		if budgetErr = capturepolicysvc.ValidateAdmissionBudgetForEnable(budget, time.Now().UTC()); budgetErr != nil {
			status, code := http.StatusServiceUnavailable, "POLICY_RECONCILER_UNAVAILABLE"
			if errors.Is(budgetErr, capturepolicysvc.ErrAdmissionBudgetExceeded) {
				status, code = http.StatusUnprocessableEntity, "ADMISSION_BUDGET_EXCEEDED"
			}
			writeJSON(w, r, status, rdto.ErrorResponse{Code: code, Message: "admission budget does not permit enabling Trace/Evidence"})
			return
		}
	}
	snapshot, err := h.commander.Request(contextWithRequest(r), request)
	if err != nil {
		status, code := http.StatusServiceUnavailable, "POLICY_RECONCILER_UNAVAILABLE"
		if errors.Is(err, capturepolicysvc.ErrRevisionConflict) {
			status, code = http.StatusConflict, "CONFIG_REVISION_CONFLICT"
		} else if errors.Is(err, capturepolicysvc.ErrOperationInProgress) {
			status, code = http.StatusConflict, "TRACE_EVIDENCE_OPERATION_IN_PROGRESS"
		}
		writeJSON(w, r, status, rdto.ErrorResponse{Code: code, Message: "capture policy change was not accepted"})
		return
	}
	writeJSON(w, r, http.StatusAccepted, rdto.TraceEvidenceConfigurationResponse(snapshot))
}

// GetTraceEvidenceConfiguration returns desired/effective state separately;
// clients must not infer rollout progress from a boolean enabled field.
//
// @Summary Get the unified Trace/Evidence capture configuration
// @Description Returns desired and effective state separately, the active operation when present, and the current admission-budget measurements.
// @Description Requires the existing trace_evidence_configuration:global read permission.
// @Tags trace-evidence
// @Produce json
// @Success 200 {object} capturepolicysvc.ConfigurationGetResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 403 {object} rdto.ErrorResponse
// @Failure 503 {object} rdto.ErrorResponse
// @Security BearerAuth
// @Router /trace-evidence-configuration [get]
func (h *CapturePolicyHandler) GetTraceEvidenceConfiguration(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	if h == nil || h.service == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_UNAVAILABLE", Message: "capture policy is not configured"})
		return
	}
	snapshot, err := h.service.Read(contextWithRequest(r))
	if err != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_UNAVAILABLE", Message: "capture policy is not available"})
		return
	}
	if h.budget == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "POLICY_RECONCILER_UNAVAILABLE", Message: "admission budget is not available"})
		return
	}
	budget, err := h.budget.ReadAdmissionBudget(contextWithRequest(r))
	if err != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "POLICY_RECONCILER_UNAVAILABLE", Message: "admission budget is not available"})
		return
	}
	response, err := frozenConfigurationGetResponse(snapshot, budget)
	if err != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "POLICY_RECONCILER_UNAVAILABLE", Message: "capture policy cannot satisfy the frozen configuration contract"})
		return
	}
	writeJSON(w, r, http.StatusOK, response)
}

func frozenConfigurationGetResponse(snapshot capturepolicysvc.Snapshot, budget capturepolicysvc.AdmissionBudget) (capturepolicysvc.ConfigurationGetResponse, error) {
	if err := capturepolicysvc.ValidateAdmissionBudgetStructure(budget); err != nil {
		return capturepolicysvc.ConfigurationGetResponse{}, err
	}
	response := capturepolicysvc.ConfigurationGetResponse{
		Kind: "configuration_get", DesiredState: snapshot.DesiredState, PolicyRevision: snapshot.Revision,
		LastStableRevision: snapshot.LastStableRevision, HeartbeatIntervalSecs: 10, LeaseTTLSeconds: 30,
		AdmissionBudget: budget,
	}
	if response.DesiredState != capturepolicysvc.StateEnabled && response.DesiredState != capturepolicysvc.StateDisabled {
		return capturepolicysvc.ConfigurationGetResponse{}, errors.New("invalid desired state")
	}
	response.EffectiveState = snapshot.EffectiveState
	if response.EffectiveState != capturepolicysvc.StateEnabled && response.EffectiveState != capturepolicysvc.StateDisabled {
		switch snapshot.Operation.RequestedState {
		case capturepolicysvc.StateEnabled:
			response.EffectiveState = capturepolicysvc.StateDisabled
		case capturepolicysvc.StateDisabled:
			response.EffectiveState = capturepolicysvc.StateEnabled
		default:
			return capturepolicysvc.ConfigurationGetResponse{}, errors.New("invalid effective state")
		}
	}
	if traceGatewayOperationActive(snapshot.Operation.Phase) {
		operationID := snapshot.Operation.ID
		response.ActiveOperationID = &operationID
	}
	return response, nil
}

// GetTraceEvidenceOperation returns the durable state of one configuration operation.
//
// @Summary Get a Trace/Evidence configuration operation
// @Description Requires the existing trace_evidence_configuration:global read permission.
// @Tags trace-evidence
// @Produce json
// @Param operation_id path string true "Configuration operation ID"
// @Success 200 {object} capturepolicysvc.Operation
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 403 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 503 {object} rdto.ErrorResponse
// @Security BearerAuth
// @Router /trace-evidence-operations/{operation_id} [get]
func (h *CapturePolicyHandler) GetTraceEvidenceOperation(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	operationID := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api/agent-observability/v1/trace-evidence-operations/")
	if operationID == "" || strings.Contains(operationID, "/") || h == nil || h.service == nil {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "TRACE_EVIDENCE_OPERATION_NOT_FOUND", Message: "capture policy operation was not found"})
		return
	}
	operation, err := h.service.ReadOperation(contextWithRequest(r), operationID)
	if err == nil {
		writeJSON(w, r, http.StatusOK, operation)
		return
	}
	// Memory/contract readers may expose only a current snapshot. Durable
	// MariaDB readers implement OperationReader, so historical operation IDs
	// remain addressable after a newer operation becomes active.
	snapshot, snapshotErr := h.service.Read(contextWithRequest(r))
	if snapshotErr != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_UNAVAILABLE", Message: "capture policy is not available"})
		return
	}
	if snapshot.Operation.ID != operationID {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "TRACE_EVIDENCE_OPERATION_NOT_FOUND", Message: "capture policy operation was not found"})
		return
	}
	writeJSON(w, r, http.StatusOK, snapshot.Operation)
}

// ReconcileTraceEvidenceOperation asks the existing reconciler to advance a known operation.
//
// @Summary Reconcile a Trace/Evidence configuration operation
// @Description Requests reconciliation of the addressed operation and returns the current configuration snapshot. Requires the existing trace_evidence_configuration:global reconcile permission.
// @Tags trace-evidence
// @Produce json
// @Param operation_id path string true "Configuration operation ID"
// @Success 202 {object} rdto.TraceEvidenceConfigurationResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 403 {object} rdto.ErrorResponse
// @Failure 404 {object} rdto.ErrorResponse
// @Failure 503 {object} rdto.ErrorResponse
// @Security BearerAuth
// @Router /trace-evidence-operations/{operation_id}:reconcile [post]
func (h *CapturePolicyHandler) ReconcileTraceEvidenceOperation(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only POST is supported"})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	path = strings.TrimSuffix(path, ":reconcile")
	operationID := strings.TrimPrefix(path, "/api/agent-observability/v1/trace-evidence-operations/")
	if operationID == "" || strings.Contains(operationID, "/") || h == nil || h.service == nil {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "TRACE_EVIDENCE_OPERATION_NOT_FOUND", Message: "capture policy operation was not found"})
		return
	}
	if h.reconciler == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "POLICY_RECONCILER_UNAVAILABLE", Message: "capture policy reconciler is not configured"})
		return
	}
	before, err := h.service.Read(contextWithRequest(r))
	if err != nil || before.Operation.ID != operationID {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "TRACE_EVIDENCE_OPERATION_NOT_FOUND", Message: "capture policy operation was not found"})
		return
	}
	if _, err := h.reconciler.Reconcile(contextWithRequest(r)); err != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "POLICY_RECONCILE_FAILED", Message: "capture policy reconciliation failed"})
		return
	}
	after, err := h.service.Read(contextWithRequest(r))
	if err != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_UNAVAILABLE", Message: "capture policy is not available"})
		return
	}
	writeJSON(w, r, http.StatusAccepted, rdto.TraceEvidenceConfigurationResponse(after))
}

func (h *CapturePolicyHandler) GetInternalTraceEvidencePolicy(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	if h == nil || h.signer == nil || h.service == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_SIGNER_UNAVAILABLE", Message: "capture policy signing is not configured"})
		return
	}
	if _, ok := workloadIdentityFromRequest(r); !ok {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "OBSERVABILITY_WORKLOAD_FORBIDDEN", Message: "a verified service principal is required"})
		return
	}
	snapshot, err := h.service.Read(contextWithRequest(r))
	if err != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_UNAVAILABLE", Message: "capture policy is not available"})
		return
	}
	signed, err := h.signer.Sign(snapshot.Revision, snapshot.DesiredState)
	if err != nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_SIGNING_FAILED", Message: "capture policy could not be signed"})
		return
	}
	writeJSON(w, r, http.StatusOK, signed)
}

type endpointHeartbeatRequest struct {
	InstanceID       string `json:"instance_id"`
	ProcessBootID    string `json:"process_boot_id"`
	ObservedRevision uint64 `json:"observed_revision"`
	Ready            bool   `json:"ready"`
}

func (h *CapturePolicyHandler) HeartbeatInternalTraceEvidenceEndpoint(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only POST is supported"})
		return
	}
	if h == nil || h.writer == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_WRITER_UNAVAILABLE", Message: "capture policy writer is not configured"})
		return
	}
	workloadIdentity, ok := workloadIdentityFromRequest(r)
	if !ok {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "OBSERVABILITY_WORKLOAD_FORBIDDEN", Message: "a verified service principal is required"})
		return
	}
	var request endpointHeartbeatRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.InstanceID == "" || request.ProcessBootID == "" || request.ObservedRevision == 0 {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_ENDPOINT_HEARTBEAT", Message: "endpoint identity and observed_revision are required"})
		return
	}
	endpointKind, ok := endpointKindFromScope(r)
	if !ok {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "OBSERVABILITY_ENDPOINT_FORBIDDEN", Message: "the verified service principal is not bound to one endpoint kind"})
		return
	}
	if request.InstanceID != workloadIdentity+"#"+request.ProcessBootID {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "OBSERVABILITY_WORKLOAD_FORBIDDEN", Message: "endpoint instance and process boot identity do not match"})
		return
	}
	now := time.Now().UTC()
	lease := icapturepolicy.EndpointLease{
		EndpointKind: endpointKind, InstanceID: request.InstanceID, WorkloadIdentity: workloadIdentity,
		ProcessBootID: request.ProcessBootID, ObservedRevision: request.ObservedRevision, Ready: request.Ready,
		HeartbeatAt: now, LeaseExpiresAt: now.Add(30 * time.Second), UpdatedAt: now,
	}
	var err error
	if endpointKind == icapturepolicy.EndpointEvidencePublisher && request.Ready {
		registrar, ok := h.writer.(EvidencePublisherHeartbeatWriter)
		if !ok {
			writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_WRITER_UNAVAILABLE", Message: "capture policy writer is not configured"})
			return
		}
		err = registrar.RegisterEvidencePublisherHeartbeat(contextWithRequest(r), lease)
	} else {
		err = h.writer.UpsertEndpointLease(contextWithRequest(r), lease)
	}
	if err != nil {
		writeJSON(w, r, http.StatusConflict, rdto.ErrorResponse{Code: "INVALID_ENDPOINT_HEARTBEAT", Message: "endpoint heartbeat was rejected"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TraceGatewayAcknowledgementV1 mirrors the frozen wire fixture at
// bkn-docs/docs/foundry/bkn-trace/testing/trace-evidence-switch/v1/trace-gateway-ack.schema.json.
// Keep these JSON names and required-field checks aligned with that schema.
type TraceGatewayAcknowledgementV1 struct {
	ContractVersion       string                         `json:"contract_version"`
	GatewayInstanceID     string                         `json:"gateway_instance_id"`
	WorkloadIdentity      string                         `json:"workload_identity"`
	ProcessBootID         string                         `json:"process_boot_id"`
	CapturePolicyRevision *uint64                        `json:"capture_policy_revision"`
	AdmissionState        string                         `json:"admission_state"`
	Ready                 bool                           `json:"ready"`
	AcknowledgedAt        time.Time                      `json:"acknowledged_at"`
	QueueDisposition      TraceGatewayQueueDispositionV1 `json:"queue_disposition"`
}

// TraceGatewayQueueDispositionV1 is the required queue_disposition object in
// TraceGatewayAcknowledgementV1.
type TraceGatewayQueueDispositionV1 struct {
	State       string                 `json:"state"`
	Exported    *uint64                `json:"exported"`
	Dropped     *uint64                `json:"dropped"`
	Unaccounted requiredNullableUint64 `json:"unaccounted"`
	GapReason   string                 `json:"gap_reason,omitempty"`
}

// requiredNullableUint64 distinguishes a required JSON null from an omitted
// property. The frozen schema permits null for gap dispositions, not omission.
type requiredNullableUint64 struct {
	Value   *uint64
	Present bool
}

func (n *requiredNullableUint64) UnmarshalJSON(data []byte) error {
	n.Present = true
	if string(data) == "null" {
		n.Value = nil
		return nil
	}
	var value uint64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	n.Value = &value
	return nil
}

func (h *CapturePolicyHandler) AcknowledgeInternalTraceEvidenceOperation(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), ":publisher-ack") {
		h.AcknowledgeInternalEvidencePublisherOperation(w, r)
		return
	}
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only POST is supported"})
		return
	}
	if h == nil || h.writer == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_WRITER_UNAVAILABLE", Message: "capture policy writer is not configured"})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	path = strings.TrimSuffix(path, ":ack")
	path = strings.TrimPrefix(path, "/api/agent-observability/v1/internal/trace-evidence/operations/")
	if path == "" || strings.Contains(path, "/") {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "TRACE_EVIDENCE_OPERATION_NOT_FOUND", Message: "capture policy operation was not found"})
		return
	}
	workloadIdentity, ok := workloadIdentityFromRequest(r)
	if !ok {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "OBSERVABILITY_WORKLOAD_FORBIDDEN", Message: "a verified service principal is required"})
		return
	}
	var request TraceGatewayAcknowledgementV1
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ContractVersion != "TraceGatewayAcknowledgementV1" || request.GatewayInstanceID == "" || request.WorkloadIdentity == "" || request.ProcessBootID == "" || request.CapturePolicyRevision == nil || *request.CapturePolicyRevision == 0 || request.AcknowledgedAt.IsZero() || !request.Ready || request.QueueDisposition.Exported == nil || request.QueueDisposition.Dropped == nil || !request.QueueDisposition.Unaccounted.Present {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_GATEWAY_ACKNOWLEDGEMENT", Message: "the frozen TraceGatewayAcknowledgementV1 contract is required"})
		return
	}
	if request.WorkloadIdentity != workloadIdentity || !strings.HasPrefix(request.GatewayInstanceID, request.WorkloadIdentity+"#") {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "OBSERVABILITY_WORKLOAD_FORBIDDEN", Message: "gateway identity does not match the verified service principal"})
		return
	}
	if endpointKind, bound := endpointKindFromScope(r); !bound || endpointKind != icapturepolicy.EndpointTraceGateway {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "OBSERVABILITY_ENDPOINT_FORBIDDEN", Message: "a trace gateway capability is required"})
		return
	}
	if request.GatewayInstanceID != request.WorkloadIdentity+"#"+request.ProcessBootID {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "OBSERVABILITY_WORKLOAD_FORBIDDEN", Message: "gateway instance and process boot identity do not match"})
		return
	}
	mode := traceadmissionsvc.Mode(request.AdmissionState)
	queueStatus := traceadmissionsvc.QueueDispositionStatus(request.QueueDisposition.State)
	ack := traceadmissionsvc.Acknowledgement{Mode: mode, Queue: traceadmissionsvc.QueueDisposition{Status: queueStatus, Exported: int(*request.QueueDisposition.Exported), Dropped: int(*request.QueueDisposition.Dropped), Unaccounted: intPointer(request.QueueDisposition.Unaccounted.Value)}}
	if err := traceadmissionsvc.ValidateAcknowledgement(ack); err != nil || mode != traceadmissionsvc.ModeEnabled && mode != traceadmissionsvc.ModeDisabled {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_GATEWAY_ACKNOWLEDGEMENT", Message: "gateway acknowledgement does not satisfy the frozen contract"})
		return
	}
	ackState := icapturepolicy.AckReady
	traceDisposition := icapturepolicy.DispositionNotApplicable
	if mode == traceadmissionsvc.ModeDisabled {
		ackState, traceDisposition = icapturepolicy.AckDisabled, string(queueStatus)
	}
	exported, dropped := *request.QueueDisposition.Exported, *request.QueueDisposition.Dropped
	if !validGatewayQueueDisposition(mode, request.QueueDisposition.State, exported, dropped, request.QueueDisposition.Unaccounted.Value, request.QueueDisposition.GapReason) {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_GATEWAY_ACKNOWLEDGEMENT", Message: "queue disposition does not satisfy TraceGatewayAcknowledgementV1"})
		return
	}
	if err := h.validateTraceGatewayAckOperation(contextWithRequest(r), path, *request.CapturePolicyRevision); err != nil {
		writeJSON(w, r, http.StatusConflict, rdto.ErrorResponse{Code: "INVALID_GATEWAY_ACKNOWLEDGEMENT", Message: "gateway acknowledgement is stale or the operation is no longer active"})
		return
	}
	if err := h.writer.RecordAcknowledgement(contextWithRequest(r), icapturepolicy.ExpectedAcknowledgement{OperationID: path, EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: request.GatewayInstanceID, WorkloadIdentity: workloadIdentity, ProcessBootID: request.ProcessBootID, PolicyRevision: *request.CapturePolicyRevision, Ready: request.Ready, AckState: string(ackState), AcknowledgedAt: &request.AcknowledgedAt, ExportedCount: &exported, DroppedCount: &dropped, UnaccountedCount: request.QueueDisposition.Unaccounted.Value, TraceDisposition: traceDisposition, GapReason: request.QueueDisposition.GapReason}); err != nil {
		writeJSON(w, r, http.StatusConflict, rdto.ErrorResponse{Code: "INVALID_GATEWAY_ACKNOWLEDGEMENT", Message: "gateway acknowledgement was rejected"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validateTraceGatewayAckOperation closes the GET-to-ACK race. The
// configuration read only identifies a candidate operation; the authoritative
// operation and policy revision are re-read immediately before persistence.
func (h *CapturePolicyHandler) validateTraceGatewayAckOperation(ctx context.Context, operationID string, revision uint64) error {
	if h == nil || h.service == nil || operationID == "" || revision == 0 {
		return errors.New("capture policy operation is unavailable")
	}
	snapshot, err := h.service.Read(ctx)
	if err != nil || snapshot.Revision != revision {
		return errors.New("capture policy revision changed")
	}
	operation := snapshot.Operation
	if latest, latestErr := h.service.ReadOperation(ctx, operationID); latestErr == nil {
		operation = latest
	} else if operation.ID != operationID {
		return errors.New("capture policy operation changed")
	}
	if operation.ID != operationID || !traceGatewayOperationActive(operation.Phase) {
		return errors.New("capture policy operation is no longer active")
	}
	return nil
}

func traceGatewayOperationActive(phase capturepolicysvc.Phase) bool {
	switch phase {
	case capturepolicysvc.PhasePending, capturepolicysvc.PhaseEnabling, capturepolicysvc.PhaseDisabling, capturepolicysvc.PhaseRollingBack:
		return true
	default:
		return false
	}
}

// evidencePublisherAcknowledgement is the Session 1 wire contract consumed by
// this control plane. It intentionally remains private: the producer-owned
// contract is not re-exported or renamed by agent-observability.
type evidencePublisherAcknowledgement struct {
	ProducerInstanceID   string    `json:"producer_instance_id"`
	PolicyRevision       uint64    `json:"capture_policy_revision"`
	LastAcceptedSequence uint64    `json:"last_accepted_sequence"`
	Published            uint64    `json:"published"`
	Dropped              uint64    `json:"dropped"`
	QueueEmpty           bool      `json:"queue_empty"`
	AcknowledgedAt       time.Time `json:"acknowledged_at"`
}

func (h *CapturePolicyHandler) AcknowledgeInternalEvidencePublisherOperation(w http.ResponseWriter, r *http.Request) {
	ensureResponseTraceID(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, r, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only POST is supported"})
		return
	}
	if h == nil || h.writer == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "CAPTURE_POLICY_WRITER_UNAVAILABLE", Message: "capture policy writer is not configured"})
		return
	}
	path := strings.TrimSuffix(strings.TrimSuffix(r.URL.Path, "/"), ":publisher-ack")
	path = strings.TrimPrefix(path, "/api/agent-observability/v1/internal/trace-evidence/operations/")
	if path == "" || strings.Contains(path, "/") {
		writeJSON(w, r, http.StatusNotFound, rdto.ErrorResponse{Code: "TRACE_EVIDENCE_OPERATION_NOT_FOUND", Message: "capture policy operation was not found"})
		return
	}
	workloadIdentity, ok := workloadIdentityFromRequest(r)
	if !ok {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "OBSERVABILITY_WORKLOAD_FORBIDDEN", Message: "a verified service principal is required"})
		return
	}
	if endpointKind, bound := endpointKindFromScope(r); !bound || endpointKind != icapturepolicy.EndpointEvidencePublisher {
		writeJSON(w, r, http.StatusForbidden, rdto.ErrorResponse{Code: "OBSERVABILITY_ENDPOINT_FORBIDDEN", Message: "an evidence publisher capability is required"})
		return
	}
	var request evidencePublisherAcknowledgement
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ProducerInstanceID == "" || request.PolicyRevision == 0 || request.AcknowledgedAt.IsZero() || !request.QueueEmpty || request.LastAcceptedSequence != request.Published+request.Dropped || !strings.HasPrefix(request.ProducerInstanceID, workloadIdentity+"#") {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_EVIDENCE_PUBLISHER_ACKNOWLEDGEMENT", Message: "publisher acknowledgement does not satisfy the Session 1 closure contract"})
		return
	}
	processBootID := strings.TrimPrefix(request.ProducerInstanceID, workloadIdentity+"#")
	if processBootID == "" {
		writeJSON(w, r, http.StatusBadRequest, rdto.ErrorResponse{Code: "INVALID_EVIDENCE_PUBLISHER_ACKNOWLEDGEMENT", Message: "producer_instance_id must include a process boot identity"})
		return
	}
	policy, err := h.service.Read(contextWithRequest(r))
	if err != nil || policy.Revision != request.PolicyRevision {
		writeJSON(w, r, http.StatusConflict, rdto.ErrorResponse{Code: "INVALID_EVIDENCE_PUBLISHER_ACKNOWLEDGEMENT", Message: "publisher acknowledgement was rejected"})
		return
	}
	published, dropped, last := request.Published, request.Dropped, request.LastAcceptedSequence
	ackState, evidenceDisposition := icapturepolicy.AckDisabled, icapturepolicy.DispositionComplete
	if policy.DesiredState == capturepolicysvc.StateEnabled {
		ackState, evidenceDisposition = icapturepolicy.AckReady, icapturepolicy.DispositionNotApplicable
	}
	if err := h.writer.RecordAcknowledgement(contextWithRequest(r), icapturepolicy.ExpectedAcknowledgement{OperationID: path, EndpointKind: icapturepolicy.EndpointEvidencePublisher, InstanceID: request.ProducerInstanceID, WorkloadIdentity: workloadIdentity, ProcessBootID: processBootID, PolicyRevision: request.PolicyRevision, Ready: true, AckState: ackState, AcknowledgedAt: &request.AcknowledgedAt, DroppedCount: &dropped, LastAcceptedSequence: &last, PublishedCount: &published, QueueEmpty: &request.QueueEmpty, EvidenceDisposition: evidenceDisposition}); err != nil {
		writeJSON(w, r, http.StatusConflict, rdto.ErrorResponse{Code: "INVALID_EVIDENCE_PUBLISHER_ACKNOWLEDGEMENT", Message: "publisher acknowledgement was rejected"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validGatewayQueueDisposition(mode traceadmissionsvc.Mode, status string, exported, dropped uint64, unaccounted *uint64, gapReason string) bool {
	switch mode {
	case traceadmissionsvc.ModeEnabled:
		return status == string(traceadmissionsvc.QueueNotApplicable) && exported == 0 && dropped == 0 && unaccounted != nil && *unaccounted == 0 && gapReason == ""
	case traceadmissionsvc.ModeDisabled:
		switch status {
		case string(traceadmissionsvc.QueueComplete):
			return unaccounted != nil && *unaccounted == 0 && gapReason == ""
		case string(traceadmissionsvc.QueueGap):
			if gapReason == "" {
				return false
			}
			switch gapReason {
			case "collector_restarted", "exporter_telemetry_unavailable", "unaccounted_span", "lease_expired":
				return unaccounted == nil || *unaccounted > 0
			default:
				return false
			}
		default:
			return false
		}
	default:
		return false
	}
}

func workloadIdentityFromRequest(r *http.Request) (string, bool) {
	scope, ok := trustedQueryScopeFromContext(r.Context())
	if !ok || scope.AccessProfile == nil || !scope.AccessProfile.AccountActive {
		return "", false
	}
	if scope.AccessProfile.ApplicationPrincipalID != "" && (scope.AccountType == "app" || scope.AccountType == "service") {
		return scope.AccessProfile.ApplicationPrincipalID, true
	}
	if (scope.AccountType == "app" || scope.AccountType == "service") && scope.AccountID != "" {
		return scope.AccountID, true
	}
	return "", false
}

// endpointKindFromScope binds endpoint identity to the verified Access Profile.
// The heartbeat body deliberately has no endpoint_kind field: a workload may
// only announce the endpoint kind granted by BKN Safe, and a principal with
// zero or multiple endpoint grants is rejected rather than guessing.
func endpointKindFromScope(r *http.Request) (string, bool) {
	scope, ok := trustedQueryScopeFromContext(r.Context())
	if !ok || scope.AccessProfile == nil {
		return "", false
	}
	profile := *scope.AccessProfile
	trace := profile.HasPermission("trace_evidence_endpoint", icapturepolicy.EndpointTraceGateway, "heartbeat")
	evidence := profile.HasPermission("trace_evidence_endpoint", icapturepolicy.EndpointEvidencePublisher, "heartbeat")
	if trace == evidence {
		return "", false
	}
	if trace {
		return icapturepolicy.EndpointTraceGateway, true
	}
	return icapturepolicy.EndpointEvidencePublisher, true
}

func intPointer(value *uint64) *int {
	if value == nil {
		return nil
	}
	converted := int(*value)
	return &converted
}

func contextWithRequest(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return r.Context()
}
