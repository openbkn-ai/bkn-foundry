package common

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceContextHelpers(t *testing.T) {
	convey.Convey("SetTraceContextToCtx preserves valid request id and sanitizes baggage", t, func() {
		ctx := SetTraceContextToCtx(context.Background(), TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000010",
			Baggage: map[string]string{
				"bkn.account.type": "service",
				"bkn.runtime.env":  "test",
				"bkn.account.id":   "user-1",
				"prompt":           "raw prompt",
			},
		})

		traceCtx, ok := GetTraceContextFromCtx(ctx)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(traceCtx.RequestID, convey.ShouldEqual, "req_01JZVALIDREQUESTID000000010")
		convey.So(traceCtx.Baggage, convey.ShouldResemble, map[string]string{
			"bkn.account.type": "service",
			"bkn.runtime.env":  "test",
		})
	})

	convey.Convey("SetTraceContextToCtx generates a request id when missing or invalid", t, func() {
		ctx := SetTraceContextToCtx(context.Background(), TraceContext{RequestID: "bad id"})

		traceCtx, ok := GetTraceContextFromCtx(ctx)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(traceCtx.RequestID, convey.ShouldStartWith, "req_")
		convey.So(IsValidBKNRequestID(traceCtx.RequestID), convey.ShouldBeTrue)
	})
}

func TestTraceContextFromHeaders(t *testing.T) {
	convey.Convey("TraceContextFromHeaders accepts bkn-request-id and falls back to x-request-id", t, func() {
		headers := map[string]string{
			HeaderBKNRequestID: "req_01JZVALIDREQUESTID000000011",
			HeaderBaggage:      "bkn.account.type=user,bkn.account.id=user-1,bkn.runtime.env=test",
		}

		traceCtx := TraceContextFromHeaders(func(key string) string { return headers[key] })
		ctx := SetTraceContextToCtx(context.Background(), traceCtx)
		traceCtx, ok := GetTraceContextFromCtx(ctx)

		convey.So(ok, convey.ShouldBeTrue)
		convey.So(traceCtx.RequestID, convey.ShouldEqual, "req_01JZVALIDREQUESTID000000011")
		convey.So(traceCtx.Baggage, convey.ShouldResemble, map[string]string{
			"bkn.account.type": "user",
			"bkn.runtime.env":  "test",
		})

		headers = map[string]string{HeaderLegacyRequestID: "req_01JZVALIDREQUESTID000000012"}
		traceCtx = TraceContextFromHeaders(func(key string) string { return headers[key] })
		ctx = SetTraceContextToCtx(context.Background(), traceCtx)
		traceCtx, ok = GetTraceContextFromCtx(ctx)

		convey.So(ok, convey.ShouldBeTrue)
		convey.So(traceCtx.RequestID, convey.ShouldEqual, "req_01JZVALIDREQUESTID000000012")
	})
}

func TestBuildTraceHeaders(t *testing.T) {
	convey.Convey("BuildTraceHeaders propagates request id, traceparent, and allowed baggage", t, func() {
		traceID := trace.TraceID{0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x30, 0x31, 0x32, 0x33, 0x34, 0x35}
		spanID := trace.SpanID{0x40, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47}
		spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)
		ctx = SetTraceContextToCtx(ctx, TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000013",
			Baggage: map[string]string{
				"bkn.account.type": "service",
				"bkn.account.id":   "user-1",
			},
		})

		headers := BuildTraceHeaders(ctx)
		convey.So(headers[HeaderBKNRequestID], convey.ShouldEqual, "req_01JZVALIDREQUESTID000000013")
		convey.So(headers[HeaderLegacyRequestID], convey.ShouldEqual, "req_01JZVALIDREQUESTID000000013")
		convey.So(headers[HeaderTraceparent], convey.ShouldEqual, "00-20212223242526272829303132333435-4041424344454647-01")
		convey.So(headers[HeaderBaggage], convey.ShouldEqual, "bkn.account.type=service")
	})
}
