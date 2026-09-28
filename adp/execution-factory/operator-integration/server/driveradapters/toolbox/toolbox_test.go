package toolbox

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func TestConvertOperatorToToolExposesCommittedToolIDForManagementAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	toolService := mocks.NewMockIToolService(gomock.NewController(t))
	toolService.EXPECT().ConvertOperatorToTool(gomock.Any(), gomock.Any()).Return(
		&interfaces.ConvertOperatorToToolResp{BoxID: "box-1", ToolID: "created-tool-1"}, nil,
	)
	handler := &toolBoxHandler{ToolService: toolService}
	engine := gin.New()
	gotToolID := ""
	engine.Use(func(c *gin.Context) { c.Next(); gotToolID = c.GetString("bkn.operation_audit.tool_id") })
	engine.POST("/operator/convert/tool", handler.OperatorToTool)
	request := httptest.NewRequest(http.MethodPost, "/operator/convert/tool", strings.NewReader(`{"operator_id":"op-1","box_id":"box-1"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("user_id", "user-1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || gotToolID != "created-tool-1" {
		t.Fatalf("response=%d audit tool ID=%q, want created-tool-1", response.Code, gotToolID)
	}
}

func TestOpenAPIBundleExposesPartialResultForManagementAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	toolService := mocks.NewMockIToolService(gomock.NewController(t))
	toolService.EXPECT().RegisterOpenApiBundle(gomock.Any(), gomock.Any()).Return(
		&interfaces.RegisterOpenApiBundleResp{BoxID: "box-1", ToolIDs: []string{"tool-1"}, FailureCount: 1}, nil,
	)
	handler := &toolBoxHandler{ToolService: toolService}
	engine := gin.New()
	partial := false
	engine.Use(func(c *gin.Context) { c.Next(); partial = c.GetBool("bkn.operation_audit.partial_bundle") })
	engine.POST("/capabilities/openapi-bundle", handler.RegisterOpenApiBundle)
	request := httptest.NewRequest(http.MethodPost, "/capabilities/openapi-bundle", strings.NewReader(`{"box_id":"box-1","box_svc_url":"https://example.test","data":"{}"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("user_id", "user-1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !partial {
		t.Fatalf("response=%d partial=%v, want accepted partial bundle marked for Audit", response.Code, partial)
	}
}

func TestCreateToolBox(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockLogger := mocks.NewMockLogger(ctrl)
	toolService := mocks.NewMockIToolService(ctrl)
	validator := mocks.NewMockValidator(ctrl)
	handler := &toolBoxHandler{
		Logger:      mockLogger,
		ToolService: toolService,
		Validator:   validator,
	}
	path := "/tool-box"
	contentType := "Content-Type"
	applicationJSON := "application/json"
	applicationUrlencoded := "application/x-www-form-urlencoded"
	headers := map[string]string{
		contentType: applicationJSON,
	}
	Convey("TestCreateToolBox", t, func() {
		Convey("TestCreateToolBox: application/json 参数为空", func() {
			recorder := mocks.MockPostRequest(path, headers, http.NoBody, handler.CreateToolBox)
			fmt.Println(recorder.Body.String())
			So(recorder.Code, ShouldEqual, http.StatusBadRequest)
		})
		Convey("TestCreateToolBox: application/json 参数错误", func() {
			recorder := mocks.MockPostRequest(path, headers, strings.NewReader(`{}`), handler.CreateToolBox)
			fmt.Println(recorder.Body.String())
			So(recorder.Code, ShouldEqual, http.StatusBadRequest)
		})
		Convey("TestCreateToolBox: application/x-www-form-urlencoded 参数错误", func() {
			headers[contentType] = applicationUrlencoded
			headers["user_id"] = "1"
			formData := url.Values{}
			formData.Add("metadata_type", "test")
			formData.Add("data", "1")
			recorder := mocks.MockPostRequest(path, headers,
				strings.NewReader(formData.Encode()),
				handler.CreateToolBox)
			fmt.Println(recorder.Body.String())
			So(recorder.Code, ShouldEqual, http.StatusBadRequest)
		})
	})
}
