package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceContextHelpers(t *testing.T) {
	ctx := SetTraceContextToCtx(context.Background(), TraceContext{
		RequestID: "req_01JZVALIDREQUESTID000000020",
		Baggage: map[string]string{
			"bkn.account.type": "service",
			"bkn.runtime.env":  "test",
			"bkn.account.id":   "user-1",
			"prompt":           "raw prompt",
		},
	})

	traceCtx, ok := GetTraceContextFromCtx(ctx)
	require.True(t, ok)
	require.Equal(t, "req_01JZVALIDREQUESTID000000020", traceCtx.RequestID)
	require.Equal(t, map[string]string{
		"bkn.account.type": "service",
		"bkn.runtime.env":  "test",
	}, traceCtx.Baggage)

	ctx = SetTraceContextToCtx(context.Background(), TraceContext{RequestID: "bad id"})
	traceCtx, ok = GetTraceContextFromCtx(ctx)
	require.True(t, ok)
	require.True(t, IsValidBKNRequestID(traceCtx.RequestID))
}

func TestTraceContextFromHeaders(t *testing.T) {
	headers := map[string]string{
		HeaderBKNRequestID: "req_01JZVALIDREQUESTID000000021",
		HeaderBaggage:      "bkn.account.type=user,bkn.account.id=user-1,bkn.runtime.env=test",
	}

	traceCtx := TraceContextFromHeaders(func(key string) string { return headers[key] })
	ctx := SetTraceContextToCtx(context.Background(), traceCtx)
	traceCtx, ok := GetTraceContextFromCtx(ctx)

	require.True(t, ok)
	require.Equal(t, "req_01JZVALIDREQUESTID000000021", traceCtx.RequestID)
	require.Equal(t, map[string]string{
		"bkn.account.type": "user",
		"bkn.runtime.env":  "test",
	}, traceCtx.Baggage)

	headers = map[string]string{HeaderLegacyRequestID: "req_01JZVALIDREQUESTID000000022"}
	traceCtx = TraceContextFromHeaders(func(key string) string { return headers[key] })
	ctx = SetTraceContextToCtx(context.Background(), traceCtx)
	traceCtx, ok = GetTraceContextFromCtx(ctx)

	require.True(t, ok)
	require.Equal(t, "req_01JZVALIDREQUESTID000000022", traceCtx.RequestID)
}

func TestBuildTraceHeaders(t *testing.T) {
	traceID := trace.TraceID{0x60, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x70, 0x71, 0x72, 0x73, 0x74, 0x75}
	spanID := trace.SpanID{0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87}
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)
	ctx = SetTraceContextToCtx(ctx, TraceContext{
		RequestID: "req_01JZVALIDREQUESTID000000023",
		Baggage: map[string]string{
			"bkn.account.type": "service",
			"bkn.account.id":   "user-1",
		},
	})

	headers := BuildTraceHeaders(ctx)
	require.Equal(t, "req_01JZVALIDREQUESTID000000023", headers[HeaderBKNRequestID])
	require.Equal(t, "req_01JZVALIDREQUESTID000000023", headers[HeaderLegacyRequestID])
	require.Equal(t, "00-60616263646566676869707172737475-8081828384858687-01", headers[HeaderTraceparent])
	require.Equal(t, "bkn.account.type=service", headers[HeaderBaggage])
}

func TestBusinessCausalityHeadersAreValidatedAndPropagated(t *testing.T) {
	ctx := SetTraceContextToCtx(context.Background(), TraceContext{
		RequestID:          "req_01JZVALIDREQUESTID000000024",
		InteractionID:      "third-party-interaction-0001",
		OperationID:        "data-query-0001",
		CausationEventID:   "retrieval-completed-0001",
		ClaimID:            "agent-answer-0001",
		Attempt:            3,
		ObservedAt:         "2026-07-25T08:00:00Z",
		ObservedAtProvided: true,
	})
	headers := BuildTraceHeaders(ctx)
	require.Equal(t, "third-party-interaction-0001", headers[HeaderBKNInteractionID])
	require.Equal(t, "data-query-0001", headers[HeaderBKNOperationID])
	require.Equal(t, "retrieval-completed-0001", headers[HeaderBKNCausationEventID])
	require.Equal(t, "agent-answer-0001", headers[HeaderBKNClaimID])
	require.Equal(t, "3", headers[HeaderBKNAttempt])

	invalid := map[string]string{
		HeaderBKNRequestID:        "req_01JZVALIDREQUESTID000000025",
		HeaderBKNInteractionID:    "bad interaction",
		HeaderBKNOperationID:      "../bad-operation",
		HeaderBKNCausationEventID: "evt<script>",
		HeaderBKNClaimID:          "claim with spaces",
		HeaderBKNAttempt:          "0",
	}
	traceCtx := TraceContextFromHeaders(func(key string) string { return invalid[key] })
	require.Empty(t, traceCtx.InteractionID)
	require.Empty(t, traceCtx.OperationID)
	require.Empty(t, traceCtx.CausationEventID)
	require.Empty(t, traceCtx.ClaimID)
	require.Equal(t, 1, traceCtx.Attempt)
}

func TestBuildTraceHeadersForChildOperation(t *testing.T) {
	ctx := SetTraceContextToCtx(context.Background(), TraceContext{
		RequestID: "req_01JZVALIDREQUESTID000000027", InteractionID: "interaction-1",
		OperationID: "parent-operation", CausationEventID: "parent-event", Attempt: 2,
	})
	first := BuildTraceHeadersForChildOperation(ctx, "model.chat", 1)
	second := BuildTraceHeadersForChildOperation(ctx, "model.chat", 1)
	third := BuildTraceHeadersForChildOperation(ctx, "model.chat", 2)
	require.NotEqual(t, "parent-operation", first[HeaderBKNOperationID])
	require.Equal(t, first[HeaderBKNOperationID], second[HeaderBKNOperationID])
	require.NotEqual(t, first[HeaderBKNOperationID], third[HeaderBKNOperationID])
	require.Equal(t, "parent-event", first[HeaderBKNCausationEventID])
	require.Equal(t, "2", first[HeaderBKNAttempt])
}

func TestMergeTraceHeadersForChildOperationKeepsCallerHeaders(t *testing.T) {
	ctx := SetTraceContextToCtx(context.Background(), TraceContext{
		RequestID: "req_01JZVALIDREQUESTID000000028", InteractionID: "interaction-1",
		OperationID: "parent-operation", CausationEventID: "parent-event", Attempt: 2,
	})
	headers := MergeTraceHeadersForChildOperation(ctx, map[string]string{"Content-Type": "application/json"}, "permission.resource.check", 2)
	require.Equal(t, "application/json", headers["Content-Type"])
	require.NotEqual(t, "parent-operation", headers[HeaderBKNOperationID])
	require.Equal(t, "parent-event", headers[HeaderBKNCausationEventID])
}

func TestStripBusinessTraceHeadersAtUntrustedBoundary(t *testing.T) {
	headers := map[string]string{
		HeaderTraceparent:         "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		HeaderBKNRequestID:        "req_01JZVALIDREQUESTID000000026",
		HeaderBKNInteractionID:    "int_business_trace_0001",
		HeaderBKNOperationID:      "op_data_query_0001",
		HeaderBKNCausationEventID: "evt_retrieval_completed_0001",
		HeaderBKNClaimID:          "claim_agent_answer_0001",
		HeaderBKNAttempt:          "3",
	}
	StripBusinessTraceHeaders(headers)
	require.NotEmpty(t, headers[HeaderTraceparent])
	require.NotEmpty(t, headers[HeaderBKNRequestID])
	require.Empty(t, headers[HeaderBKNInteractionID])
	require.Empty(t, headers[HeaderBKNOperationID])
	require.Empty(t, headers[HeaderBKNCausationEventID])
	require.Empty(t, headers[HeaderBKNClaimID])
	require.Empty(t, headers[HeaderBKNAttempt])
}
