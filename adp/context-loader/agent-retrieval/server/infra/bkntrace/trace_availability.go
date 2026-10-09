// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"context"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

// Trace budgets measure only synchronous observation work, never business time.
// They are shared by nested guards within one request and do not survive it.
const tracePhaseBudget = time.Second
const maxTracePartialReasons = 64

type traceAvailabilityKey struct{}
type traceAvailability struct {
	mu          sync.Mutex
	pre, post   time.Duration
	unavailable bool
	stage, code string
	reasons     []string
}

func WithTraceAvailability(ctx context.Context) context.Context {
	if _, ok := ctx.Value(traceAvailabilityKey{}).(*traceAvailability); ok {
		return ctx
	}
	return context.WithValue(ctx, traceAvailabilityKey{}, &traceAvailability{pre: tracePhaseBudget, post: tracePhaseBudget})
}

// TraceIOContext creates a phase-limited context. Its cleanup must run immediately after observation I/O, before
// entering business code. Holding this context across business would spend the
// shared budget on work unrelated to Trace.
func TraceIOContext(ctx context.Context, phase string) (context.Context, func()) {
	state, _ := ctx.Value(traceAvailabilityKey{}).(*traceAvailability)
	remaining := tracePhaseBudget
	if state != nil {
		state.mu.Lock()
		if phase == "post" {
			remaining = state.post
		} else {
			remaining = state.pre
		}
		if state.unavailable && phase != "post" {
			remaining = 0
		}
		state.mu.Unlock()
	}
	ioCtx, cancel := context.WithTimeout(ctx, remaining)
	started := time.Now()
	var once sync.Once
	return ioCtx, func() {
		once.Do(func() {
			cancel()
			if state == nil {
				return
			}
			elapsed := time.Since(started)
			state.mu.Lock()
			defer state.mu.Unlock()
			if phase == "post" {
				state.post = max(time.Duration(0), state.post-elapsed)
			} else {
				state.pre = max(time.Duration(0), state.pre-elapsed)
			}
		})
	}
}

// IsTraceInfrastructureFailure identifies observation failures. Domain refusals
// are never inferred from Retryable. Only observation transport,
// explicit service outages, and absent observation facilities may degrade.
func IsTraceInfrastructureFailure(apiErr *APIError, err error) bool {
	if apiErr != nil {
		switch apiErr.Code {
		case "permission_denied", "resource_not_disclosed", "lease_invalid", "lease_conflict", "terminal_conflict", "attempt_conflict", "operation_required", "business_ref_invalid":
			return false
		case "trace_core_unavailable", "feature_not_installed", "service_unavailable", "evidence_capture_unavailable":
			return true
		}
		return apiErr.HTTPStatus >= 500
	}
	if err == nil {
		return false
	}
	var coreErr *CoreHTTPError
	if errors.As(err, &coreErr) {
		return coreErr.StatusCode >= 500
	}
	if errors.Is(err, ErrFeatureNotInstalled) || errors.Is(err, ErrEvidenceArtifactURLNotConfigured) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return true
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

func MarkTraceUnavailable(ctx context.Context, stage, tool, code string) {
	state, _ := ctx.Value(traceAvailabilityKey{}).(*traceAvailability)
	if state == nil {
		return
	}
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	state.mu.Lock()
	defer state.mu.Unlock()
	state.unavailable, state.stage, state.code = true, stage, code
	if tool == "" || (stage != "context" && stage != "ensure_operation" && stage != "function_begin") || !common.IsValidBKNRequestID(traceContext.RequestID) {
		return
	}
	reason := "trace_call_unrecorded:" + tool + ":" + traceContext.RequestID
	if slices.Contains(state.reasons, reason) {
		return
	}
	if len(state.reasons) < maxTracePartialReasons {
		state.reasons = append(state.reasons, reason)
	}
}

func TracePartialReasons(ctx context.Context) []string {
	state, _ := ctx.Value(traceAvailabilityKey{}).(*traceAvailability)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]string(nil), state.reasons...)
}

func TraceAvailabilityFromContext(ctx context.Context) map[string]any {
	state, _ := ctx.Value(traceAvailabilityKey{}).(*traceAvailability)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.unavailable {
		return nil
	}
	value := map[string]any{"available": false, "recorded": false, "stage": state.stage, "code": state.code, "required_action": "continue_business_without_trace_retry"}
	if len(state.reasons) > 0 {
		value["partial_reasons"] = append([]string(nil), state.reasons...)
	}
	return value
}

// ClearManagedTraceContext preserves authenticated business context and technical request correlation;
// supplied or unconfirmed managed identities must not reach a child as facts.
func ClearManagedTraceContext(ctx context.Context) context.Context {
	// An unregistered child must not retain or mutate its parent attempt facts.
	ctx = context.WithValue(ctx, evidenceOutcomeContextKey{}, (*evidenceOutcome)(nil))
	ctx = withRequestDerivedBusinessRefs(ctx, nil)
	value, ok := common.GetTraceContextFromCtx(ctx)
	if !ok {
		return ctx
	}
	value.ConversationID, value.InteractionID, value.OperationID, value.ParentOperationID = "", "", "", ""
	value.CausationEventID, value.ClaimID, value.ToolName, value.Attempt = "", "", "", 0
	return common.SetTraceContextToCtx(ctx, value)
}

// IsTracePartialReason validates a carried gap that can only lower record integrity.
// It is never permission,
// operation registration, an authoritative identity, or proof of success.
func IsTracePartialReason(reason string) bool {
	if len(reason) > 256 {
		return false
	}
	parts := strings.SplitN(reason, ":", 3)
	if len(parts) != 3 || parts[0] != "trace_call_unrecorded" || len(parts[1]) == 0 || len(parts[1]) > 64 || !common.IsValidBKNRequestID(parts[2]) {
		return false
	}
	for _, c := range parts[1] {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func MergeTracePartialReasons(ctx context.Context, reasons []string) {
	state, _ := ctx.Value(traceAvailabilityKey{}).(*traceAvailability)
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, reason := range reasons {
		if !IsTracePartialReason(reason) || slices.Contains(state.reasons, reason) {
			continue
		}
		state.unavailable, state.stage, state.code = true, "embedded_call", "trace_call_unrecorded"
		if len(state.reasons) >= maxTracePartialReasons {
			break
		}
		state.reasons = append(state.reasons, reason)
	}
}
