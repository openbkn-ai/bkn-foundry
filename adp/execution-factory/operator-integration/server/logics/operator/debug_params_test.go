package operator

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

const (
	debugOperatorID      = "5a3d1f6c-2e57-4a2f-95a1-3c1c1f0f9d21"
	debugMetadataVersion = "9b0d2a44-4f4e-4f2f-8a6f-6a4a4d0d1c33"
)

// debugOperatorFixture 组装算子调试所需的最小依赖，并回传代理实际收到的请求。
type debugOperatorFixture struct {
	manager  *operatorManager
	captured **interfaces.HTTPRequest
}

func newDebugOperatorFixture(t *testing.T) *debugOperatorFixture {
	ctrl := gomock.NewController(t)

	mockDBOperatorManager := mocks.NewMockIOperatorRegisterDB(ctrl)
	mockOpReleaseDB := mocks.NewMockIOperatorReleaseDB(ctrl)
	mockAuthService := mocks.NewMockIAuthorizationService(ctrl)
	mockMetadataService := mocks.NewMockIMetadataService(ctrl)
	mockMetadata := mocks.NewMockIMetadataDB(ctrl)
	mockProxy := mocks.NewMockProxyHandler(ctrl)
	mockAuditLog := mocks.NewMockLogModelOperator[*metric.AuditLogBuilderParams](ctrl)

	mockDBOperatorManager.EXPECT().SelectByOperatorID(gomock.Any(), nil, debugOperatorID).
		Return(true, &model.OperatorRegisterDB{
			OperatorID:      debugOperatorID,
			Name:            "op",
			MetadataVersion: debugMetadataVersion,
			MetadataType:    string(interfaces.MetadataTypeAPI),
			ExecutionMode:   string(interfaces.ExecutionModeSync),
			ExecuteControl:  `{"timeout":30}`,
		}, nil).AnyTimes()
	mockOpReleaseDB.EXPECT().SelectByOpID(gomock.Any(), debugOperatorID).
		Return(true, &model.OperatorReleaseDB{
			OpID:            debugOperatorID,
			Name:            "op",
			Status:          string(interfaces.BizStatusPublished),
			MetadataVersion: debugMetadataVersion,
			MetadataType:    string(interfaces.MetadataTypeAPI),
			ExecutionMode:   string(interfaces.ExecutionModeSync),
			ExecuteControl:  `{"timeout":30}`,
		}, nil).AnyTimes()
	mockAuthService.EXPECT().GetAccessor(gomock.Any(), "u1").
		Return(&interfaces.AuthAccessor{ID: "u1"}, nil).AnyTimes()
	mockAuthService.EXPECT().
		CheckExecutePermission(gomock.Any(), gomock.Any(), debugOperatorID, interfaces.AuthResourceTypeOperator).
		Return(nil).AnyTimes()
	mockMetadataService.EXPECT().
		CheckMetadataExists(gomock.Any(), interfaces.MetadataTypeAPI, debugMetadataVersion).
		Return(true, mockMetadata, nil).AnyTimes()
	mockMetadata.EXPECT().GetServerURL().Return("http://operator-svc").AnyTimes()
	mockMetadata.EXPECT().GetPath().Return("/api/v1/operator/market/{operator_id}").AnyTimes()
	mockMetadata.EXPECT().GetMethod().Return(http.MethodGet).AnyTimes()

	var captured *interfaces.HTTPRequest
	mockProxy.EXPECT().HandlerRequest(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error) {
			captured = req
			return &interfaces.HTTPResponse{StatusCode: http.StatusOK}, nil
		}).AnyTimes()

	return &debugOperatorFixture{
		manager: &operatorManager{
			Logger:            logger.DefaultLogger(),
			DBOperatorManager: mockDBOperatorManager,
			OpReleaseDB:       mockOpReleaseDB,
			AuthService:       mockAuthService,
			MetadataService:   mockMetadataService,
			Proxy:             mockProxy,
			AuditLog:          mockAuditLog,
		},
		captured: &captured,
	}
}

// debugOperatorRequestJSON 是 Studio 算子调试面板提交的请求体形态：header/query/path/body 四段并列。
const debugOperatorRequestJSON = `{
	"timeout": 5,
	"header": {"X-Api-Key": "secret-key"},
	"query": {"version": "v1"},
	"path": {"operator_id": "op-1"},
	"body": {"payload": "p1", "query": "业务字段 query"}
}`

// TestDebugOperator_ForwardsAllRequestParams 校验算子调试的 header/query/path/body 原样抵达代理层（#216 验收标准 5）。
func TestDebugOperator_ForwardsAllRequestParams(t *testing.T) {
	Convey("算子调试透传完整请求参数", t, func() {
		req := &interfaces.DebugOperatorReq{
			UserID:     "u1",
			OperatorID: debugOperatorID,
			Version:    debugMetadataVersion,
		}
		So(json.Unmarshal([]byte(debugOperatorRequestJSON), req), ShouldBeNil)

		Convey("四段参数在请求体反序列化时各就各位", func() {
			So(req.PathParams["operator_id"], ShouldEqual, "op-1")
			So(req.QueryParams["version"], ShouldEqual, "v1")
			So(req.Headers["X-Api-Key"], ShouldEqual, "secret-key")

			body, ok := req.Body.(map[string]any)
			So(ok, ShouldBeTrue)
			So(body["payload"], ShouldEqual, "p1")
			// body 里的同名业务字段不会被当作调试信封（#216 验收标准 12）
			So(body["query"], ShouldEqual, "业务字段 query")
		})

		Convey("DebugOperator 把四段参数交给代理层", func() {
			fixture := newDebugOperatorFixture(t)

			resp, err := fixture.manager.DebugOperator(context.Background(), req)

			So(err, ShouldBeNil)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)

			proxyReq := *fixture.captured
			So(proxyReq, ShouldNotBeNil)
			So(proxyReq.Method, ShouldEqual, http.MethodGet)
			So(proxyReq.URL, ShouldEqual, "http://operator-svc/api/v1/operator/market/{operator_id}")
			So(proxyReq.PathParams["operator_id"], ShouldEqual, "op-1")
			So(proxyReq.QueryParams["version"], ShouldEqual, "v1")
			So(proxyReq.Headers["X-Api-Key"], ShouldEqual, "secret-key")

			body, ok := proxyReq.Body.(map[string]any)
			So(ok, ShouldBeTrue)
			So(body["payload"], ShouldEqual, "p1")
			So(body["query"], ShouldEqual, "业务字段 query")
		})
	})
}

// TestExecuteOperator_ForwardsAllRequestParams 校验算子正式执行走同一条透传链路（#216 验收标准 5）。
func TestExecuteOperator_ForwardsAllRequestParams(t *testing.T) {
	Convey("算子执行透传完整请求参数", t, func() {
		fixture := newDebugOperatorFixture(t)
		req := &interfaces.ExecuteOperatorReq{UserID: "u1", OperatorID: debugOperatorID}
		So(json.Unmarshal([]byte(debugOperatorRequestJSON), req), ShouldBeNil)

		resp, err := fixture.manager.ExecuteOperator(context.Background(), req)

		So(err, ShouldBeNil)
		So(resp.StatusCode, ShouldEqual, http.StatusOK)

		proxyReq := *fixture.captured
		So(proxyReq, ShouldNotBeNil)
		So(proxyReq.PathParams["operator_id"], ShouldEqual, "op-1")
		So(proxyReq.QueryParams["version"], ShouldEqual, "v1")
		So(proxyReq.Headers["X-Api-Key"], ShouldEqual, "secret-key")
	})
}

// TestDebugOperator_NoParamsOperatorSendsEmptyEnvelope 校验无入参算子调试时不会凭空造出 path/query/header（#216 验收标准 8 的后端侧）。
func TestDebugOperator_NoParamsOperatorSendsEmptyEnvelope(t *testing.T) {
	Convey("无入参算子调试只发空信封", t, func() {
		fixture := newDebugOperatorFixture(t)
		req := &interfaces.DebugOperatorReq{
			UserID:     "u1",
			OperatorID: debugOperatorID,
			Version:    debugMetadataVersion,
		}
		So(json.Unmarshal([]byte(`{"timeout":5}`), req), ShouldBeNil)

		resp, err := fixture.manager.DebugOperator(context.Background(), req)

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
