package vega_backend

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	rmock "github.com/openbkn-ai/bkn-comm-go/rest/mock"
	"github.com/smartystreets/goconvey/convey"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	"ontology-query/common"
	"ontology-query/interfaces"
)

func TestVegaBackendAccessBuildHeadersPropagatesTraceContext(t *testing.T) {
	convey.Convey("buildHeaders includes account and BKN trace context headers", t, func() {
		traceID := trace.TraceID{0x50, 0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x60, 0x61, 0x62, 0x63, 0x64, 0x65}
		spanID := trace.SpanID{0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77}
		spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)
		ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{
			ID:   "user-1",
			Type: "user",
		})
		ctx = common.SetTraceContextToCtx(ctx, common.TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000016",
			Baggage: map[string]string{
				"bkn.account.type": "user",
				"bkn.account.id":   "user-1",
			},
		})

		access := &vegaBackendAccess{}
		headers := access.buildHeaders(ctx)

		convey.So(headers[interfaces.CONTENT_TYPE_NAME], convey.ShouldEqual, interfaces.CONTENT_TYPE_JSON)
		convey.So(headers[interfaces.HTTP_HEADER_ACCOUNT_ID], convey.ShouldEqual, "user-1")
		convey.So(headers[interfaces.HTTP_HEADER_ACCOUNT_TYPE], convey.ShouldEqual, "user")
		convey.So(headers[common.HeaderBKNRequestID], convey.ShouldEqual, "req_01JZVALIDREQUESTID000000016")
		convey.So(headers[common.HeaderLegacyRequestID], convey.ShouldEqual, "req_01JZVALIDREQUESTID000000016")
		convey.So(headers[common.HeaderTraceparent], convey.ShouldEqual, "00-50515253545556575859606162636465-7071727374757677-01")
		convey.So(headers[common.HeaderBaggage], convey.ShouldEqual, "bkn.account.type=user")
	})
}

func TestVegaBackendAccessQueryResourceDataUsesLocalClientSpan(t *testing.T) {
	convey.Convey("QueryResourceData changes outbound parent span id while preserving trace id", t, func() {
		originalProvider := otel.GetTracerProvider()
		tp := sdktrace.NewTracerProvider()
		otel.SetTracerProvider(tp)
		defer func() {
			otel.SetTracerProvider(originalProvider)
			_ = tp.Shutdown(context.Background())
		}()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)

		traceID := trace.TraceID{0x50, 0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x60, 0x61, 0x62, 0x63, 0x64, 0x65}
		upstreamSpanID := trace.SpanID{0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77}
		spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     upstreamSpanID,
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)
		ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{
			ID:   "user-1",
			Type: "user",
		})
		ctx = common.SetTraceContextToCtx(ctx, common.TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000017",
		})

		var outboundTraceparent string
		mockHTTPClient.EXPECT().
			PostNoUnmarshal(gomock.Any(), "http://vega/resources/resource-1/data", gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
				outboundTraceparent = headers[common.HeaderTraceparent]
				return http.StatusOK, []byte(`{"entries":[]}`), nil
			})

		access := &vegaBackendAccess{
			httpClient: mockHTTPClient,
			baseURL:    "http://vega",
		}
		_, err := access.QueryResourceData(ctx, "resource-1", &interfaces.ResourceDataQueryParams{})

		convey.So(err, convey.ShouldBeNil)
		convey.So(outboundTraceparent, convey.ShouldNotBeEmpty)
		match := regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-01$`).FindStringSubmatch(outboundTraceparent)
		convey.So(match, convey.ShouldHaveLength, 3)
		convey.So(match[1], convey.ShouldEqual, traceID.String())
		convey.So(match[2], convey.ShouldNotEqual, upstreamSpanID.String())
		convey.So(strings.Contains(outboundTraceparent, upstreamSpanID.String()), convey.ShouldBeFalse)
	})
}
