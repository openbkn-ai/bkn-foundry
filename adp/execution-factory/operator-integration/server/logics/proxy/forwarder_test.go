package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	myErr "github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	. "github.com/smartystreets/goconvey/convey"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

func TestForwardStream_MissingResponseWriterDoesNotPanic(t *testing.T) {
	Convey("ForwardStream handles missing response writer without panic", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		forwarder := &forwarder{
			logger: logger.DefaultLogger(),
		}

		req := &interfaces.HTTPRequest{
			HTTPRouter: interfaces.HTTPRouter{
				Method: "GET",
				URL:    "http://example.com",
			},
		}

		So(func() {
			resp, err := forwarder.ForwardStream(context.Background(), req)

			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)

			httpErr, ok := err.(*myErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, 500)
			So(httpErr.ErrorDetails, ShouldEqual, "response writer not found in context")
		}, ShouldNotPanic)
	})
}

func TestForward_PropagatesTraceHeadersFromContext(t *testing.T) {
	Convey("Forward merges trace context headers into downstream HTTP request", t, func() {
		captured := map[string]string{}
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured[common.HeaderTraceparent] = r.Header.Get(common.HeaderTraceparent)
			captured[common.HeaderBKNRequestID] = r.Header.Get(common.HeaderBKNRequestID)
			captured[common.HeaderLegacyRequestID] = r.Header.Get(common.HeaderLegacyRequestID)
			captured[common.HeaderBaggage] = r.Header.Get(common.HeaderBaggage)
			captured["x-account-id"] = r.Header.Get("x-account-id")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer target.Close()

		traceID := trace.TraceID{0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x30, 0x31, 0x32, 0x33, 0x34, 0x35}
		spanID := trace.SpanID{0x40, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47}
		spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)
		ctx = common.SetTraceContextToCtx(ctx, common.TraceContext{
			RequestID: "req_01JZVALIDREQUESTID000000215",
			Baggage: map[string]string{
				"bkn.account.type": "service",
				"prompt":           "raw prompt",
			},
		})

		forwarder := &forwarder{
			pool: &clientPool{
				logger:      logger.DefaultLogger(),
				clients:     make(map[clientKey]*ProxyClient),
				config:      PoolConfig{MaxClients: 4, MaxTimeout: 5 * time.Second, DefaultTimeout: 5 * time.Second},
				stopCleanup: make(chan struct{}),
			},
			logger: logger.DefaultLogger(),
		}
		resp, err := forwarder.Forward(ctx, &interfaces.HTTPRequest{
			Timeout: time.Second,
			HTTPRouter: interfaces.HTTPRouter{
				Method: http.MethodPost,
				URL:    target.URL,
			},
			HTTPRequestParams: interfaces.HTTPRequestParams{
				Headers: map[string]any{"x-account-id": "u-9"},
				Body:    map[string]any{"hello": "world"},
			},
		})

		So(err, ShouldBeNil)
		So(resp.StatusCode, ShouldEqual, http.StatusOK)
		So(captured["x-account-id"], ShouldEqual, "u-9")
		So(captured[common.HeaderBKNRequestID], ShouldEqual, "req_01JZVALIDREQUESTID000000215")
		So(captured[common.HeaderLegacyRequestID], ShouldEqual, "req_01JZVALIDREQUESTID000000215")
		So(captured[common.HeaderTraceparent], ShouldEqual, "00-20212223242526272829303132333435-4041424344454647-01")
		So(captured[common.HeaderBaggage], ShouldEqual, "bkn.account.type=service")
	})
}
