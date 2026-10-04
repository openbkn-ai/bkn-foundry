// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"context"
	"syscall"
	"testing"
	"time"
)

func TestTraceBudgetExcludesInterveningBusinessTime(t *testing.T) {
	ctx := WithTraceAvailability(context.Background())
	state := ctx.Value(traceAvailabilityKey{}).(*traceAvailability)
	state.pre, state.post = 100*time.Millisecond, 100*time.Millisecond
	first, release := TraceIOContext(ctx, "pre")
	if first.Err() != nil {
		t.Fatal(first.Err())
	}
	release()
	// A business step may take much longer than the observation budget. That
	// must not expire a later inner function's remaining Trace I/O budget.
	time.Sleep(120 * time.Millisecond)
	inner, innerRelease := TraceIOContext(ctx, "pre")
	defer innerRelease()
	if inner.Err() != nil || ctx.Err() != nil {
		t.Fatalf("business time consumed Trace/request budget: inner=%v business=%v", inner.Err(), ctx.Err())
	}
	deadline, ok := inner.Deadline()
	if !ok || time.Until(deadline) < 50*time.Millisecond {
		t.Fatalf("inner budget prematurely expired: %v", deadline)
	}
}

func TestTraceInfrastructureClassificationKeepsDomainErrors(t *testing.T) {
	if !IsTraceInfrastructureFailure(nil, syscall.ECONNREFUSED) {
		t.Fatal("refused transport must degrade")
	}
	if !IsTraceInfrastructureFailure(&APIError{HTTPStatus: 503, Code: "trace_core_unavailable"}, nil) {
		t.Fatal("Core outage must degrade")
	}
	for _, code := range []string{"permission_denied", "resource_not_disclosed", "lease_conflict", "terminal_conflict", "attempt_conflict"} {
		if IsTraceInfrastructureFailure(&APIError{HTTPStatus: 403, Code: code, Retryable: true}, nil) {
			t.Fatalf("domain error %s swallowed", code)
		}
	}
	if IsTraceInfrastructureFailure(nil, context.Canceled) {
		t.Fatal("caller cancellation is not a Trace outage")
	}
}

func TestUnknownFinishDoesNotInventAnUnrecordedCall(t *testing.T) {
	ctx := WithTraceAvailability(context.Background())
	MarkTraceUnavailable(ctx, "finish_operation", "execute_tool", "trace_finish_unconfirmed")
	if len(TracePartialReasons(ctx)) != 0 {
		t.Fatal("unknown finish falsely asserts registration absent")
	}
	if TraceAvailabilityFromContext(ctx)["recorded"] != false {
		t.Fatal("unknown finish claimed confirmed completion")
	}
}
