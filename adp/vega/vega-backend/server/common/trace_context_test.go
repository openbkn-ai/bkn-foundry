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
