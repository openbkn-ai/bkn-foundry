// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const (
	CoreSchemaVersion = "3.0.0"
	coreAPIPath       = "/api/agent-observability/v1"
	envCoreURL        = "BKN_TRACE_CORE_URL"

	lifecycleClientTimeout      = 10 * time.Second
	lifecycleMaxIdleConnections = 100
	lifecycleIdleConnectionTTL  = 90 * time.Second
)

var (
	ErrFeatureNotInstalled      = errors.New("BKN Trace Core URL is not configured")
	ErrMissingFinishCorrelation = errors.New("current request and OTel trace context are required")
)

type APIError struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	CurrentStatus  string `json:"current_status,omitempty"`
	Retryable      bool   `json:"retryable"`
	RequiredAction string `json:"required_action,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	RetryAfterMS   int    `json:"retry_after_ms"`
}

type errorEnvelope struct {
	Error APIError `json:"error"`
}

type Owner struct {
	TenantID               string `json:"tenant_id"`
	BusinessDomainID       string `json:"business_domain_id"`
	ApplicationPrincipalID string `json:"application_principal_id"`
	EffectiveSubjectType   string `json:"effective_subject_type"`
	EffectiveSubjectID     string `json:"effective_subject_id"`
	DelegationID           string `json:"delegation_id,omitempty"`
}

type Conversation struct {
	ConversationID          string     `json:"conversation_id"`
	Owner                   Owner      `json:"owner"`
	ExternalConversationKey string     `json:"external_conversation_key"`
	Status                  string     `json:"status"`
	Generation              uint64     `json:"generation"`
	OneShot                 bool       `json:"one_shot"`
	RowVersion              uint64     `json:"row_version"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	ClosedAt                *time.Time `json:"closed_at,omitempty"`
}

type ExpectedOperation struct {
	OperationID string `json:"operation_id"`
	Required    bool   `json:"required"`
}

type ExpectedReceipt struct {
	ReceiptID string `json:"receipt_id"`
	Required  bool   `json:"required"`
}

type ClosureManifest struct {
	Version              string              `json:"completion_manifest_version"`
	AnswerArtifactRef    string              `json:"answer_artifact_ref,omitempty"`
	Claims               []string            `json:"claims,omitempty"`
	ExpectedOperations   []ExpectedOperation `json:"expected_operations,omitempty"`
	ExpectedReceipts     []ExpectedReceipt   `json:"expected_receipts,omitempty"`
	AssemblerDeadline    *time.Time          `json:"assembler_deadline,omitempty"`
	CompletionReason     string              `json:"completion_reason"`
	SystemPartialReasons []string            `json:"system_partial_reasons,omitempty"`
}

type Interaction struct {
	InteractionID   string           `json:"interaction_id"`
	ConversationID  string           `json:"conversation_id"`
	Ordinal         uint64           `json:"ordinal"`
	ExecutionStatus string           `json:"execution_status"`
	EvidenceStatus  string           `json:"evidence_status"`
	ClosureManifest *ClosureManifest `json:"closure_manifest,omitempty"`
	LeaseToken      string           `json:"lease_token"`
	LeaseEpoch      uint64           `json:"lease_epoch"`
	LeaseVersion    uint64           `json:"lease_version"`
	LeaseExpiresAt  time.Time        `json:"lease_expires_at"`
	RowVersion      uint64           `json:"row_version"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	TerminalAt      *time.Time       `json:"terminal_at,omitempty"`
}

type Operation struct {
	OperationID         string    `json:"operation_id"`
	ConversationID      string    `json:"conversation_id"`
	InteractionID       string    `json:"interaction_id"`
	OperationKey        string    `json:"operation_key"`
	ToolName            string    `json:"tool_name"`
	NormalizedInputHash string    `json:"normalized_input_hash"`
	ParentOperationID   string    `json:"parent_operation_id,omitempty"`
	CausationEventIDs   []string  `json:"causation_event_ids,omitempty"`
	Attempt             uint32    `json:"attempt"`
	AttemptStatus       string    `json:"attempt_status"`
	Retryable           bool      `json:"retryable"`
	RowVersion          uint64    `json:"row_version"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type BusinessRef struct {
	RefType          string     `json:"ref_type"`
	RefID            string     `json:"ref_id"`
	BusinessDomainID string     `json:"business_domain_id"`
	Version          string     `json:"version"`
	AsOf             *time.Time `json:"as_of,omitempty"`
	DisplayHint      string     `json:"display_hint,omitempty"`
}

type Receipt struct {
	ReceiptID            string        `json:"receipt_id"`
	SchemaVersion        string        `json:"schema_version"`
	Owner                Owner         `json:"owner"`
	ConversationID       string        `json:"conversation_id"`
	InteractionID        string        `json:"interaction_id"`
	OperationID          string        `json:"operation_id"`
	Attempt              uint32        `json:"attempt"`
	OperationKey         string        `json:"operation_key"`
	ToolName             string        `json:"tool_name"`
	NormalizedInputHash  string        `json:"normalized_input_hash"`
	ReceiptStatus        string        `json:"receipt_status"`
	EvidenceDurability   string        `json:"evidence_durability"`
	Required             bool          `json:"required"`
	RequestID            string        `json:"request_id"`
	TraceID              string        `json:"trace_id"`
	CausationEventIDs    []string      `json:"causation_event_ids"`
	ObservedEvidenceRefs []string      `json:"observed_evidence_refs"`
	BusinessRefs         []BusinessRef `json:"business_refs"`
	ArtifactRefs         []string      `json:"artifact_refs"`
	PartialReasons       []string      `json:"partial_reasons"`
	RowVersion           uint64        `json:"row_version"`
	IssuedAt             time.Time     `json:"issued_at"`
	TerminalAt           *time.Time    `json:"terminal_at,omitempty"`
	PayloadHash          string        `json:"payload_hash"`
}

type OperationResult struct {
	Operation Operation `json:"operation"`
	Receipt   Receipt   `json:"receipt"`
	Created   bool      `json:"created"`
	Execute   bool      `json:"execute"`
}

type EnsureOperationInput struct {
	ConversationID      string
	InteractionID       string
	OperationKey        string
	ToolName            string
	NormalizedInputHash string
	ParentOperationID   string
	CausationEventIDs   []string
}

type FinishAttemptInput struct {
	OperationID          string
	Attempt              uint32
	ReceiptID            string
	PayloadHash          string
	RequestID            string
	TraceID              string
	EvidenceDurability   string
	Retryable            bool
	ObservedEvidenceRefs []string
}

type LifecycleClient struct {
	baseURL string
	client  *http.Client
}

func NewLifecycleClient(baseURL string, client *http.Client) *LifecycleClient {
	if client == nil {
		client = newLifecycleHTTPClient()
	}
	return &LifecycleClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func NewLifecycleClientFromEnv() *LifecycleClient {
	return NewLifecycleClient(os.Getenv(envCoreURL), nil)
}

func newLifecycleHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = lifecycleMaxIdleConnections
	transport.MaxIdleConnsPerHost = lifecycleMaxIdleConnections
	transport.IdleConnTimeout = lifecycleIdleConnectionTTL
	return &http.Client{
		Transport: transport,
		Timeout:   lifecycleClientTimeout,
	}
}

func (c *LifecycleClient) Enabled() bool {
	return c != nil && c.baseURL != ""
}

func (c *LifecycleClient) EnsureOperation(
	ctx context.Context,
	input EnsureOperationInput,
) (OperationResult, *APIError, error) {
	var interaction Interaction
	apiErr, err := c.do(ctx, http.MethodGet, "/interactions/"+url.PathEscape(input.InteractionID), nil, &interaction)
	if err != nil || apiErr != nil {
		return OperationResult{}, apiErr, err
	}
	if interaction.ConversationID != input.ConversationID {
		return OperationResult{}, &APIError{
			Code: "interaction_required", Message: "interaction does not belong to conversation",
			RequiredAction: "start_interaction",
		}, nil
	}
	if interaction.ExecutionStatus != "active" {
		return OperationResult{}, &APIError{
			Code: "interaction_terminal", Message: "interaction is not active",
			CurrentStatus: interaction.ExecutionStatus, RequiredAction: "start_interaction",
		}, nil
	}
	body := map[string]any{
		"operation_key": input.OperationKey, "tool_name": input.ToolName,
		"normalized_input_hash": input.NormalizedInputHash, "required": true,
		"lease_token": interaction.LeaseToken, "lease_epoch": interaction.LeaseEpoch,
	}
	if input.ParentOperationID != "" {
		body["parent_operation_id"] = input.ParentOperationID
	}
	if len(input.CausationEventIDs) > 0 {
		body["causation_event_ids"] = input.CausationEventIDs
	}
	var result OperationResult
	path := "/conversations/" + url.PathEscape(input.ConversationID) +
		"/interactions/" + url.PathEscape(input.InteractionID) + "/operations:ensure"
	apiErr, err = c.do(ctx, http.MethodPost, path, body, &result)
	return result, apiErr, err
}

func (c *LifecycleClient) CompleteAttempt(
	ctx context.Context,
	input FinishAttemptInput,
) (OperationResult, *APIError, error) {
	evidenceDurability := strings.TrimSpace(input.EvidenceDurability)
	if evidenceDurability == "" {
		evidenceDurability = "pending"
	}
	return c.finishAttempt(ctx, input, "complete", evidenceDurability)
}

func (c *LifecycleClient) FailAttempt(
	ctx context.Context,
	input FinishAttemptInput,
) (OperationResult, *APIError, error) {
	return c.finishAttempt(ctx, input, "fail", "failed")
}

func (c *LifecycleClient) GetOperation(
	ctx context.Context,
	operationID string,
) (Operation, *APIError, error) {
	var operation Operation
	apiErr, err := c.do(ctx, http.MethodGet, "/operations/"+url.PathEscape(operationID), nil, &operation)
	return operation, apiErr, err
}

func (c *LifecycleClient) GetReceipt(
	ctx context.Context,
	receiptID string,
) (Receipt, *APIError, error) {
	var receipt Receipt
	apiErr, err := c.do(ctx, http.MethodGet, "/receipts/"+url.PathEscape(receiptID), nil, &receipt)
	return receipt, apiErr, err
}

// StartOperationAttempt is the explicit retry path. EnsureOperation never calls it.
func (c *LifecycleClient) StartOperationAttempt(
	ctx context.Context,
	operationID string,
) (OperationResult, *APIError, error) {
	operation, apiErr, err := c.GetOperation(ctx, operationID)
	if err != nil || apiErr != nil {
		return OperationResult{}, apiErr, err
	}
	var interaction Interaction
	apiErr, err = c.do(ctx, http.MethodGet, "/interactions/"+url.PathEscape(operation.InteractionID), nil, &interaction)
	if err != nil || apiErr != nil {
		return OperationResult{}, apiErr, err
	}
	body := map[string]any{
		"lease_token": interaction.LeaseToken,
		"lease_epoch": interaction.LeaseEpoch,
	}
	var result OperationResult
	apiErr, err = c.do(
		ctx,
		http.MethodPost,
		"/operations/"+url.PathEscape(operationID)+"/attempts",
		body,
		&result,
	)
	return result, apiErr, err
}

func (c *LifecycleClient) finishAttempt(
	ctx context.Context,
	input FinishAttemptInput,
	action string,
	evidenceDurability string,
) (OperationResult, *APIError, error) {
	body := map[string]any{
		"receipt_id": input.ReceiptID, "payload_hash": input.PayloadHash,
		"evidence_durability": evidenceDurability, "retryable": input.Retryable,
		"request_id": input.RequestID, "trace_id": input.TraceID,
		"observed_evidence_refs": input.ObservedEvidenceRefs,
	}
	var result OperationResult
	path := "/operations/" + url.PathEscape(input.OperationID) + "/attempts/" +
		strconv.FormatUint(uint64(input.Attempt), 10) + ":" + action
	apiErr, err := c.do(ctx, http.MethodPost, path, body, &result)
	return result, apiErr, err
}

func (c *LifecycleClient) Call(
	ctx context.Context,
	method, path string,
	body any,
	target any,
) (*APIError, error) {
	return c.do(ctx, method, path, body, target)
}

func (c *LifecycleClient) do(
	ctx context.Context,
	method, path string,
	body any,
	target any,
) (*APIError, error) {
	if !c.Enabled() {
		return nil, ErrFeatureNotInstalled
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+coreAPIPath+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := setTrustedLifecycleHeaders(ctx, req.Header); err != nil {
		return &APIError{
			Code: "permission_denied", Message: err.Error(),
			RequiredAction: "request_authorization",
		}, nil
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope errorEnvelope
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
			return nil, fmt.Errorf("decode lifecycle error response: %w", err)
		}
		return &envelope.Error, nil
	}
	if target == nil {
		return nil, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target); err != nil {
		return nil, fmt.Errorf("decode lifecycle response: %w", err)
	}
	return nil, nil
}

func setTrustedLifecycleHeaders(ctx context.Context, headers http.Header) error {
	traceContext, ok := common.GetTraceContextFromCtx(ctx)
	if !ok || traceContext.TenantID == "" || traceContext.BusinessDomain == "" {
		return fmt.Errorf("trusted tenant and business domain context is required")
	}
	auth, ok := common.GetAccountAuthContextFromCtx(ctx)
	if !ok || auth.AccountID == "" || auth.AccountType == interfaces.AccessorTypeAnonymous {
		return fmt.Errorf("trusted account context is required")
	}
	applicationID := auth.AccountID
	if auth.TokenInfo != nil && strings.TrimSpace(auth.TokenInfo.ClientID) != "" {
		applicationID = strings.TrimSpace(auth.TokenInfo.ClientID)
	}
	subjectType := "user"
	if auth.AccountType == interfaces.AccessorTypeApp {
		subjectType = "service"
	}
	headers.Set("X-BKN-Tenant-ID", traceContext.TenantID)
	headers.Set("X-Business-Domain-ID", traceContext.BusinessDomain)
	headers.Set("X-BKN-Application-Principal-ID", applicationID)
	headers.Set("X-BKN-Effective-Subject-Type", subjectType)
	headers.Set("X-BKN-Effective-Subject-ID", auth.AccountID)
	return nil
}
