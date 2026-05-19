// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func Test_CatalogRestHandler_ListCatalogs(t *testing.T) {
	Convey("Test CatalogHandler ListCatalogs\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		cs := vmock.NewMockCatalogService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, nil, nil, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		url := "/api/vega-backend/in/v1/catalogs"

		Convey("Invalid type\n", func() {
			req := httptest.NewRequest(http.MethodGet, url+"?type=unknown", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "VegaBackend.Catalog.InvalidParameter.Type")
			So(w.Body.String(), ShouldContainSubstring, "invalid type: unknown")
		})

		Convey("Invalid health check status\n", func() {
			req := httptest.NewRequest(http.MethodGet, url+"?health_check_status=unknown", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "VegaBackend.Catalog.InvalidParameter")
			So(w.Body.String(), ShouldContainSubstring, "invalid health_check_status: unknown")
		})

		Convey("Invalid enabled\n", func() {
			req := httptest.NewRequest(http.MethodGet, url+"?enabled=maybe", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "invalid enabled: maybe")
		})

		Convey("Success list catalogs with name type and health check status\n", func() {
			cs.EXPECT().List(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.Catalog, int64, error) {
					So(params.Name, ShouldEqual, "lake")
					So(params.Type, ShouldEqual, interfaces.CatalogTypePhysical)
					So(params.HealthCheckStatus, ShouldEqual, interfaces.CatalogHealthStatusHealthy)
					return []*interfaces.Catalog{}, int64(0), nil
				})

			req := httptest.NewRequest(http.MethodGet, url+"?name=lake&type=physical&health_check_status=healthy", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})

		Convey("Success list catalogs with enabled filter\n", func() {
			cs.EXPECT().List(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.Catalog, int64, error) {
					So(params.Enabled, ShouldNotBeNil)
					So(*params.Enabled, ShouldBeFalse)
					return []*interfaces.Catalog{}, int64(0), nil
				})

			req := httptest.NewRequest(http.MethodGet, url+"?enabled=false", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})

		Convey("Invalid disabled health check status\n", func() {
			req := httptest.NewRequest(http.MethodGet, url+"?health_check_status=disabled", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "invalid health_check_status: disabled")
		})

		Convey("Success list catalogs with unchecked health check status\n", func() {
			cs.EXPECT().List(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.Catalog, int64, error) {
					So(params.HealthCheckStatus, ShouldEqual, interfaces.CatalogHealthStatusUnchecked)
					return []*interfaces.Catalog{}, int64(0), nil
				})

			req := httptest.NewRequest(http.MethodGet, url+"?health_check_status=unchecked", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})
	})
}

func Test_CatalogRestHandler_SetCatalogEnabled(t *testing.T) {
	Convey("Test CatalogHandler SetCatalogEnabled\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		cs := vmock.NewMockCatalogService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, nil, nil, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		Convey("Enable disabled catalog\n", func() {
			cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
				Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Enabled: false}, nil)
			cs.EXPECT().SetEnabled(gomock.Any(), gomock.Any(), true).Return(nil)

			req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/enable", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusNoContent)
		})

		Convey("Disable enabled catalog\n", func() {
			cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
				Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Enabled: true}, nil)
			cs.EXPECT().SetEnabled(gomock.Any(), gomock.Any(), false).Return(nil)

			req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/disable", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusNoContent)
		})

		Convey("Enable already enabled catalog is idempotent\n", func() {
			cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
				Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Enabled: true}, nil)

			req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/enable", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusNoContent)
		})
	})
}

func Test_CatalogRestHandler_UpdateRejectsEnabledChange(t *testing.T) {
	Convey("Test CatalogHandler Update rejects enabled change\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		cs := vmock.NewMockCatalogService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, nil, nil, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{
				ID:            "catalog-1",
				Name:          "catalog",
				Enabled:       false,
				ConnectorType: "mariadb",
				ConnectorCfg:  interfaces.ConnectorConfig{},
			}, nil)

		body := `{"id":"catalog-1","name":"catalog","enabled":true,"connector_type":"mariadb","connector_config":{}}`
		req := httptest.NewRequest(http.MethodPut, "/api/vega-backend/in/v1/catalogs/catalog-1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
		So(w.Body.String(), ShouldContainSubstring, "use POST /catalogs/{id}/enable or /disable to change enabled state")
	})
}

func Test_CatalogRestHandler_DiscoverRejectsDisabledCatalog(t *testing.T) {
	Convey("Test CatalogHandler Discover rejects disabled catalog\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		cs := vmock.NewMockCatalogService(mockCtrl)
		dts := vmock.NewMockDiscoverTaskService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, cs, nil, nil, nil, nil, dts, nil, nil, nil)
		handler.RegisterPublic(engine)

		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog", Enabled: false}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/vega-backend/in/v1/catalogs/catalog-1/discover", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		So(w.Result().StatusCode, ShouldEqual, http.StatusConflict)
		So(w.Body.String(), ShouldContainSubstring, "VegaBackend.Catalog.IsDisabled")
	})
}
