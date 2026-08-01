package proxy

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	myErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	. "github.com/smartystreets/goconvey/convey"
)

// newTestForwarder 手工装配转发器，避免 NewForwarder/NewClientPool 读取配置文件。
func newTestForwarder() *forwarder {
	return &forwarder{
		pool: &clientPool{
			clients: make(map[clientKey]*ProxyClient),
			config: PoolConfig{
				MaxClients:     4,
				MaxTimeout:     30 * time.Second,
				DefaultTimeout: 10 * time.Second,
			},
			stopCleanup: make(chan struct{}),
			logger:      logger.DefaultLogger(),
		},
		logger: logger.DefaultLogger(),
	}
}

func readAll(t *testing.T, body io.ReadCloser) string {
	t.Helper()
	if body == nil {
		return ""
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	return string(data)
}

// TestBuildRequest_PathParams 覆盖 path 参数占位符替换（#216 验收标准 1）。
func TestBuildRequest_PathParams(t *testing.T) {
	f := newTestForwarder()

	Convey("path 参数替换 URL 占位符", t, func() {
		Convey("花括号占位符被替换", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    "http://svc/api/v1/executions/sessions/{session_id}/execute-sync",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams: map[string]string{"session_id": "probe-abc"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.URL.Path, ShouldEqual, "/api/v1/executions/sessions/probe-abc/execute-sync")
		})

		Convey("多个占位符全部被替换", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/box/{box_id}/tool/{tool_id}",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams: map[string]string{"box_id": "b1", "tool_id": "t1"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.URL.Path, ShouldEqual, "/api/v1/box/b1/tool/t1")
		})

		Convey("冒号占位符被替换", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/operator/market/:operator_id",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams: map[string]string{"operator_id": "op-1"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.URL.Path, ShouldEqual, "/api/v1/operator/market/op-1")
		})

		Convey("冒号加花括号的占位符被整体替换，不留多余冒号", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/operator/market/:{operator_id}",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams: map[string]string{"operator_id": "op-1"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.URL.Path, ShouldEqual, "/api/v1/operator/market/op-1")
		})

		Convey("未提供对应 path 参数时拒绝发送并给出明确错误（#216 验收标准 7）", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/operator/market/{operator_id}",
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(httpReq, ShouldBeNil)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*myErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.Code, ShouldEndWith, string(myErr.ErrExtProxyPathParamMissing))
			So(httpErr.ErrorDetails, ShouldContainSubstring, "operator_id")
		})

		Convey("path 参数只给一半时同样被拒绝", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/box/{box_id}/tool/{tool_id}",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams: map[string]string{"box_id": "b1"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(httpReq, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.(*myErr.HTTPError).ErrorDetails, ShouldContainSubstring, "tool_id")
		})

		Convey("query 里的花括号是业务值，不触发占位符拦截", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    `http://svc/api/v1/resources?filter={"name":"n1"}`,
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.URL.Query().Get("filter"), ShouldEqual, `{"name":"n1"}`)
		})
	})
}

// TestBuildRequest_QueryParams 覆盖 query 参数拼接（#216 验收标准 3）。
func TestBuildRequest_QueryParams(t *testing.T) {
	f := newTestForwarder()

	Convey("query 参数写入请求 URL", t, func() {
		Convey("追加到无 query 的 URL", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					QueryParams: map[string]any{"page": 1, "size": 20, "active": true},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			query := httpReq.URL.Query()
			So(query.Get("page"), ShouldEqual, "1")
			So(query.Get("size"), ShouldEqual, "20")
			So(query.Get("active"), ShouldEqual, "true")
		})

		Convey("与 URL 自带 query 合并而非覆盖", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources?from=metadata",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					QueryParams: map[string]any{"page": 2},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			query := httpReq.URL.Query()
			So(query.Get("from"), ShouldEqual, "metadata")
			So(query.Get("page"), ShouldEqual, "2")
		})

		Convey("需要转义的值被正确编码", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					QueryParams: map[string]any{"keyword": "a b&c=d"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.URL.RawQuery, ShouldEqual, "keyword=a+b%26c%3Dd")
			So(httpReq.URL.Query().Get("keyword"), ShouldEqual, "a b&c=d")
		})

		Convey("query 与 path 同时存在时互不干扰", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/box/{box_id}/tools",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams:  map[string]string{"box_id": "b1"},
					QueryParams: map[string]any{"page": 1},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.URL.Path, ShouldEqual, "/api/v1/box/b1/tools")
			So(httpReq.URL.Query().Get("page"), ShouldEqual, "1")
		})
	})
}

// TestBuildRequest_Headers 覆盖自定义请求头透传（#216 验收标准 4）。
func TestBuildRequest_Headers(t *testing.T) {
	f := newTestForwarder()

	Convey("自定义请求头写入下游请求", t, func() {
		Convey("业务头与鉴权头原样设置", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{
						"X-Api-Key":       "secret-key",
						"X-Tenant-Id":     "tenant-1",
						"Authorization":   "Bearer token-1",
						"X-Numeric-Value": 42,
					},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("X-Api-Key"), ShouldEqual, "secret-key")
			So(httpReq.Header.Get("X-Tenant-Id"), ShouldEqual, "tenant-1")
			So(httpReq.Header.Get("Authorization"), ShouldEqual, "Bearer token-1")
			So(httpReq.Header.Get("X-Numeric-Value"), ShouldEqual, "42")
		})

		Convey("传输层请求头被丢弃，不透传给下游（#216 P1 安全约束）", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{
						"Host":              "evil.example.com",
						"Connection":        "close",
						"transfer-encoding": "chunked",
						"Content-Length":    "999",
						"Upgrade":           "websocket",
						"X-Company-Token":   "keep-me",
					},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.Host, ShouldEqual, "svc")
			So(httpReq.Header.Get("Host"), ShouldEqual, "")
			So(httpReq.Header.Get("Connection"), ShouldEqual, "")
			So(httpReq.Header.Get("Transfer-Encoding"), ShouldEqual, "")
			So(httpReq.Header.Get("Content-Length"), ShouldEqual, "")
			So(httpReq.Header.Get("Upgrade"), ShouldEqual, "")
			// 自定义鉴权头不在拦截范围内
			So(httpReq.Header.Get("X-Company-Token"), ShouldEqual, "keep-me")
		})

		Convey("content-type 识别大小写不敏感，且不被默认值覆盖", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{"content-type": "application/json; charset=utf-8"},
					Body:    map[string]any{"name": "n1"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("Content-Type"), ShouldEqual, "application/json; charset=utf-8")
			So(readAll(t, httpReq.Body), ShouldEqual, `{"name":"n1"}`)
		})
	})
}

// TestBuildRequest_Body 覆盖请求体编码（#216 验收标准 5）。
func TestBuildRequest_Body(t *testing.T) {
	f := newTestForwarder()

	Convey("请求体按 Content-Type 编码", t, func() {
		Convey("未声明 Content-Type 时按 JSON 编码并补默认头", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Body: map[string]any{"name": "n1"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("Content-Type"), ShouldEqual, "application/json")
			So(readAll(t, httpReq.Body), ShouldEqual, `{"name":"n1"}`)
		})

		Convey("表单编码", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{"Content-Type": "application/x-www-form-urlencoded"},
					Body:    map[string]interface{}{"name": "n1"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("Content-Type"), ShouldEqual, "application/x-www-form-urlencoded")
			So(readAll(t, httpReq.Body), ShouldEqual, "name=n1")
		})

		Convey("multipart 编码时 Content-Type 带 boundary 并可被下游解析", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{"Content-Type": "multipart/form-data"},
					Body:    map[string]interface{}{"name": "n1"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			mediaType, params, parseErr := mime.ParseMediaType(httpReq.Header.Get("Content-Type"))
			So(parseErr, ShouldBeNil)
			So(mediaType, ShouldEqual, "multipart/form-data")
			So(params["boundary"], ShouldNotBeEmpty)

			reader := multipart.NewReader(httpReq.Body, params["boundary"])
			form, formErr := reader.ReadForm(1 << 20)
			So(formErr, ShouldBeNil)
			// 字段值按 JSON 序列化写入（现状），字符串因此带引号
			So(form.Value["name"][0], ShouldEqual, `"n1"`)
		})

		Convey("纯文本编码", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{"Content-Type": "text/plain"},
					Body:    "hello",
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(readAll(t, httpReq.Body), ShouldEqual, "hello")
		})

		Convey("无请求体时不发送 body，也不补 Content-Type", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{"X-Api-Key": "secret-key"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("Content-Type"), ShouldEqual, "")
			So(readAll(t, httpReq.Body), ShouldEqual, "")
			So(httpReq.Header.Get("X-Api-Key"), ShouldEqual, "secret-key")
		})
	})
}

// TestBuildRequest_BusinessFieldsNamedLikeEnvelope 覆盖请求体自带 header/query/path/body 同名业务字段的场景（#216 验收标准 12）。
func TestBuildRequest_BusinessFieldsNamedLikeEnvelope(t *testing.T) {
	f := newTestForwarder()

	Convey("body 中的 header/query/path/body 同名字段不被当成调试信封", t, func() {
		req := &interfaces.HTTPRequest{
			HTTPRouter: interfaces.HTTPRouter{
				Method: http.MethodPost,
				URL:    "http://svc/api/v1/records",
			},
			HTTPRequestParams: interfaces.HTTPRequestParams{
				Headers: map[string]any{"X-Api-Key": "secret-key"},
				Body: map[string]any{
					"header": "业务字段 header",
					"query":  "业务字段 query",
					"path":   "业务字段 path",
					"body":   "业务字段 body",
				},
			},
		}

		httpReq, err := f.buildRequest(context.Background(), req)

		So(err, ShouldBeNil)
		// 业务字段留在 body 里，既不会跑到 URL，也不会变成请求头
		So(httpReq.URL.RawQuery, ShouldEqual, "")
		So(httpReq.Header.Get("query"), ShouldEqual, "")

		var got map[string]any
		So(json.Unmarshal([]byte(readAll(t, httpReq.Body)), &got), ShouldBeNil)
		So(got["header"], ShouldEqual, "业务字段 header")
		So(got["query"], ShouldEqual, "业务字段 query")
		So(got["path"], ShouldEqual, "业务字段 path")
		So(got["body"], ShouldEqual, "业务字段 body")
	})
}

// TestForward_DownstreamReceivesAllParams 端到端校验下游实际收到 header + query + path + body（#216 验收标准 1/3/4/5/10）。
func TestForward_DownstreamReceivesAllParams(t *testing.T) {
	f := newTestForwarder()

	Convey("下游收到完整的 header/query/path/body", t, func() {
		type captured struct {
			method string
			path   string
			query  string
			apiKey string
			body   string
		}
		var got captured

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			got = captured{
				method: r.Method,
				path:   r.URL.Path,
				query:  r.URL.RawQuery,
				apiKey: r.Header.Get("X-Api-Key"),
				body:   string(body),
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer server.Close()

		Convey("POST 请求带 path/query/header/body", func() {
			resp, err := f.Forward(t.Context(), &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    server.URL + "/api/v1/executions/sessions/{session_id}/execute-sync",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams:  map[string]string{"session_id": "probe-abc"},
					QueryParams: map[string]any{"trace": "on"},
					Headers:     map[string]any{"X-Api-Key": "secret-key"},
					Body:        map[string]any{"code": "print(1)"},
				},
				Timeout: 5 * time.Second,
			})

			So(err, ShouldBeNil)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)
			So(got.method, ShouldEqual, http.MethodPost)
			So(got.path, ShouldEqual, "/api/v1/executions/sessions/probe-abc/execute-sync")
			So(got.query, ShouldEqual, "trace=on")
			So(got.apiKey, ShouldEqual, "secret-key")
			So(got.body, ShouldEqual, `{"code":"print(1)"}`)

			respBody, ok := resp.Body.(map[string]any)
			So(ok, ShouldBeTrue)
			So(respBody["ok"], ShouldEqual, true)
		})

		Convey("GET 请求同样完成 path 替换与 query 拼接", func() {
			resp, err := f.Forward(t.Context(), &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    server.URL + "/api/v1/operator/market/{operator_id}",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					PathParams:  map[string]string{"operator_id": "op-1"},
					QueryParams: map[string]any{"version": "v1"},
					Headers:     map[string]any{"X-Api-Key": "secret-key"},
				},
				Timeout: 5 * time.Second,
			})

			So(err, ShouldBeNil)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)
			So(got.method, ShouldEqual, http.MethodGet)
			So(got.path, ShouldEqual, "/api/v1/operator/market/op-1")
			So(got.query, ShouldEqual, "version=v1")
			So(got.apiKey, ShouldEqual, "secret-key")
			So(got.body, ShouldEqual, "")
		})
	})
}
