package toolbox

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// debugToolFixture assembles the minimal dependencies required for tool debugging and returns the actual request received by the proxy.
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

// debugToolRequestJSON is the request body form actually submitted by the Studio debugging panel: header/query/path/body four sections in parallel.
const debugToolRequestJSON = `{
	"timeout": 5,
	"header": {"X-Api-Key": "secret-key"},
	"query": {"page": 1},
	"path": {"session_id": "probe-abc"},
	"body": {"code": "print(1)", "header": "业务字段 header"}
}`

// TestDebugTool_ForwardsAllRequestParams verifies that the debug request body's header/query/path/body arrives unchanged at the proxy layer (#216 Acceptance Criteria 1/3/4/5).
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
			// Business fields with the same name in the body will not be treated as debugging envelopes (#216 Acceptance Criteria 12)
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

func TestFunctionRuntimeHeadersUseTrustedRequestContextOnly(t *testing.T) {
	trustedRuntimeURL := interfaces.AOIServerURL + interfaces.SetAOIFuncExecPath("function-version")

	Convey("Function tools receive the authenticated caller and managed Interaction as internal proxy headers", t, func() {
		req := &interfaces.ExecuteToolReq{
			HTTPRequestParams: interfaces.HTTPRequestParams{
				Headers: map[string]any{
					"Authorization":       "Bearer caller-controlled",
					"bkn-conversation-id": "caller-controlled",
				},
			},
			RequestAuthorization: "Bearer trusted-token",
			BKNConversationID:    "conv_trusted",
			BKNInteractionID:     "int_trusted",
		}

		params := functionRuntimeHeaders(req.HTTPRequestParams, req, trustedRuntimeURL)

		So(params.Headers["Authorization"], ShouldEqual, "Bearer trusted-token")
		So(params.Headers["bkn-conversation-id"], ShouldEqual, "conv_trusted")
		So(params.Headers["bkn-interaction-id"], ShouldEqual, "int_trusted")
		So(req.Headers["Authorization"], ShouldEqual, "Bearer caller-controlled")
	})

	Convey("Incomplete managed context does not forward a credential to Function execution", t, func() {
		req := &interfaces.ExecuteToolReq{
			RequestAuthorization: "Bearer trusted-token",
			BKNConversationID:    "conv_only",
		}

		params := functionRuntimeHeaders(req.HTTPRequestParams, req, trustedRuntimeURL)

		So(params.Headers, ShouldBeNil)
	})

	Convey("Untrusted Function endpoints never receive server-captured credentials or Interaction context", t, func() {
		req := &interfaces.ExecuteToolReq{
			RequestAuthorization: "Bearer trusted-token",
			BKNConversationID:    "conv_trusted",
			BKNInteractionID:     "int_trusted",
		}

		params := functionRuntimeHeaders(req.HTTPRequestParams, req, "https://external.example/functions/run")

		So(params.Headers, ShouldBeNil)
	})

	Convey("A trusted server URL plus an imported path cannot redirect managed credentials", t, func() {
		req := &interfaces.ExecuteToolReq{
			RequestAuthorization: "Bearer trusted-token",
			BKNConversationID:    "conv_trusted",
			BKNInteractionID:     "int_trusted",
		}

		params := functionRuntimeHeaders(req.HTTPRequestParams, req, interfaces.AOIServerURL+"@evil.example/collect")

		So(params.Headers, ShouldBeNil)
	})
}

// TestDebugTool_NoParamsToolSendsEmptyEnvelope verifies that path/query/header will not be created out of thin air when debugging the tool without input parameters (#216 backend side of acceptance criterion 8).
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
