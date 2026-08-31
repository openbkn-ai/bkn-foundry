package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	myErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
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

func TestBuildRequest_PathQueryHeaderBody(t *testing.T) {
	Convey("buildRequest 按 OpenAPI 参数位置拼出下游请求（#216）", t, func() {
		f := &forwarder{logger: logger.DefaultLogger()}
		ctx := context.Background()

		Convey("path 参数三种写法都会被替换", func() {
			for _, template := range []string{
				"http://svc:9000/resources/{resource_id}/detail",
				"http://svc:9000/resources/:{resource_id}/detail",
				"http://svc:9000/resources/:resource_id/detail",
			} {
				httpReq, err := f.buildRequest(ctx, &interfaces.HTTPRequest{
					HTTPRouter: interfaces.HTTPRouter{Method: http.MethodGet, URL: template},
					HTTPRequestParams: interfaces.HTTPRequestParams{
						PathParams: map[string]string{"resource_id": "res-42"},
					},
				})

				So(err, ShouldBeNil)
				So(httpReq.URL.String(), ShouldEqual, "http://svc:9000/resources/res-42/detail")
			}
		})

		Convey("path 值按路径段转义，不能改写 URL 结构", func() {
			httpReq, err := f.buildRequest(ctx, &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{Method: http.MethodGet, URL: "http://svc:9000/files/{name}"},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams: map[string]string{"name": "../admin?x=1"},
				},
			})

			So(err, ShouldBeNil)
			So(httpReq.URL.Path, ShouldEqual, "/files/../admin?x=1")
			So(httpReq.URL.EscapedPath(), ShouldEqual, "/files/..%2Fadmin%3Fx=1")
			So(httpReq.URL.RawQuery, ShouldEqual, "")
		})

		Convey("冒号写法要求名字边界，不能吃掉更长的参数名", func() {
			httpReq, err := f.buildRequest(ctx, &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{Method: http.MethodGet, URL: "http://svc:9000/x/:id/:identifier"},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams: map[string]string{"id": "1", "identifier": "2"},
				},
			})

			So(err, ShouldBeNil)
			So(httpReq.URL.Path, ShouldEqual, "/x/1/2")
		})

		Convey("占位符没填齐时拒发，不把 {name} 原样发给下游", func() {
			_, err := f.buildRequest(ctx, &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{Method: http.MethodGet, URL: "http://svc:9000/resources/{resource_id}"},
			})

			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*myErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.ErrorDetails, ShouldContainSubstring, "resource_id")
		})

		Convey("query 参数追加到 URL，且不丢模板里已有的 query", func() {
			httpReq, err := f.buildRequest(ctx, &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{Method: http.MethodGet, URL: "http://svc:9000/search?region=r1"},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					QueryParams: map[string]any{"limit": 20, "keyword": "a b"},
				},
			})

			So(err, ShouldBeNil)
			query := httpReq.URL.Query()
			So(query.Get("region"), ShouldEqual, "r1")
			So(query.Get("limit"), ShouldEqual, "20")
			So(query.Get("keyword"), ShouldEqual, "a b")
		})

		Convey("header 逐个落到下游请求头上", func() {
			httpReq, err := f.buildRequest(ctx, &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{Method: http.MethodGet, URL: "http://svc:9000/ping"},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{"X-Region-Id": "r-1", "X-Api-Key": "ak-live"},
				},
			})

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("X-Region-Id"), ShouldEqual, "r-1")
			So(httpReq.Header.Get("X-Api-Key"), ShouldEqual, "ak-live")
		})

		Convey("body 默认按 JSON 编码，Content-Type 自动补齐", func() {
			httpReq, err := f.buildRequest(ctx, &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{Method: http.MethodPost, URL: "http://svc:9000/items"},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Body: map[string]any{"name": "demo"},
				},
			})

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("Content-Type"), ShouldEqual, "application/json")
			payload, readErr := io.ReadAll(httpReq.Body)
			So(readErr, ShouldBeNil)
			So(string(payload), ShouldEqual, `{"name":"demo"}`)
		})

		Convey("四个位置同时给全时互不串位", func() {
			httpReq, err := f.buildRequest(ctx, &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    "http://svc:9000/regions/{region_id}/items",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams:  map[string]string{"region_id": "r-9"},
					QueryParams: map[string]any{"dry_run": true},
					Headers:     map[string]any{"X-Account-Id": "u-1"},
					Body:        map[string]any{"name": "demo"},
				},
			})

			So(err, ShouldBeNil)
			So(httpReq.URL.Path, ShouldEqual, "/regions/r-9/items")
			So(httpReq.URL.Query().Get("dry_run"), ShouldEqual, "true")
			So(httpReq.Header.Get("X-Account-Id"), ShouldEqual, "u-1")
			payload, readErr := io.ReadAll(httpReq.Body)
			So(readErr, ShouldBeNil)
			So(string(payload), ShouldEqual, `{"name":"demo"}`)
		})
	})
}

func TestBuildRequestUsesEffectiveLocale(t *testing.T) {
	Convey("buildRequest forwards the resolved locale instead of caller preferences", t, func() {
		forwarder := &forwarder{logger: logger.DefaultLogger()}
		ctx := sharedrest.WithLanguage(context.Background(), sharedrest.AmericanEnglish)
		httpReq, err := forwarder.buildRequest(ctx, &interfaces.HTTPRequest{
			HTTPRouter: interfaces.HTTPRouter{Method: http.MethodGet, URL: "http://svc:9000/ping"},
			HTTPRequestParams: interfaces.HTTPRequestParams{
				Headers: map[string]any{sharedrest.AcceptLanguageHeader: "zh-CN, en-US;q=0.8"},
			},
		})

		So(err, ShouldBeNil)
		So(httpReq.Header.Get(sharedrest.AcceptLanguageHeader), ShouldEqual, sharedrest.AmericanEnglish)
	})
}

func TestPreprocessResponseHeadersPreservesStrictCacheControls(t *testing.T) {
	Convey("preprocessResponseHeaders keeps stricter upstream cache directives", t, func() {
		writer := httptest.NewRecorder()
		writer.Header().Set("Cache-Control", "public, no-store")
		writer.Header().Set("Content-Language", "en-US")
		writer.Header().Set("Vary", "Accept-Encoding")

		preprocessResponseHeaders(interfaces.StreamingModeSSE, writer)

		So(writer.Header().Get("Cache-Control"), ShouldEqual, "no-store, private")
		So(writer.Header().Get("Content-Language"), ShouldEqual, "en-US")
		So(writer.Header().Get("Vary"), ShouldEqual, "Accept-Encoding")
	})
}
