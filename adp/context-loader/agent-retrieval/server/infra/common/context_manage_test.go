package common

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestResponseFormatContextHelpers(t *testing.T) {
	convey.Convey("SetResponseFormatToCtx and GetResponseFormatFromCtx", t, func() {
		type responseFormat string

		ctx := context.Background()
		ctx = SetResponseFormatToCtx(ctx, responseFormat("toon"))

		v, ok := GetResponseFormatFromCtx(ctx)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(v, convey.ShouldEqual, responseFormat("toon"))
	})
}

func TestIsPublicAPIFromCtx(t *testing.T) {
	convey.Convey("SetPublicAPIToCtx and IsPublicAPIFromCtx", t, func() {
		ctx := context.Background()
		convey.So(IsPublicAPIFromCtx(ctx), convey.ShouldBeFalse)

		ctx = SetPublicAPIToCtx(ctx, true)
		convey.So(IsPublicAPIFromCtx(ctx), convey.ShouldBeTrue)
	})
}

func TestGetHeaderFromCtx(t *testing.T) {
	convey.Convey("GetHeaderFromCtx returns account headers when auth context exists", t, func() {
		ctx := context.Background()
		authCtx := &interfaces.AccountAuthContext{
			AccountID:   "user-1",
			AccountType: interfaces.AccessorType("tenant"),
		}
		ctx = SetAccountAuthContextToCtx(ctx, authCtx)

		header := GetHeaderFromCtx(ctx)
		convey.So(header[string(interfaces.HeaderXAccountID)], convey.ShouldEqual, "user-1")
		convey.So(header[string(interfaces.HeaderXAccountType)], convey.ShouldEqual, "tenant")
	})
}

func TestTraceContextHelpers(t *testing.T) {
	convey.Convey("SetTraceContextToCtx preserves a valid request id and sanitizes baggage", t, func() {
		ctx := SetTraceContextToCtx(context.Background(), TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000000",
			Baggage: map[string]string{
				"bkn.account.type": "service",
				"bkn.runtime.env":  "test",
				"bkn.account.id":   "user-1",
				"prompt":           "raw prompt",
			},
		})

		traceCtx, ok := GetTraceContextFromCtx(ctx)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(traceCtx.RequestID, convey.ShouldEqual, "req_01JZVALIDREQUESTID000000000")
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

func TestGetHeaderFromCtxPropagatesTraceContext(t *testing.T) {
	convey.Convey("GetHeaderFromCtx returns bkn request id, legacy request id, traceparent, and allowed baggage", t, func() {
		traceID := trace.TraceID{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x20, 0x21, 0x22, 0x23, 0x24, 0x25}
		spanID := trace.SpanID{0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37}
		spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)
		ctx = SetTraceContextToCtx(ctx, TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000001",
			Baggage: map[string]string{
				"bkn.account.type": "service",
				"bkn.account.id":   "user-1",
			},
		})

		header := GetHeaderFromCtx(ctx)
		convey.So(header[HeaderBKNRequestID], convey.ShouldEqual, "req_01JZVALIDREQUESTID000000001")
		convey.So(header[HeaderLegacyRequestID], convey.ShouldEqual, "req_01JZVALIDREQUESTID000000001")
		convey.So(header[HeaderTraceparent], convey.ShouldEqual, "00-10111213141516171819202122232425-3031323334353637-01")
		convey.So(header[HeaderBaggage], convey.ShouldEqual, "bkn.account.type=service")
	})
}
