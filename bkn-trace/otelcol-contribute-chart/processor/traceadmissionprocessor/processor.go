// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full
// license text.

// Package traceadmissionprocessor is an OTel Collector traces-only processor.
// Logs never consult this policy and are not accepted by this factory.
package traceadmissionprocessor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceadmissionsvc"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor"
	"go.uber.org/zap"
)

var typeTraceAdmission = component.MustNewType("traceadmission")

func NewFactory() processor.Factory {
	return processor.NewFactory(
		typeTraceAdmission,
		func() component.Config {
			return &Config{PollInterval: 10 * time.Second, HTTPTimeout: 3 * time.Second}
		},
		processor.WithTraces(createTraces, component.StabilityLevelDevelopment),
	)
}

func createTraces(_ context.Context, settings processor.Settings, cfg component.Config, next consumer.Traces) (processor.Traces, error) {
	configuration, ok := cfg.(*Config)
	if !ok {
		return nil, errors.New("traceadmission config must be *Config")
	}
	return newProcessorWithLogger(*configuration, next, settings.Logger)
}

type traceAdmissionProcessor struct {
	next          consumer.Traces
	client        *http.Client
	config        Config
	logger        *zap.Logger
	refreshFailed bool
	gateway       *traceadmissionsvc.Gateway
	now           func() time.Time

	mu              sync.Mutex
	etag            string
	currentRevision uint64
	activeOpID      string
	cached          *traceadmissionsvc.Snapshot
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	dropped         atomic.Uint64
}

func newProcessor(config Config, next consumer.Traces) (*traceAdmissionProcessor, error) {
	return newProcessorWithLogger(config, next, nil)
}

func newProcessorWithClient(config Config, next consumer.Traces, baseClient *http.Client) (*traceAdmissionProcessor, error) {
	return newProcessorWithClientAndLogger(config, next, baseClient, nil)
}

func newProcessorWithLogger(config Config, next consumer.Traces, logger *zap.Logger) (*traceAdmissionProcessor, error) {
	return newProcessorWithClientAndLogger(config, next, http.DefaultClient, logger)
}

func newProcessorWithClientAndLogger(config Config, next consumer.Traces, baseClient *http.Client, logger *zap.Logger) (*traceAdmissionProcessor, error) {
	if next == nil {
		return nil, errors.New("traceadmission next consumer is required")
	}
	config.setDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	var err error
	if config.ProcessBootID == "" {
		config.ProcessBootID, err = newBootID()
		if err != nil {
			return nil, err
		}
	}
	now := time.Now
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	clientCopy := *baseClient
	if clientCopy.Timeout <= 0 {
		clientCopy.Timeout = config.HTTPTimeout
	}
	return &traceAdmissionProcessor{
		next: next, client: &clientCopy, config: config, logger: logger, now: now,
		gateway: traceadmissionsvc.NewGateway(traceadmissionsvc.GatewayConfig{
			Now: now,
		}),
	}, nil
}

func (p *traceAdmissionProcessor) Start(ctx context.Context, _ component.Host) error {
	if p == nil {
		return errors.New("traceadmission processor is nil")
	}
	_ = p.refresh(ctx) // fail closed until the first valid snapshot arrives
	background, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancel = cancel
	p.wg.Add(1)
	p.mu.Unlock()
	go p.poll(background)
	return nil
}

func (p *traceAdmissionProcessor) poll(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = p.refresh(ctx)
		}
	}
}

func (p *traceAdmissionProcessor) Shutdown(context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	p.wg.Wait()
	return nil
}

func (p *traceAdmissionProcessor) ConsumeTraces(ctx context.Context, traces ptrace.Traces) error {
	count := traces.SpanCount()
	decision := p.gateway.Admit(count)
	if decision.Accepted == count {
		return p.next.ConsumeTraces(ctx, traces)
	}
	if decision.Dropped > 0 {
		p.dropped.Add(uint64(decision.Dropped))
	}
	return nil
}

func (p *traceAdmissionProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (p *traceAdmissionProcessor) refresh(ctx context.Context) error {
	snapshot, err := p.pullPolicy(ctx)
	if err != nil {
		return p.controlFailure("policy", err)
	}
	if err := p.gateway.Apply(snapshot); err != nil {
		return p.controlFailure("policy", err)
	}
	p.mu.Lock()
	p.currentRevision = snapshot.Revision
	p.cached = &snapshot
	p.mu.Unlock()
	operationID, err := p.pullActiveOperation(ctx, snapshot.Revision)
	if err != nil {
		return p.controlFailure("configuration", err)
	}
	p.mu.Lock()
	p.activeOpID = operationID
	p.mu.Unlock()
	if p.config.HeartbeatURL != "" {
		if err := p.sendHeartbeat(ctx, snapshot.Revision); err != nil {
			return p.controlFailure("heartbeat", err)
		}
	}
	if operationID != "" && p.config.AckURLBase != "" {
		if err := p.sendAcknowledgement(ctx, operationID, snapshot); err != nil {
			return p.controlFailure("ack", err)
		}
	}
	p.controlRecovered()
	return nil
}

func (p *traceAdmissionProcessor) pullPolicy(ctx context.Context) (traceadmissionsvc.Snapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.config.PolicyURL, nil)
	if err != nil {
		return traceadmissionsvc.Snapshot{}, err
	}
	p.mu.Lock()
	if p.etag != "" {
		request.Header.Set("If-None-Match", p.etag)
	}
	p.mu.Unlock()
	response, err := p.client.Do(request)
	if err != nil {
		return traceadmissionsvc.Snapshot{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.cached == nil {
			return traceadmissionsvc.Snapshot{}, errors.New("policy returned 304 without a cached snapshot")
		}
		return *p.cached, nil
	}
	if response.StatusCode != http.StatusOK {
		return traceadmissionsvc.Snapshot{}, &controlError{status: response.StatusCode, err: fmt.Errorf("policy endpoint returned %s", response.Status)}
	}
	var snapshot traceadmissionsvc.Snapshot
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return traceadmissionsvc.Snapshot{}, err
	}
	p.mu.Lock()
	p.etag = response.Header.Get("ETag")
	p.mu.Unlock()
	return snapshot, nil
}

type configurationReadModel struct {
	Kind                    string              `json:"kind"`
	DesiredState            string              `json:"desired_state"`
	EffectiveState          string              `json:"effective_state"`
	PolicyRevision          uint64              `json:"policy_revision"`
	LastStableRevision      uint64              `json:"last_stable_revision"`
	ActiveOperationID       *string             `json:"active_operation_id"`
	HeartbeatIntervalSecond int                 `json:"heartbeat_interval_seconds"`
	LeaseTTLSeconds         int                 `json:"lease_ttl_seconds"`
	AdmissionBudget         configurationBudget `json:"admission_budget"`
}

type configurationBudget struct {
	ContractVersion string                 `json:"contract_version"`
	Profile         string                 `json:"profile"`
	SampledAt       time.Time              `json:"sampled_at"`
	FreshUntil      time.Time              `json:"fresh_until"`
	Measurements    []configurationMeasure `json:"measurements"`
}

type configurationMeasure struct {
	Metric     string    `json:"metric"`
	Source     string    `json:"source"`
	SampleTime time.Time `json:"sample_time"`
	Value      float64   `json:"value"`
	Threshold  float64   `json:"threshold"`
	Fresh      bool      `json:"fresh"`
}

func (p *traceAdmissionProcessor) pullActiveOperation(ctx context.Context, revision uint64) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.config.ConfigurationURL, nil)
	if err != nil {
		return "", err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", &controlError{status: response.StatusCode, err: fmt.Errorf("configuration endpoint returned %s", response.Status)}
	}
	var model configurationReadModel
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&model); err != nil {
		return "", err
	}
	if model.Kind != "configuration_get" || (model.DesiredState != "enabled" && model.DesiredState != "disabled") || (model.EffectiveState != "enabled" && model.EffectiveState != "disabled") || model.PolicyRevision == 0 || model.LastStableRevision == 0 || model.HeartbeatIntervalSecond != 10 || model.LeaseTTLSeconds != 30 || !validConfigurationBudget(model.AdmissionBudget) {
		return "", errors.New("configuration endpoint returned an invalid frozen configuration_get contract")
	}
	if model.PolicyRevision != revision || model.ActiveOperationID == nil || strings.TrimSpace(*model.ActiveOperationID) == "" {
		return "", nil
	}
	return strings.TrimSpace(*model.ActiveOperationID), nil
}

func validConfigurationBudget(budget configurationBudget) bool {
	if budget.ContractVersion != "AdmissionBudgetV1" || budget.Profile == "" || budget.SampledAt.IsZero() || budget.FreshUntil.IsZero() || !budget.FreshUntil.After(budget.SampledAt) || len(budget.Measurements) != 4 {
		return false
	}
	allowed := map[string]struct{}{
		"trace_opensearch_capacity":     {},
		"trace_opensearch_heap":         {},
		"trace_collector_queue":         {},
		"trace_storage_connection_pool": {},
	}
	seen := make(map[string]struct{}, len(budget.Measurements))
	for _, measurement := range budget.Measurements {
		if _, ok := allowed[measurement.Metric]; !ok {
			return false
		}
		if _, ok := seen[measurement.Metric]; ok {
			return false
		}
		seen[measurement.Metric] = struct{}{}
		if measurement.Source == "" || measurement.SampleTime.IsZero() || !measurement.Fresh || measurement.Value < 0 || measurement.Value > 1 || measurement.Threshold <= 0 || measurement.Threshold > 1 {
			return false
		}
	}
	return len(seen) == len(allowed)
}

type heartbeatRequest struct {
	EndpointKind     string `json:"endpoint_kind"`
	WorkloadIdentity string `json:"workload_identity"`
	InstanceID       string `json:"instance_id"`
	ProcessBootID    string `json:"process_boot_id"`
	ObservedRevision uint64 `json:"observed_revision"`
	Ready            bool   `json:"ready"`
}

func (p *traceAdmissionProcessor) sendHeartbeat(ctx context.Context, revision uint64) error {
	return p.postJSON(ctx, p.config.HeartbeatURL, "heartbeat", heartbeatRequest{
		EndpointKind: "trace_gateway", WorkloadIdentity: p.config.WorkloadIdentity,
		InstanceID: p.config.WorkloadIdentity + "#" + p.config.ProcessBootID, ProcessBootID: p.config.ProcessBootID,
		ObservedRevision: revision, Ready: p.gateway.ReadyFor(revision),
	})
}

type TraceGatewayAcknowledgementV1 struct {
	ContractVersion       string                         `json:"contract_version"`
	GatewayInstanceID     string                         `json:"gateway_instance_id"`
	WorkloadIdentity      string                         `json:"workload_identity"`
	ProcessBootID         string                         `json:"process_boot_id"`
	CapturePolicyRevision uint64                         `json:"capture_policy_revision"`
	AdmissionState        string                         `json:"admission_state"`
	Ready                 bool                           `json:"ready"`
	AcknowledgedAt        time.Time                      `json:"acknowledged_at"`
	QueueDisposition      TraceGatewayQueueDispositionV1 `json:"queue_disposition"`
}

type TraceGatewayQueueDispositionV1 struct {
	State       string  `json:"state"`
	Exported    uint64  `json:"exported"`
	Dropped     uint64  `json:"dropped"`
	Unaccounted *uint64 `json:"unaccounted"`
	GapReason   string  `json:"gap_reason,omitempty"`
}

func (p *traceAdmissionProcessor) sendAcknowledgement(ctx context.Context, operationID string, snapshot traceadmissionsvc.Snapshot) error {
	state := string(snapshot.TraceAdmission)
	disposition := TraceGatewayQueueDispositionV1{State: "not_applicable", Exported: 0, Dropped: 0, Unaccounted: uint64Ptr(0)}
	if snapshot.TraceAdmission == traceadmissionsvc.ModeDisabled {
		disposition = TraceGatewayQueueDispositionV1{State: "gap", Exported: 0, Dropped: p.dropped.Load(), Unaccounted: nil, GapReason: "exporter_telemetry_unavailable"}
	}
	ack := TraceGatewayAcknowledgementV1{
		ContractVersion: "TraceGatewayAcknowledgementV1", GatewayInstanceID: p.config.WorkloadIdentity + "#" + p.config.ProcessBootID,
		WorkloadIdentity: p.config.WorkloadIdentity, ProcessBootID: p.config.ProcessBootID, CapturePolicyRevision: snapshot.Revision,
		AdmissionState: state, Ready: p.gateway.ReadyFor(snapshot.Revision), AcknowledgedAt: p.now().UTC(), QueueDisposition: disposition,
	}
	return p.postJSON(ctx, strings.TrimRight(p.config.AckURLBase, "/")+"/"+url.PathEscape(operationID)+":ack", "ack", ack)
}

func (p *traceAdmissionProcessor) postJSON(ctx context.Context, endpoint, _ string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &controlError{status: response.StatusCode, err: fmt.Errorf("internal control endpoint returned %s", response.Status)}
	}
	return nil
}

type controlError struct {
	status int
	err    error
}

func (e *controlError) Error() string { return e.err.Error() }
func (e *controlError) Unwrap() error { return e.err }

func (p *traceAdmissionProcessor) controlFailure(stage string, err error) error {
	p.mu.Lock()
	p.refreshFailed = true
	p.mu.Unlock()
	if p.logger != nil {
		status := 0
		logStage := stage
		var endpointErr *controlError
		if errors.As(err, &endpointErr) {
			status = endpointErr.status
		}
		p.logger.Warn("trace admission control refresh failed", zap.String("stage", logStage), zap.Int("status", status), zap.String("error_code", controlErrorCode(err)))
	}
	return err
}

func (p *traceAdmissionProcessor) controlRecovered() {
	p.mu.Lock()
	recovered := p.refreshFailed
	p.refreshFailed = false
	p.mu.Unlock()
	if recovered && p.logger != nil {
		p.logger.Info("trace admission control refresh recovered")
	}
}

func controlErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var endpointErr *controlError
	if errors.As(err, &endpointErr) {
		return "http_status"
	}
	return "request_failed"
}

func (p *traceAdmissionProcessor) operationID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.activeOpID
}

func (p *traceAdmissionProcessor) revision() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.currentRevision
}

func newBootID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func uint64Ptr(value uint64) *uint64 { return &value }

var _ processor.Traces = (*traceAdmissionProcessor)(nil)
