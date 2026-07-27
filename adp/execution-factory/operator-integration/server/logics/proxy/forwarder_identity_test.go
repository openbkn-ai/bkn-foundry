package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	. "github.com/smartystreets/goconvey/convey"
)

const (
	testAuthAccountID = "11111111-1111-4111-8111-111111111111"
	testSpoofedID     = "22222222-2222-4222-8222-222222222222"
)

// publicCtx 构造公开接口的请求上下文：认证中间件会同时写入认证账户与公开接口标记。
func publicCtx() context.Context {
	ctx := common.SetPublicAPIToCtx(context.Background(), true)
	return common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID:   testAuthAccountID,
		AccountType: interfaces.AccessorTypeUser,
	})
}

// internalCtx 构造内部接口的请求上下文：没有公开接口标记，身份由上游运行时按 /in 约定注入。
func internalCtx() context.Context {
	return common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID:   testAuthAccountID,
		AccountType: interfaces.AccessorTypeUser,
	})
}

// TestBuildRequest_IdentityHeaders 覆盖公开接口下身份请求头的回填（#216 P1 安全约束）。
//
// 内置工具箱把 x-account-id / x-account-type 声明成普通 OpenAPI header 参数，下游 /in
// 接口不验 token、直接据此判定调用者身份，因此公开的调试与执行入口不能把调用方自填的
// 身份头原样转发出去。
func TestBuildRequest_IdentityHeaders(t *testing.T) {
	f := newTestForwarder()

	Convey("公开接口的身份请求头以认证账户为准", t, func() {
		Convey("调用方伪造的 x-account-id 被回填成认证账户", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    "http://svc/api/agent-retrieval/in/v1/kn/run_sql",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{
						"x-account-id":   testSpoofedID,
						"x-account-type": "app",
					},
					Body: map[string]any{"sql": "select 1"},
				},
			}

			httpReq, err := f.buildRequest(publicCtx(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("x-account-id"), ShouldEqual, testAuthAccountID)
			So(httpReq.Header.Get("x-account-type"), ShouldEqual, string(interfaces.AccessorTypeUser))
		})

		Convey("大小写变体同样被回填", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{
						"X-Account-Id": testSpoofedID,
						"USER_ID":      testSpoofedID,
					},
				},
			}

			httpReq, err := f.buildRequest(publicCtx(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("X-Account-Id"), ShouldEqual, testAuthAccountID)
			So(httpReq.Header.Get("user_id"), ShouldEqual, testAuthAccountID)
		})

		Convey("业务请求头不受影响", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{
						"X-Api-Key":         "secret-key",
						"x-business-domain": "domain-1",
						"x-account-id":      testSpoofedID,
					},
				},
			}

			httpReq, err := f.buildRequest(publicCtx(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("X-Api-Key"), ShouldEqual, "secret-key")
			So(httpReq.Header.Get("x-business-domain"), ShouldEqual, "domain-1")
			So(httpReq.Header.Get("x-account-id"), ShouldEqual, testAuthAccountID)
		})

		Convey("调用方没填身份头时不会凭空注入", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{"X-Api-Key": "secret-key"},
				},
			}

			httpReq, err := f.buildRequest(publicCtx(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("x-account-id"), ShouldEqual, "")
			So(httpReq.Header.Get("x-account-type"), ShouldEqual, "")
		})

		Convey("拿不到认证账户时身份头被丢弃而不是透传", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{"x-account-id": testSpoofedID},
				},
			}

			httpReq, err := f.buildRequest(common.SetPublicAPIToCtx(context.Background(), true), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("x-account-id"), ShouldEqual, "")
		})
	})

	Convey("内部接口的身份请求头原样透传", t, func() {
		req := &interfaces.HTTPRequest{
			HTTPRouter: interfaces.HTTPRouter{
				Method: http.MethodPost,
				URL:    "http://svc/api/agent-retrieval/in/v1/kn/search_schema",
			},
			HTTPRequestParams: interfaces.HTTPRequestParams{
				Headers: map[string]any{
					"x-account-id":   "runtime-injected-account",
					"x-account-type": "app",
				},
				Body: map[string]any{"query": "订单"},
			},
		}

		httpReq, err := f.buildRequest(internalCtx(), req)

		So(err, ShouldBeNil)
		So(httpReq.Header.Get("x-account-id"), ShouldEqual, "runtime-injected-account")
		So(httpReq.Header.Get("x-account-type"), ShouldEqual, "app")
	})
}

// TestBuildRequest_BodylessMethods 覆盖 GET/HEAD 的空 body 信封（#216 验收标准 10）。
func TestBuildRequest_BodylessMethods(t *testing.T) {
	f := newTestForwarder()

	Convey("GET/HEAD 的空 body 信封不发给下游", t, func() {
		Convey("GET 带空 body 时不发送请求体，也不补 Content-Type", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					QueryParams: map[string]any{"page": 1},
					Body:        map[string]any{},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("Content-Type"), ShouldEqual, "")
			So(readAll(t, httpReq.Body), ShouldEqual, "")
			So(httpReq.URL.RawQuery, ShouldEqual, "page=1")
		})

		Convey("HEAD 带空 body 时同样不发送请求体", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodHead,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{Body: map[string]any{}},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(readAll(t, httpReq.Body), ShouldEqual, "")
		})

		Convey("GET 带非空 body 时仍然发送，不误伤既有用法", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Body: map[string]any{"filter": "name"},
				},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("Content-Type"), ShouldEqual, "application/json")
			So(readAll(t, httpReq.Body), ShouldEqual, `{"filter":"name"}`)
		})

		Convey("POST 带空 body 时保持原样发送空 JSON 对象", func() {
			req := &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    "http://svc/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{Body: map[string]any{}},
			}

			httpReq, err := f.buildRequest(context.Background(), req)

			So(err, ShouldBeNil)
			So(httpReq.Header.Get("Content-Type"), ShouldEqual, "application/json")
			So(readAll(t, httpReq.Body), ShouldEqual, `{}`)
		})
	})
}

// TestForward_DownstreamReceivesAuthenticatedIdentity 端到端校验下游收到的是认证账户而非调用方自填值。
func TestForward_DownstreamReceivesAuthenticatedIdentity(t *testing.T) {
	f := newTestForwarder()

	Convey("公开接口转发后，下游拿到的身份是认证账户", t, func() {
		type captured struct {
			accountID   string
			accountType string
			contentType string
			body        string
		}
		var got captured

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			got = captured{
				accountID:   r.Header.Get("x-account-id"),
				accountType: r.Header.Get("x-account-type"),
				contentType: r.Header.Get("Content-Type"),
				body:        string(body),
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer server.Close()

		Convey("伪造的身份头在下游被替换", func() {
			resp, err := f.Forward(publicCtx(), &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodPost,
					URL:    server.URL + "/api/agent-retrieval/in/v1/kn/run_sql",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					Headers: map[string]any{
						"x-account-id":   testSpoofedID,
						"x-account-type": "app",
					},
					Body: map[string]any{"sql": "select 1"},
				},
				Timeout: 5 * time.Second,
			})

			So(err, ShouldBeNil)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)
			So(got.accountID, ShouldEqual, testAuthAccountID)
			So(got.accountType, ShouldEqual, string(interfaces.AccessorTypeUser))
			So(got.body, ShouldEqual, `{"sql":"select 1"}`)
		})

		Convey("GET 的空 body 信封不会给下游带上请求体", func() {
			resp, err := f.Forward(publicCtx(), &interfaces.HTTPRequest{
				HTTPRouter: interfaces.HTTPRouter{
					Method: http.MethodGet,
					URL:    server.URL + "/api/v1/resources",
				},
				HTTPRequestParams: interfaces.HTTPRequestParams{
					QueryParams: map[string]any{"page": 1},
					Body:        map[string]any{},
				},
				Timeout: 5 * time.Second,
			})

			So(err, ShouldBeNil)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)
			So(got.body, ShouldEqual, "")
			So(got.contentType, ShouldEqual, "")
		})
	})
}
