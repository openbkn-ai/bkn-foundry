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
	"crypto/ed25519"
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
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
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

func createTraces(_ context.Context, _ processor.Settings, cfg component.Config, next consumer.Traces) (processor.Traces, error) {
	configuration, ok := cfg.(*Config)
	if !ok {
		return nil, errors.New("traceadmission config must be *Config")
	}
	return newProcessor(*configuration, next)
}

type traceAdmissionProcessor struct {
	next    consumer.Traces
	client  *http.Client
	config  Config
	gateway *traceadmissionsvc.Gateway
	now     func() time.Time

	mu              sync.Mutex
	etag            string
	currentRevision uint64
	activeOpID      string
	cached          *traceadmissionsvc.SignedSnapshot
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	dropped         atomic.Uint64
}

func newProcessor(config Config, next consumer.Traces) (*traceAdmissionProcessor, error) {
	return newProcessorWithClient(config, next, http.DefaultClient)
}

func newProcessorWithClient(config Config, next consumer.Traces, baseClient *http.Client) (*traceAdmissionProcessor, error) {
	if next == nil {
		return nil, errors.New("traceadmission next consumer is required")
	}
	config.setDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	current, err := decodePublicKey(config.CurrentPublicKey)
	if err != nil {
		return nil, err
	}
	previous, err := decodeOptionalPublicKey(config.PreviousPublicKey)
	if err != nil {
		return nil, err
	}
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
	oauthConfig := clientcredentials.Config{
		ClientID: config.ClientID, ClientSecret: config.ClientSecret, TokenURL: config.TokenURL,
		Scopes: strings.Fields(config.Scope),
	}
	oauthContext := context.WithValue(context.Background(), oauth2.HTTPClient, &clientCopy)
	client := oauthConfig.Client(oauthContext)
	return &traceAdmissionProcessor{
		next: next, client: client, config: config, now: now,
		gateway: traceadmissionsvc.NewGateway(traceadmissionsvc.GatewayConfig{
			Audience: config.Audience, CurrentKeyID: config.CurrentKeyID, CurrentKey: ed25519.PublicKey(current),
			PreviousKeyID: config.PreviousKeyID, PreviousKey: ed25519.PublicKey(previous), Now: now,
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
		return err
	}
	if err := p.gateway.Apply(snapshot); err != nil {
		return err
	}
	p.mu.Lock()
	p.currentRevision = snapshot.Revision
	p.cached = &snapshot
	p.mu.Unlock()
	operationID, err := p.pullActiveOperation(ctx, snapshot.Revision)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.activeOpID = operationID
	p.mu.Unlock()
	if p.config.HeartbeatURL != "" {
		if err := p.sendHeartbeat(ctx, snapshot.Revision); err != nil {
			return err
		}
	}
	if operationID != "" && p.config.AckURLBase != "" {
		return p.sendAcknowledgement(ctx, operationID, snapshot)
	}
	return nil
}

func (p *traceAdmissionProcessor) pullPolicy(ctx context.Context) (traceadmissionsvc.SignedSnapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.config.PolicyURL, nil)
	if err != nil {
		return traceadmissionsvc.SignedSnapshot{}, err
	}
	p.mu.Lock()
	if p.etag != "" {
		request.Header.Set("If-None-Match", p.etag)
	}
	p.mu.Unlock()
	response, err := p.client.Do(request)
	if err != nil {
		return traceadmissionsvc.SignedSnapshot{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.cached == nil {
			return traceadmissionsvc.SignedSnapshot{}, errors.New("policy returned 304 without a cached snapshot")
		}
		return *p.cached, nil
	}
	if response.StatusCode != http.StatusOK {
		return traceadmissionsvc.SignedSnapshot{}, fmt.Errorf("policy endpoint returned %s", response.Status)
	}
	var snapshot traceadmissionsvc.SignedSnapshot
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return traceadmissionsvc.SignedSnapshot{}, err
	}
	p.mu.Lock()
	p.etag = response.Header.Get("ETag")
	p.mu.Unlock()
	return snapshot, nil
}

type configurationReadModel struct {
	Kind              string  `json:"kind"`
	PolicyRevision    uint64  `json:"policy_revision"`
	ActiveOperationID *string `json:"active_operation_id"`
	// Revision/Operation are retained only to consume the currently deployed
	// control-plane response while it converges on the frozen configuration_get
	// contract. The signed policy and ACK contracts remain strict.
	Revision  uint64 `json:"revision"`
	Operation struct {
		ID    string `json:"id"`
		Phase string `json:"phase"`
	} `json:"operation"`
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
		return "", fmt.Errorf("configuration endpoint returned %s", response.Status)
	}
	var model configurationReadModel
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&model); err != nil {
		return "", err
	}
	if model.Kind != "" && model.Kind != "configuration_get" {
		return "", fmt.Errorf("configuration endpoint returned unsupported kind %q", model.Kind)
	}
	if model.PolicyRevision != 0 {
		if model.PolicyRevision != revision || model.ActiveOperationID == nil || strings.TrimSpace(*model.ActiveOperationID) == "" {
			return "", nil
		}
		return strings.TrimSpace(*model.ActiveOperationID), nil
	}
	if model.Revision != revision || !activeOperationPhase(model.Operation.Phase) || model.Operation.ID == "" {
		return "", nil
	}
	return model.Operation.ID, nil
}

func activeOperationPhase(phase string) bool {
	switch phase {
	case "pending", "enabling", "disabling", "rolling_back":
		return true
	default:
		return false
	}
}

type heartbeatRequest struct {
	InstanceID       string `json:"instance_id"`
	ProcessBootID    string `json:"process_boot_id"`
	ObservedRevision uint64 `json:"observed_revision"`
	Ready            bool   `json:"ready"`
}

func (p *traceAdmissionProcessor) sendHeartbeat(ctx context.Context, revision uint64) error {
	return p.postJSON(ctx, p.config.HeartbeatURL, heartbeatRequest{
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

func (p *traceAdmissionProcessor) sendAcknowledgement(ctx context.Context, operationID string, snapshot traceadmissionsvc.SignedSnapshot) error {
	state := string(snapshot.TraceAdmission)
	disposition := TraceGatewayQueueDispositionV1{State: "not_applicable", Exported: 0, Dropped: 0, Unaccounted: uint64Ptr(0)}
	if snapshot.TraceAdmission == traceadmissionsvc.ModeDisabled {
		disposition = TraceGatewayQueueDispositionV1{State: "gap", Exported: 0, Dropped: p.dropped.Load(), Unaccounted: nil, GapReason: "exporter_telemetry_unavailable"}
	}
	ack := TraceGatewayAcknowledgementV1{
		ContractVersion: traceadmissionsvc.ContractVersion, GatewayInstanceID: p.config.WorkloadIdentity + "#" + p.config.ProcessBootID,
		WorkloadIdentity: p.config.WorkloadIdentity, ProcessBootID: p.config.ProcessBootID, CapturePolicyRevision: snapshot.Revision,
		AdmissionState: state, Ready: p.gateway.ReadyFor(snapshot.Revision), AcknowledgedAt: p.now().UTC(), QueueDisposition: disposition,
	}
	return p.postJSON(ctx, strings.TrimRight(p.config.AckURLBase, "/")+"/"+url.PathEscape(operationID)+":ack", ack)
}

func (p *traceAdmissionProcessor) postJSON(ctx context.Context, endpoint string, payload any) error {
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
		return fmt.Errorf("internal control endpoint returned %s", response.Status)
	}
	return nil
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

func decodeOptionalPublicKey(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	return decodePublicKey(value)
}

var _ processor.Traces = (*traceAdmissionProcessor)(nil)
