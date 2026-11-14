// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func buildScheduleHTTPFixture(t *testing.T) (*gin.Engine, *mock.MockBuildScheduleService) {
	t.Helper()
	ctrl := gomock.NewController(t)
	service := mock.NewMockBuildScheduleService(ctrl)
	handler := &restHandler{bss: service}
	engine := gin.New()
	engine.POST("/build-schedules", handler.CreateBuildScheduleByIn)
	engine.GET("/build-schedules", handler.ListBuildSchedulesByIn)
	engine.GET("/build-schedules/:id", handler.GetBuildScheduleByIn)
	engine.PUT("/build-schedules/:id", handler.UpdateBuildScheduleByIn)
	engine.DELETE("/build-schedules/:id", handler.DeleteBuildScheduleByIn)
	engine.POST("/build-schedules/:id/enable", handler.EnableBuildScheduleByIn)
	engine.POST("/build-schedules/:id/disable", handler.DisableBuildScheduleByIn)
	return engine, service
}

func TestBuildScheduleRestHandlerCreate(t *testing.T) {
	engine, service := buildScheduleHTTPFixture(t)
	service.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *interfaces.BuildScheduleRequest) (string, error) {
		assert.Equal(t, "resource", req.ResourceID)
		assert.Equal(t, interfaces.BuildTaskExecuteTypeFull, req.ExecuteType)
		assert.False(t, req.Enabled)
		assert.Equal(t, interfaces.AccountInfo{ID: "account", Type: "user"}, ctx.Value(interfaces.ACCOUNT_INFO_KEY))
		return "schedule", nil
	})
	req := httptest.NewRequest(http.MethodPost, "/build-schedules", strings.NewReader(`{"name":"nightly","resource_id":"resource","cron_expr":"0 * * * *","execute_type":"full","last_status":"success","last_message":"forged"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "account")
	req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)
	require.Equal(t, http.StatusCreated, response.Code)
	assert.JSONEq(t, `{"id":"schedule"}`, response.Body.String())
}

func TestBuildScheduleRestHandlerList(t *testing.T) {
	t.Run("passes filters and returns recent result", func(t *testing.T) {
		engine, service := buildScheduleHTTPFixture(t)
		service.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p interfaces.BuildScheduleQueryParams) ([]*interfaces.BuildSchedule, int64, error) {
			assert.Equal(t, "resource", p.ResourceID)
			assert.Equal(t, "catalog", p.CatalogID)
			assert.Equal(t, "full", p.ExecuteType)
			require.NotNil(t, p.Enabled)
			assert.True(t, *p.Enabled)
			return []*interfaces.BuildSchedule{{ID: "schedule", LastStatus: interfaces.ScheduleStatusFailed, LastMessage: "资源配置无效", RunID: "internal-run"}}, 1, nil
		})
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/build-schedules?resource_id=resource&catalog_id=catalog&execute_type=full&enabled=true", nil))
		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"last_status":"failed"`)
		assert.Contains(t, response.Body.String(), `"last_message":"资源配置无效"`)
		assert.Contains(t, response.Body.String(), `"total_count":1`)
		assert.NotContains(t, response.Body.String(), "internal-run")
	})
	for _, query := range []string{"execute_type=streaming", "enabled=invalid", "limit=-2", "sort=untrusted"} {
		t.Run(query, func(t *testing.T) {
			engine, _ := buildScheduleHTTPFixture(t)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/build-schedules?"+query, nil))
			assert.Equal(t, http.StatusBadRequest, response.Code)
		})
	}
}

func TestBuildScheduleRestHandlerGet(t *testing.T) {
	engine, service := buildScheduleHTTPFixture(t)
	service.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/build-schedules/missing", nil))
	require.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), verrors.VegaBackend_BuildSchedule_NotFound)
}

func TestBuildScheduleRestHandlerUpdate(t *testing.T) {
	t.Run("requires expected version before update", func(t *testing.T) {
		engine, service := buildScheduleHTTPFixture(t)
		service.EXPECT().GetByID(gomock.Any(), "schedule").Return(&interfaces.BuildSchedule{ID: "schedule"}, nil)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/build-schedules/schedule", strings.NewReader(`{"name":"nightly","resource_id":"resource","cron_expr":"0 * * * *","execute_type":"full"}`)))
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})
	t.Run("propagates resource binding conflict", func(t *testing.T) {
		engine, service := buildScheduleHTTPFixture(t)
		current := &interfaces.BuildSchedule{ID: "schedule", ResourceID: "resource", UpdateTime: 10}
		service.EXPECT().GetByID(gomock.Any(), "schedule").Return(current, nil)
		service.EXPECT().Update(gomock.Any(), current, gomock.Any()).Return(httperrors.NewHTTPError(context.Background(), http.StatusConflict, verrors.VegaBackend_BuildSchedule_ResourceMismatch))
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/build-schedules/schedule", strings.NewReader(`{"name":"nightly","resource_id":"other","cron_expr":"0 * * * *","execute_type":"full","expected_update_time":10}`)))
		assert.Equal(t, http.StatusConflict, response.Code)
	})
}

func TestBuildScheduleRestHandlerDisable(t *testing.T) {
	engine, service := buildScheduleHTTPFixture(t)
	current := &interfaces.BuildSchedule{ID: "schedule", Name: "nightly", LastStatus: interfaces.ScheduleStatusFailed, LastMessage: "previous failure"}
	service.EXPECT().GetByID(gomock.Any(), "schedule").Return(current, nil)
	service.EXPECT().UpdateEnabled(gomock.Any(), current, false).Return(nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/build-schedules/schedule/disable", nil))
	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, "previous failure", current.LastMessage)
}
