// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func Test_BuildTaskRestHandler_ListBuildTasks(t *testing.T) {
	Convey("Test BuildTaskHandler ListBuildTasks\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		bts := vmock.NewMockBuildTaskService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, nil, bts, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		url := "/api/vega-backend/in/v1/build-tasks"

		Convey("Invalid offset\n", func() {
			req := httptest.NewRequest(http.MethodGet, url+"?offset=-1", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "VegaBackend.InvalidParameter.Offset")
		})

		Convey("Invalid limit\n", func() {
			req := httptest.NewRequest(http.MethodGet, url+"?limit=99999999", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "VegaBackend.InvalidParameter.Limit")
		})

		Convey("Invalid order_by\n", func() {
			req := httptest.NewRequest(http.MethodGet, url+"?order_by=unknown_field", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "VegaBackend.InvalidParameter.Sort")
		})

		Convey("Invalid order\n", func() {
			req := httptest.NewRequest(http.MethodGet, url+"?order=foo", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "VegaBackend.InvalidParameter.Direction")
		})

		Convey("Invalid status\n", func() {
			req := httptest.NewRequest(http.MethodGet, url+"?status=foo", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "VegaBackend.BuildTask.InvalidStatus")
		})

		Convey("Invalid mode\n", func() {
			req := httptest.NewRequest(http.MethodGet, url+"?mode=foo", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "VegaBackend.BuildTask.InvalidParameter.Mode")
		})

		Convey("Success with default pagination\n", func() {
			bts.EXPECT().ListBuildTasks(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
					So(params.Offset, ShouldEqual, 0)
					So(params.Limit, ShouldEqual, 20)
					So(params.OrderBy, ShouldEqual, interfaces.BuildTaskOrderByDefault)
					So(params.Order, ShouldEqual, interfaces.DESC_DIRECTION)
					return []*interfaces.BuildTask{}, int64(0), nil
				})

			req := httptest.NewRequest(http.MethodGet, url, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})

		Convey("Success with explicit query params\n", func() {
			bts.EXPECT().ListBuildTasks(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
					So(params.ResourceID, ShouldEqual, "res-1")
					So(params.CatalogID, ShouldEqual, "cat-1")
					So(params.Statuses, ShouldResemble, []string{interfaces.BuildTaskStatusCompleted})
					So(params.Mode, ShouldEqual, interfaces.BuildTaskModeBatch)
					So(params.Offset, ShouldEqual, 5)
					So(params.Limit, ShouldEqual, 10)
					So(params.OrderBy, ShouldEqual, interfaces.BuildTaskOrderByCreatedAt)
					So(params.Order, ShouldEqual, interfaces.ASC_DIRECTION)
					return []*interfaces.BuildTask{}, int64(0), nil
				})

			req := httptest.NewRequest(http.MethodGet, url+"?resource_id=res-1&catalog_id=cat-1&status=completed&mode=batch&offset=5&limit=10&order_by=created_at&order=asc", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		})
	})
}

func Test_BuildTaskRestHandler_DeleteBuildTasks(t *testing.T) {
	Convey("Test BuildTaskHandler DeleteBuildTasks\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		bts := vmock.NewMockBuildTaskService(mockCtrl)
		handler := MockNewRestHandler(&common.AppSetting{}, nil, nil, nil, bts, nil, nil, nil, nil, nil, nil)
		handler.RegisterPublic(engine)

		url := "/api/vega-backend/in/v1/build-tasks/t1,t2?ignore_missing=true&delete_active_index=true"

		bts.EXPECT().DeleteBuildTasks(gomock.Any(), []string{"t1", "t2"}, true, true).Return(nil)

		req := httptest.NewRequest(http.MethodDelete, url, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		So(w.Result().StatusCode, ShouldEqual, http.StatusNoContent)
	})
}
