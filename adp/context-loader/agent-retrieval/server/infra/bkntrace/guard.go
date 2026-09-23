// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"log"
	"time"

	"github.com/bytedance/sonic"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/telemetry"
)

const (
	finishTimeout       = 5 * time.Second
	finishRetryAttempts = 3
	finishRetryBase     = 20 * time.Millisecond
)

type BusinessContext struct {
	ConversationID    string   `json:"conversation_id"`
	InteractionID     string   `json:"interaction_id"`
	OperationKey      string   `json:"operation_key"`
	ParentOperationID string   `json:"parent_operation_id,omitempty"`
	CausationEventIDs []string `json:"causation_event_ids,omitempty"`
	// BusinessRefs are what the server derived from the validated request.
	// They are authoritative: together with the refs evidence observes, they
	// are what the receipt records and what decides the recorded scope.
	BusinessRefs []BusinessRef `json:"business_refs,omitempty"`
	// DeclaredBusinessRefs are what the caller sent. A model asked to fill
	// this field in has been observed inventing a version, an object type and
	// an instance it never read, and the old merge let the invented version
	// overwrite the observed one. They are a diagnostic input only: they never
	// reach a receipt and never widen the recorded scope. Unparsable ones are
	// still refused, so a programmatic integration hears about its defect.
	DeclaredBusinessRefs []BusinessRef `json:"-"`
}

type GuardIntent struct {
	Context           BusinessContext
	ToolName          string
	Protocol          string
	SourceModule      string
	Input             json.RawMessage
	CapabilityProfile json.RawMessage
}

type GuardState struct {
	Result       OperationResult
	ArtifactRefs []string
}

type GuardDisposition string

const (
	GuardExecute GuardDisposition = "execute"
	GuardPending GuardDisposition = "pending"
	GuardReplay  GuardDisposition = "replay"
)

type Guard struct {
	client               *LifecycleClient
	emitOperationOutcome func(context.Context, bool)
}

func NewGuard(client *LifecycleClient) *Guard {
	return &Guard{client: client, emitOperationOutcome: telemetry.EmitOperationOutcome}
}

func (g *Guard) Begin(
	ctx context.Context,
	intent GuardIntent,
) (context.Context, GuardState, GuardDisposition, *APIError, error) {
	ctx, err := EnsureTraceCorrelation(ctx)
	if err != nil {
		return ctx, GuardState{}, "", nil, err
	}
	result, apiErr, err := g.client.EnsureOperation(ctx, EnsureOperationInput{
		ConversationID:    intent.Context.ConversationID,
		InteractionID:     intent.Context.InteractionID,
		OperationKey:      intent.Context.OperationKey,
		ToolName:          intent.ToolName,
		Protocol:          intent.Protocol,
		SourceModule:      intent.SourceModule,
		Input:             intent.Input,
		ParentOperationID: intent.Context.ParentOperationID,
		CausationEventIDs: intent.Context.CausationEventIDs,
		CapabilityProfile: intent.CapabilityProfile,
	})
	if err != nil || apiErr != nil {
		return ctx, GuardState{}, "", apiErr, err
	}
	state := GuardState{Result: result, ArtifactRefs: append([]string(nil), result.PayloadArtifactRefs...)}
	switch result.Receipt.ReceiptStatus {
	case "completed", "failed":
		return ctx, state, GuardReplay, nil, nil
	case "pending":
		if !result.Execute {
			return ctx, state, GuardPending, nil, nil
		}
	}
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	traceContext.ConversationID = intent.Context.ConversationID
	traceContext.InteractionID = intent.Context.InteractionID
	traceContext.OperationID = result.Operation.OperationID
	traceContext.ToolName = intent.ToolName
	traceContext.Attempt = int(result.Operation.Attempt)
	ctx = common.SetTraceContextToCtx(ctx, traceContext)
	ctx = common.SetAuthoritativeObservedAtIfMissing(ctx, result.Operation.CreatedAt)
	trustedRefs := append([]BusinessRef(nil), intent.Context.BusinessRefs...)
	recordIgnoredDeclaredRefs(ctx, intent)
	return withRequestDerivedBusinessRefs(withEvidenceOutcome(ctx), trustedRefs), state, GuardExecute, nil, nil
}

// EnsureTraceCorrelation preserves an incoming W3C trace when present and
// derives stable request-scoped correlation when the client does not send one.
func EnsureTraceCorrelation(ctx context.Context) (context.Context, error) {
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	ctx = common.SetTraceContextToCtx(ctx, traceContext)
	if trace.SpanContextFromContext(ctx).IsValid() {
		return ctx, nil
	}
	traceContext, _ = common.GetTraceContextFromCtx(ctx)
	traceSum := sha256.Sum256([]byte("bkn.synthetic.trace:" + traceContext.RequestID))
	spanSum := sha256.Sum256([]byte("bkn.synthetic.span:" + traceContext.RequestID))
	var traceID trace.TraceID
	var spanID trace.SpanID
	copy(traceID[:], traceSum[:len(traceID)])
	copy(spanID[:], spanSum[:len(spanID)])
	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})), nil
}

func (g *Guard) Finish(
	ctx context.Context,
	state GuardState,
	payload any,
	failed bool,
	retryable bool,
) (OperationResult, *APIError, error) {
	ctx, err := EnsureTraceCorrelation(ctx)
	if err != nil {
		return OperationResult{}, nil, err
	}
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	spanContext := trace.SpanContextFromContext(ctx)
	if traceContext.RequestID == "" || !spanContext.IsValid() {
		return OperationResult{}, nil, ErrMissingFinishCorrelation
	}
	rawPayload, marshalErr := guardPayloadJSON(payload)
	if marshalErr != nil {
		return OperationResult{}, nil, marshalErr
	}
	input := FinishAttemptInput{
		OperationID:  state.Result.Operation.OperationID,
		Attempt:      state.Result.Operation.Attempt,
		ReceiptID:    state.Result.Receipt.ReceiptID,
		RequestID:    traceContext.RequestID,
		TraceID:      spanContext.TraceID().String(),
		SpanID:       spanContext.SpanID().String(),
		Retryable:    retryable,
		ArtifactRefs: append([]string(nil), state.ArtifactRefs...),
	}
	if failed {
		input.Error = rawPayload
	} else {
		input.Output = rawPayload
	}
	// The refs derived from the request are part of the governed operation
	// context, not a by-product of evidence delivery. Keep them in the receipt
	// even while observed evidence is still awaiting a durable acknowledgement.
	// What the caller declared is not here: see BusinessContext.
	input.BusinessRefs = requestDerivedBusinessRefsFromContext(ctx)
	attempted, durable, evidenceRefs, businessRefs := snapshotEvidenceOutcome(ctx)
	switch {
	case durable:
		input.EvidenceDurability = "durable"
		input.ObservedEvidenceRefs = evidenceRefs
		input.BusinessRefs = mergeBusinessRefs(businessRefs, input.BusinessRefs)
	case attempted:
		input.EvidenceDurability = "failed"
	default:
		if evidenceIngestURL() == "" {
			input.EvidenceDurability = "pending"
		} else {
			// The receipt is itself the durable record for tools that do not emit a
			// separate business-evidence event and for rejected downstream calls.
			input.EvidenceDurability = "durable"
		}
	}
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()

	lastResult := state.Result
	for attempt := 0; attempt < finishRetryAttempts; attempt++ {
		var result OperationResult
		var apiErr *APIError
		var err error
		if failed {
			result, apiErr, err = g.client.FailAttempt(finishContext, input)
		} else {
			result, apiErr, err = g.client.CompleteAttempt(finishContext, input)
		}
		if err == nil && apiErr == nil {
			g.emitFinalizedOutcome(ctx, failed)
			return result, nil, nil
		}
		if apiErr != nil && !apiErr.Retryable && apiErr.Code != "receipt_pending" {
			return OperationResult{}, apiErr, nil
		}

		receipt, receiptErr, pollErr := g.client.GetReceipt(finishContext, input.ReceiptID)
		if pollErr == nil && receiptErr == nil {
			lastResult.Receipt = receipt
			switch receipt.ReceiptStatus {
			case "completed", "failed":
				if conflict := validateRecoveredReceipt(input, receipt, failed); conflict != nil {
					return lastResult, conflict, nil
				}
				operation, operationErr, getOperationErr := g.client.GetOperation(
					finishContext, input.OperationID,
				)
				if getOperationErr == nil && operationErr == nil {
					if conflict := validateRecoveredOperation(input, operation, failed); conflict != nil {
						return lastResult, conflict, nil
					}
					lastResult.Operation = operation
					g.emitFinalizedOutcome(ctx, failed)
					return lastResult, nil, nil
				}
				if operationErr != nil && !operationErr.Retryable {
					return lastResult, operationErr, nil
				}
			}
		}
		if attempt+1 < finishRetryAttempts {
			if err := waitForFinishRetry(finishContext, attempt); err != nil {
				break
			}
		}
	}

	// Disconnects and caller cancellation are recovered in-process above. A process
	// exit still relies on the Core lease cleaner to abandon the Interaction; this
	// client intentionally never re-executes the downstream side effect on replay.
	return lastResult, &APIError{
		Code: "receipt_pending", Message: "operation receipt finalization is not yet confirmed",
		Retryable: true, RequiredAction: "poll_receipt",
	}, nil
}

func guardPayloadJSON(payload any) (json.RawMessage, error) {
	switch value := payload.(type) {
	case json.RawMessage:
		if json.Valid(value) {
			return append(json.RawMessage(nil), value...), nil
		}
	case []byte:
		if json.Valid(value) {
			return append(json.RawMessage(nil), value...), nil
		}
	}
	raw, err := sonic.Marshal(payload)
	return raw, err
}

// recordIgnoredDeclaredRefs reports what a caller declared and the server did
// not derive. Nothing is refused over it: the point is to be able to see, per
// tool, how often a caller's declaration disagrees with the request.
func recordIgnoredDeclaredRefs(ctx context.Context, intent GuardIntent) {
	if len(intent.Context.DeclaredBusinessRefs) == 0 {
		return
	}
	derived := make(map[string]struct{}, len(intent.Context.BusinessRefs))
	for _, ref := range intent.Context.BusinessRefs {
		derived[ref.RefType+"\x00"+ref.RefID] = struct{}{}
	}
	ignored := 0
	for _, ref := range intent.Context.DeclaredBusinessRefs {
		if _, authoritative := derived[ref.RefType+"\x00"+ref.RefID]; !authoritative {
			ignored++
		}
	}
	if ignored == 0 {
		return
	}
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"bkn.business_refs.declared": len(intent.Context.DeclaredBusinessRefs),
		"bkn.business_refs.ignored":  ignored,
	})
	log.Printf(
		"INFO: business refs declared by the caller were not derived from the request and are ignored: tool=%s declared=%d ignored=%d",
		intent.ToolName, len(intent.Context.DeclaredBusinessRefs), ignored,
	)
}

// mergeBusinessRefs keeps one entry per ref_type and ref_id, with the earlier
// list winning. Callers pass the observed refs first: what evidence saw
// outranks what the request implied, so an observed version is never replaced
// by a version the request only assumed.
func mergeBusinessRefs(observed, derived []BusinessRef) []BusinessRef {
	merged := make([]BusinessRef, 0, len(observed)+len(derived))
	seen := make(map[string]struct{}, len(observed)+len(derived))
	for _, refs := range [][]BusinessRef{observed, derived} {
		for _, ref := range refs {
			key := ref.RefType + "\x00" + ref.RefID
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, ref)
		}
	}
	return merged
}

func (g *Guard) emitFinalizedOutcome(ctx context.Context, failed bool) {
	if g.emitOperationOutcome != nil {
		g.emitOperationOutcome(ctx, failed)
	}
}

func validateRecoveredReceipt(
	input FinishAttemptInput,
	receipt Receipt,
	failed bool,
) *APIError {
	expectedStatus := "completed"
	if failed {
		expectedStatus = "failed"
	}
	if receipt.ReceiptID != input.ReceiptID ||
		receipt.OperationID != input.OperationID ||
		receipt.Attempt != input.Attempt ||
		receipt.ReceiptStatus != expectedStatus ||
		receipt.RequestID != input.RequestID ||
		receipt.TraceID != input.TraceID {
		return finishIdempotencyConflict()
	}
	return nil
}

func validateRecoveredOperation(
	input FinishAttemptInput,
	operation Operation,
	failed bool,
) *APIError {
	expectedStatus := "completed"
	if failed {
		expectedStatus = "failed"
	}
	if operation.OperationID != input.OperationID ||
		operation.Attempt != input.Attempt ||
		operation.AttemptStatus != expectedStatus {
		return finishIdempotencyConflict()
	}
	return nil
}

func finishIdempotencyConflict() *APIError {
	return &APIError{
		Code:           "idempotency_conflict",
		Message:        "receipt finalization conflicts with the requested operation outcome",
		RequiredAction: "use_new_idempotency_key",
	}
}

func waitForFinishRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(finishRetryBase * time.Duration(1<<attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
