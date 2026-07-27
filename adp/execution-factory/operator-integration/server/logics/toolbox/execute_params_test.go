package toolbox

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// debugToolFixture 组装工具调试所需的最小依赖，并回传代理实际收到的请求。
type debugToolFixture struct {
	service  *ToolServiceImpl
	captured **interfaces.HTTPRequest
}

func newDebugToolFixture(t *testing.T, toolStatus string) *debugToolFixture {
	ctrl := gomock.NewController(t)

	mockAuthService := mocks.NewMockIAuthorizationService(ctrl)
	mockToolBoxDB := mocks.NewMockIToolboxDB(ctrl)
	mockToolDB := mocks.NewMockIToolDB(ctrl)
	mockMetadataService := mocks.NewMockIMetadataService(ctrl)
	mockMetadata := mocks.NewMockIMetadataDB(ctrl)
	mockProxy := mocks.NewMockProxyHandler(ctrl)
	mockAuditLog := mocks.NewMockLogModelOperator[*metric.AuditLogBuilderParams](ctrl)

	mockAuthService.EXPECT().GetAccessor(gomock.Any(), "u1").
		Return(&interfaces.AuthAccessor{ID: "u1"}, nil).AnyTimes()
	mockAuthService.EXPECT().
		CheckExecutePermission(gomock.Any(), gomock.Any(), "b1", interfaces.AuthResourceTypeToolBox).
		Return(nil).AnyTimes()
	mockToolBoxDB.EXPECT().SelectToolBox(gomock.Any(), "b1").
		Return(true, &model.ToolboxDB{BoxID: "b1", Name: "box", ServerURL: "http://tool-box-svc"}, nil).AnyTimes()
	mockToolDB.EXPECT().SelectTool(gomock.Any(), "t1").
		Return(true, &model.ToolDB{
			ToolID:     "t1",
			BoxID:      "b1",
			Name:       "tool",
			SourceID:   "s1",
			SourceType: model.SourceTypeOpenAPI,
			Status:     toolStatus,
		}, nil).AnyTimes()
	mockMetadataService.EXPECT().GetMetadataBySource(gomock.Any(), "s1", model.SourceTypeOpenAPI).
		Return(true, mockMetadata, nil).AnyTimes()
	mockMetadata.EXPECT().GetServerURL().Return("http://metadata-svc").AnyTimes()
	mockMetadata.EXPECT().GetPath().Return("/api/v1/executions/sessions/{session_id}/execute-sync").AnyTimes()
	mockMetadata.EXPECT().GetMethod().Return(http.MethodPost).AnyTimes()

	var captured *interfaces.HTTPRequest
	mockProxy.EXPECT().HandlerRequest(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error) {
			captured = req
			return &interfaces.HTTPResponse{StatusCode: http.StatusOK}, nil
		}).AnyTimes()

	return &debugToolFixture{
		service: &ToolServiceImpl{
			Logger:          logger.DefaultLogger(),
			AuthService:     mockAuthService,
			ToolBoxDB:       mockToolBoxDB,
			ToolDB:          mockToolDB,
			MetadataService: mockMetadataService,
			Proxy:           mockProxy,
			AuditLog:        mockAuditLog,
		},
		captured: &captured,
	}
}

// debugToolRequestJSON 是 Studio 调试面板实际提交的请求体形态：header/query/path/body 四段并列。
const debugToolRequestJSON = `{
	"timeout": 5,
	"header": {"X-Api-Key": "secret-key"},
	"query": {"page": 1},
	"path": {"session_id": "probe-abc"},
	"body": {"code": "print(1)", "header": "业务字段 header"}
}`

// TestDebugTool_ForwardsAllRequestParams 校验调试请求体的 header/query/path/body 原样抵达代理层（#216 验收标准 1/3/4/5）。
func TestDebugTool_ForwardsAllRequestParams(t *testing.T) {
	Convey("工具调试透传完整请求参数", t, func() {
		req := &interfaces.ExecuteToolReq{UserID: "u1", BoxID: "b1", ToolID: "t1"}
		So(json.Unmarshal([]byte(debugToolRequestJSON), req), ShouldBeNil)

		Convey("四段参数在请求体反序列化时各就各位", func() {
			So(req.PathParams["session_id"], ShouldEqual, "probe-abc")
			So(req.QueryParams["page"], ShouldEqual, float64(1))
			So(req.Headers["X-Api-Key"], ShouldEqual, "secret-key")

			body, ok := req.Body.(map[string]any)
			So(ok, ShouldBeTrue)
			So(body["code"], ShouldEqual, "print(1)")
			// body 里的同名业务字段不会被当作调试信封（#216 验收标准 12）
			So(body["header"], ShouldEqual, "业务字段 header")
		})

		Convey("DebugTool 把四段参数交给代理层", func() {
			fixture := newDebugToolFixture(t, string(interfaces.ToolStatusTypeEnabled))

			resp, err := fixture.service.DebugTool(context.Background(), req)

			So(err, ShouldBeNil)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)

			proxyReq := *fixture.captured
			So(proxyReq, ShouldNotBeNil)
			So(proxyReq.Method, ShouldEqual, http.MethodPost)
			So(proxyReq.URL, ShouldEqual, "http://tool-box-svc/api/v1/executions/sessions/{session_id}/execute-sync")
			So(proxyReq.PathParams["session_id"], ShouldEqual, "probe-abc")
			So(proxyReq.QueryParams["page"], ShouldEqual, float64(1))
			So(proxyReq.Headers["X-Api-Key"], ShouldEqual, "secret-key")

			body, ok := proxyReq.Body.(map[string]any)
			So(ok, ShouldBeTrue)
			So(body["code"], ShouldEqual, "print(1)")
			So(body["header"], ShouldEqual, "业务字段 header")
		})

		Convey("ExecuteTool 走同一条透传链路", func() {
			fixture := newDebugToolFixture(t, string(interfaces.ToolStatusTypeEnabled))

			resp, err := fixture.service.ExecuteTool(context.Background(), req)

			So(err, ShouldBeNil)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)

			proxyReq := *fixture.captured
			So(proxyReq, ShouldNotBeNil)
			So(proxyReq.PathParams["session_id"], ShouldEqual, "probe-abc")
			So(proxyReq.QueryParams["page"], ShouldEqual, float64(1))
			So(proxyReq.Headers["X-Api-Key"], ShouldEqual, "secret-key")
		})
	})
}

// TestDebugTool_NoParamsToolSendsEmptyEnvelope 校验无入参工具调试时不会凭空造出 path/query/header（#216 验收标准 8 的后端侧）。
func TestDebugTool_NoParamsToolSendsEmptyEnvelope(t *testing.T) {
	Convey("无入参工具调试只发空信封", t, func() {
		fixture := newDebugToolFixture(t, string(interfaces.ToolStatusTypeEnabled))
		req := &interfaces.ExecuteToolReq{UserID: "u1", BoxID: "b1", ToolID: "t1"}
		So(json.Unmarshal([]byte(`{"timeout":5}`), req), ShouldBeNil)

		resp, err := fixture.service.DebugTool(context.Background(), req)

		So(err, ShouldBeNil)
		So(resp.StatusCode, ShouldEqual, http.StatusOK)

		proxyReq := *fixture.captured
		So(proxyReq, ShouldNotBeNil)
		So(proxyReq.PathParams, ShouldBeEmpty)
		So(proxyReq.QueryParams, ShouldBeEmpty)
		So(proxyReq.Headers, ShouldBeEmpty)
		So(proxyReq.Body, ShouldBeNil)
	})
}
