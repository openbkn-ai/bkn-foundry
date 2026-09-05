// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func MockNewBKNRestHandler(
	appSetting *common.AppSetting,
	as interfaces.AuthService,
	kns interfaces.KNService,
	bs interfaces.BKNService,
) *restHandler {
	return &restHandler{
		appSetting: appSetting,
		as:         as,
		kns:        kns,
		bs:         bs,
	}
}

// newValidBKNTar creates a minimal valid BKN tar archive for tests.
func newValidBKNTar(t *testing.T) []byte {
	net := &bknsdk.BknNetwork{
		BknNetworkFrontmatter: bknsdk.BknNetworkFrontmatter{
			Type:    "network",
			ID:      "test-net",
			Name:    "Test Network",
			Version: "1.0.0",
		},
	}
	var buf bytes.Buffer
	if err := bknsdk.WriteNetworkToTar(net, &buf); err != nil {
		t.Fatalf("failed to create test BKN tar: %v", err)
	}
	return buf.Bytes()
}

// newMultipartRequestWithContentType builds a file-upload request with the specified Content-Type for extension-validation tests.
func newMultipartRequestWithContentType(t *testing.T, url, filename, contentType string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	h.Set("Content-Type", contentType)
	fw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("failed to create form part: %v", err)
	}
	_, _ = fw.Write(content)
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// newMultipartRequest builds a multipart/form-data request containing a file.
func newMultipartRequest(t *testing.T, url, filename string, content []byte) *http.Request {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	_, _ = fw.Write(content)
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func Test_BKNRestHandler_UploadBKN(t *testing.T) {
	Convey("Test BKNHandler UploadBKN\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		as := bmock.NewMockAuthService(mockCtrl)
		kns := bmock.NewMockKNService(mockCtrl)
		bs := bmock.NewMockBKNService(mockCtrl)

		handler := MockNewBKNRestHandler(appSetting, as, kns, bs)
		handler.RegisterPublic(engine)

		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{}, nil)

		url := "/api/bkn-backend/v1/bkns"

		Convey("Success with authenticated request\n", func() {
			kns.EXPECT().CreateKN(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("kn1", nil)

			req := newMultipartRequest(t, url, "test.tar", newValidBKNTar(t))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})

		Convey("Success without optional headers\n", func() {
			kns.EXPECT().CreateKN(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("kn1", nil)

			req := newMultipartRequest(t, url, "test.tar", newValidBKNTar(t))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})

		Convey("Failed when no file uploaded\n", func() {
			req := httptest.NewRequest(http.MethodPost, url, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed when file has invalid extension\n", func() {
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			fw, _ := mw.CreateFormFile("file", "test.json")
			_, _ = fw.Write([]byte(`{"invalid": "content"}`))
			_ = mw.Close()

			req := httptest.NewRequest(http.MethodPost, url, &body)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed when tar content is invalid\n", func() {
			req := newMultipartRequest(t, url, "test.tar", []byte("this is not a valid tar"))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed when binding_policy is invalid\n", func() {
			req := newMultipartRequest(t, url+"?binding_policy=copy", "test.tar", newValidBKNTar(t))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed when CreateKN returns error\n", func() {
			err := &rest.HTTPError{
				HTTPCode: http.StatusInternalServerError,
				Language: rest.DefaultLanguage,
				BaseError: rest.BaseError{
					ErrorCode: berrors.BknBackend_KnowledgeNetwork_InternalError,
				},
			}
			kns.EXPECT().CreateKN(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", err)

			req := newMultipartRequest(t, url, "test.tar", newValidBKNTar(t))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusInternalServerError)
		})
	})
}

func TestDetachBKNExternalBindingsPreservesTopology(t *testing.T) {
	relation := &interfaces.RelationType{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
		RTID: "rt-1", SourceObjectTypeID: "ot-1", TargetObjectTypeID: "ot-2",
	}}
	metric := &interfaces.MetricDefinition{ID: "metric-1", ScopeType: interfaces.ScopeTypeObjectType, ScopeRef: "ot-1"}
	kn := &interfaces.KN{
		ObjectTypes: []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: "ot-1", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
			LogicProperties: []*interfaces.LogicProperty{{
				Name: "forecast", Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
				DataSource: &interfaces.ResourceInfo{Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL, BoxID: "box-1", ToolID: "tool-1"},
			}},
		}}},
		RelationTypes: []*interfaces.RelationType{relation},
		Metrics:       []*interfaces.MetricDefinition{metric},
		ActionTypes: []*interfaces.ActionType{{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "at-1", ObjectTypeID: "ot-1",
			ActionSource: interfaces.ActionSource{Type: interfaces.ACTION_SOURCE_TYPE_MCP, McpID: "mcp-1", ToolName: "run"},
		}}},
	}

	detachBKNExternalBindings(kn)

	if kn.ObjectTypes[0].DataSource != nil || kn.ObjectTypes[0].LogicProperties[0].DataSource != nil {
		t.Fatalf("object bindings were not detached: %#v", kn.ObjectTypes[0])
	}
	if kn.ActionTypes[0].ActionSource != (interfaces.ActionSource{}) {
		t.Fatalf("action source was not detached: %#v", kn.ActionTypes[0].ActionSource)
	}
	if kn.RelationTypes[0] != relation || relation.SourceObjectTypeID != "ot-1" || relation.TargetObjectTypeID != "ot-2" {
		t.Fatalf("relation topology changed: %#v", kn.RelationTypes)
	}
	if kn.Metrics[0] != metric || metric.ScopeRef != "ot-1" {
		t.Fatalf("metric topology changed: %#v", kn.Metrics)
	}
	if kn.ActionTypes[0].ObjectTypeID != "ot-1" {
		t.Fatalf("action object binding changed: %#v", kn.ActionTypes[0])
	}
}

func Test_BKNRestHandler_DownloadBKN(t *testing.T) {
	Convey("Test BKNHandler DownloadBKN\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		as := bmock.NewMockAuthService(mockCtrl)
		kns := bmock.NewMockKNService(mockCtrl)
		bs := bmock.NewMockBKNService(mockCtrl)

		handler := MockNewBKNRestHandler(appSetting, as, kns, bs)
		handler.RegisterPublic(engine)

		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{}, nil)

		Convey("Success downloading BKN tar\n", func() {
			bs.EXPECT().ExportToTar(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).Return([]byte("tar-content"), nil)

			req := httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/bkns/kn1", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Result().Header.Get("Content-Type"), ShouldEqual, "application/octet-stream")
		})

		Convey("Failed when ExportToTar returns error\n", func() {
			bs.EXPECT().ExportToTar(gomock.Any(), "kn1", gomock.Any()).Return(nil, &rest.HTTPError{
				HTTPCode: http.StatusInternalServerError,
				Language: rest.DefaultLanguage,
				BaseError: rest.BaseError{
					ErrorCode: berrors.BknBackend_KnowledgeNetwork_InternalError,
				},
			})

			req := httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/bkns/kn1", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusInternalServerError)
		})
	})
}

func Test_BKNRestHandler_UploadBKN_AuthFail(t *testing.T) {
	Convey("Test BKNHandler UploadBKN returns 401 when auth fails\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		engine := gin.New()
		engine.Use(gin.Recovery())

		as := bmock.NewMockAuthService(mockCtrl)
		handler := MockNewBKNRestHandler(&common.AppSetting{}, as, nil, nil)
		handler.RegisterPublic(engine)

		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).Return(hydra.Visitor{}, errors.New("invalid token"))

		req := newMultipartRequest(t, "/api/bkn-backend/v1/bkns", "test.tar", newValidBKNTar(t))
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		So(w.Result().StatusCode, ShouldEqual, http.StatusUnauthorized)
	})
}

func Test_BKNRestHandler_UploadBKN_ExtensionCheck(t *testing.T) {
	Convey("Test BKNHandler UploadBKN extension validation (non-octet-stream content type)\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		engine := gin.New()
		engine.Use(gin.Recovery())

		as := bmock.NewMockAuthService(mockCtrl)
		kns := bmock.NewMockKNService(mockCtrl)
		handler := MockNewBKNRestHandler(&common.AppSetting{}, as, kns, nil)
		handler.RegisterPublic(engine)

		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{}, nil)

		url := "/api/bkn-backend/v1/bkns"

		Convey("Failed when invalid extension with non-octet-stream content type\n", func() {
			req := newMultipartRequestWithContentType(t, url, "test.json", "application/json", []byte("content"))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Success when .tgz extension with non-octet-stream content type\n", func() {
			kns.EXPECT().CreateKN(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("kn1", nil)

			req := newMultipartRequestWithContentType(t, url, "test.tgz", "application/gzip", newValidBKNTar(t))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})
	})
}
