// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package build_schedule

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type serviceFixture struct {
	s   *buildScheduleService
	sa  *mock.MockBuildScheduleAccess
	cs  *mock.MockCatalogService
	rs  *mock.MockResourceService
	bts *mock.MockBuildTaskService
	ctx context.Context
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	clock := gomonkey.ApplyFuncReturn(time.Now, time.Date(2026, 11, 14, 12, 15, 0, 0, time.UTC))
	t.Cleanup(clock.Reset)
	ctrl := gomock.NewController(t)
	f := &serviceFixture{sa: mock.NewMockBuildScheduleAccess(ctrl), cs: mock.NewMockCatalogService(ctrl), rs: mock.NewMockResourceService(ctrl), bts: mock.NewMockBuildTaskService(ctrl)}
	f.s = &buildScheduleService{sa: f.sa, cs: f.cs, rs: f.rs, bts: f.bts}
	f.ctx = context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "creator", Type: "user"})
	return f
}

func validRequest() *interfaces.BuildScheduleRequest {
	return &interfaces.BuildScheduleRequest{Name: "nightly", ResourceID: "resource", CronExpr: "0 * * * *", ExecuteType: interfaces.BuildTaskExecuteTypeIncremental}
}

func TestBuildScheduleServiceCreate(t *testing.T) {
	t.Run("allows incremental schedule before baseline and defaults disabled", func(t *testing.T) {
		f := newServiceFixture(t)
		f.rs.EXPECT().GetByID(f.ctx, "resource").Return(&interfaces.Resource{ID: "resource", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable}, nil)
		f.cs.EXPECT().CheckCatalogPermission(f.ctx, "catalog", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, &interfaces.Catalog{ID: "catalog"}, nil)
		f.sa.EXPECT().Create(f.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, s *interfaces.BuildSchedule) error {
			assert.Equal(t, interfaces.ScheduleStatusNever, s.LastStatus)
			assert.Equal(t, interfaces.BuildTaskExecuteTypeIncremental, s.ExecuteType)
			assert.False(t, s.Enabled)
			assert.Equal(t, "creator", s.Creator.ID)
			assert.Equal(t, time.Now().Add(45*time.Minute).UnixMilli(), s.NextRun)
			assert.NotEmpty(t, s.ID)
			return nil
		})
		id, err := f.s.Create(f.ctx, validRequest())
		require.NoError(t, err)
		assert.NotEmpty(t, id)
	})
	for _, tc := range []struct {
		name string
		edit func(*interfaces.BuildScheduleRequest)
		code string
	}{
		{"invalid cron", func(r *interfaces.BuildScheduleRequest) { r.CronExpr = "* * * * *" }, verrors.VegaBackend_BuildSchedule_InvalidCronExpr},
		{"invalid timezone", func(r *interfaces.BuildScheduleRequest) { r.CronExpr = "CRON_TZ=Invalid/Zone 0 * * * *" }, verrors.VegaBackend_BuildSchedule_InvalidCronExpr},
		{"invalid execute type", func(r *interfaces.BuildScheduleRequest) { r.ExecuteType = "streaming" }, verrors.VegaBackend_BuildSchedule_InvalidExecuteType},
		{"invalid window", func(r *interfaces.BuildScheduleRequest) { r.StartTime = 20; r.EndTime = 10 }, verrors.VegaBackend_BuildSchedule_InvalidTimeRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t)
			req := validRequest()
			tc.edit(req)
			id, err := f.s.Create(f.ctx, req)
			assert.Empty(t, id)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tc.code, httpErr.ErrorCode)
		})
	}
}

func TestBuildScheduleServiceExecuteSchedule(t *testing.T) {
	t.Run("background calls use the service default language", func(t *testing.T) {
		original := i18n.DefaultLanguage
		i18n.SetDefaultLocale(i18n.AmericanEnglish)
		t.Cleanup(func() { i18n.SetDefaultLocale(original) })
		service := &buildScheduleService{}
		result, err := service.ExecuteSchedule(context.Background(), &interfaces.BuildSchedule{})
		require.NoError(t, err)
		assert.Equal(t, interfaces.ScheduleStatusFailed, result.Status)
		assert.Equal(t, "The schedule execution identity is invalid; recreate the schedule.", result.Message)
	})
	for _, tc := range []struct {
		language i18n.Language
		message  string
	}{
		{language: i18n.SimplifiedChinese, message: "调度执行身份无效，请重新创建调度"},
		{language: i18n.AmericanEnglish, message: "The schedule execution identity is invalid; recreate the schedule."},
	} {
		t.Run("invalid creator message in "+string(tc.language), func(t *testing.T) {
			service := &buildScheduleService{}
			ctx := i18n.WithLanguage(context.Background(), tc.language)
			result, err := service.ExecuteSchedule(ctx, &interfaces.BuildSchedule{})
			require.NoError(t, err)
			assert.Equal(t, interfaces.ScheduleStatusFailed, result.Status)
			assert.Equal(t, tc.message, result.Message)
		})
	}
	for _, tc := range []struct {
		name                    string
		enabled                 bool
		taskErr                 error
		wantStatus, wantMessage string
	}{
		{name: "creates task with creator identity and provenance", enabled: true, wantStatus: interfaces.ScheduleStatusSuccess},
		{name: "disabled resource is skipped", wantStatus: interfaces.ScheduleStatusSkipped, wantMessage: "资源或目录已停用"},
		{name: "active task is skipped", enabled: true, taskErr: httperrors.NewHTTPError(context.Background(), http.StatusBadRequest, verrors.VegaBackend_BuildTask_Exist), wantStatus: interfaces.ScheduleStatusSkipped, wantMessage: "资源已有待执行、运行中或停止中的构建任务"},
		{name: "invalid baseline fails without full fallback", enabled: true, taskErr: httperrors.NewHTTPError(context.Background(), http.StatusConflict, verrors.VegaBackend_BuildTask_IncrementalBaselineUnavailable), wantStatus: interfaces.ScheduleStatusFailed, wantMessage: "增量构建缺少可用索引或有效基线"},
		{name: "sensitive raw failure is hidden", enabled: true, taskErr: errors.New("database password=secret"), wantStatus: interfaces.ScheduleStatusFailed, wantMessage: "无法创建构建任务，请检查资源配置或联系管理员"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t)
			schedule := &interfaces.BuildSchedule{ID: "schedule", ResourceID: "resource", CatalogID: "catalog", ExecuteType: interfaces.BuildTaskExecuteTypeIncremental, Creator: interfaces.AccountInfo{ID: "original", Type: "user"}}
			f.cs.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).DoAndReturn(func(ctx context.Context, _ string, _ []string, _ bool) (bool, *interfaces.Catalog, error) {
				assert.Equal(t, schedule.Creator, ctx.Value(interfaces.ACCOUNT_INFO_KEY))
				return true, &interfaces.Catalog{ID: "catalog", Enabled: true}, nil
			})
			f.rs.EXPECT().InternalGetByID(gomock.Any(), nil, "resource").Return(&interfaces.Resource{ID: "resource", CatalogID: "catalog", Enabled: tc.enabled}, nil)
			if tc.enabled {
				f.bts.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *interfaces.CreateBuildTaskRequest) (string, error) {
					assert.Equal(t, schedule.Creator, ctx.Value(interfaces.ACCOUNT_INFO_KEY))
					assert.Equal(t, interfaces.BuildTaskModeBatch, req.Mode)
					assert.Equal(t, schedule.ExecuteType, req.ExecuteType)
					assert.Equal(t, "schedule", req.ScheduleID)
					assert.Equal(t, interfaces.BuildTaskTriggerScheduled, req.TriggerType)
					return "task", tc.taskErr
				})
			}
			result, err := f.s.ExecuteSchedule(f.ctx, schedule)
			assert.Equal(t, tc.taskErr, err)
			assert.Equal(t, tc.wantStatus, result.Status)
			assert.Equal(t, tc.wantMessage, result.Message)
		})
	}
	t.Run("revoked permission fails before resource read", func(t *testing.T) {
		f := newServiceFixture(t)
		f.cs.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog", gomock.Any(), true).Return(false, nil, nil)
		result, err := f.s.ExecuteSchedule(f.ctx, &interfaces.BuildSchedule{CatalogID: "catalog", Creator: interfaces.AccountInfo{ID: "creator", Type: "user"}})
		require.Error(t, err)
		assert.Equal(t, interfaces.ScheduleStatusFailed, result.Status)
		assert.Contains(t, result.Message, "权限")
	})
}

func TestBuildScheduleServiceList(t *testing.T) {
	t.Run("authorization is pushed into data query", func(t *testing.T) {
		f := newServiceFixture(t)
		f.cs.EXPECT().ListPermittedCatalogIDs(f.ctx, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, interfaces.VISIBILITY_MATCH_ALL, interfaces.CatalogsQueryParams{}).Return([]string{"visible"}, nil)
		f.sa.EXPECT().List(f.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p interfaces.BuildScheduleQueryParams) ([]*interfaces.BuildSchedule, int64, error) {
			assert.Equal(t, []string{"visible"}, p.CatalogIDs)
			assert.Equal(t, "resource", p.ResourceID)
			return []*interfaces.BuildSchedule{}, 0, nil
		})
		rows, total, err := f.s.List(f.ctx, interfaces.BuildScheduleQueryParams{ResourceID: "resource"})
		require.NoError(t, err)
		assert.Empty(t, rows)
		assert.Zero(t, total)
	})
	t.Run("no visible catalogs avoids querying schedules", func(t *testing.T) {
		f := newServiceFixture(t)
		f.cs.EXPECT().ListPermittedCatalogIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
		rows, total, err := f.s.List(f.ctx, interfaces.BuildScheduleQueryParams{})
		require.NoError(t, err)
		assert.NotNil(t, rows)
		assert.Zero(t, total)
	})
	t.Run("authorization outage is propagated", func(t *testing.T) {
		f := newServiceFixture(t)
		outage := errors.New("permission unavailable")
		f.cs.EXPECT().CheckCatalogPermission(f.ctx, "catalog", gomock.Any(), true).Return(false, nil, outage)
		_, _, err := f.s.List(f.ctx, interfaces.BuildScheduleQueryParams{CatalogID: "catalog"})
		assert.ErrorIs(t, err, outage)
	})
	for _, tc := range []struct {
		name      string
		err       error
		wantEmpty bool
	}{
		{name: "forbidden returns empty list", err: httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden), wantEmpty: true},
		{name: "permission backend failure is propagated", err: errors.New("permission backend unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t)
			f.cs.EXPECT().ListPermittedCatalogIDs(f.ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, tc.err)
			rows, total, err := f.s.List(f.ctx, interfaces.BuildScheduleQueryParams{})
			if tc.wantEmpty {
				require.NoError(t, err)
				assert.NotNil(t, rows)
				assert.Empty(t, rows)
				assert.Zero(t, total)
			} else {
				assert.ErrorIs(t, err, tc.err)
			}
		})
	}

}

func TestBuildScheduleServiceUpdate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset time.Duration
	}{
		{name: "same millisecond"},
		{name: "clock behind stored version", offset: time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t)
			version := time.Now().Add(tc.offset).UnixMilli()
			current := &interfaces.BuildSchedule{ID: "schedule", ResourceID: "resource", CatalogID: "catalog", Enabled: true, UpdateTime: version, Creator: interfaces.AccountInfo{ID: "original", Type: "user"}, LastStatus: interfaces.ScheduleStatusFailed, LastMessage: "previous error", RunID: "previous-trigger"}
			req := validRequest()
			req.Enabled = true
			req.ExpectedUpdateTime = version
			f.cs.EXPECT().CheckCatalogPermission(f.ctx, "catalog", gomock.Any(), true).Return(true, &interfaces.Catalog{ID: "catalog"}, nil)
			f.sa.EXPECT().Update(f.ctx, gomock.Any(), version).DoAndReturn(func(_ context.Context, updated *interfaces.BuildSchedule, expected int64) (int64, error) {
				assert.Equal(t, version, expected)
				assert.Equal(t, version+1, updated.UpdateTime)
				assert.Equal(t, current.Creator, updated.Creator)
				assert.Equal(t, current.LastStatus, updated.LastStatus)
				assert.Equal(t, current.LastMessage, updated.LastMessage)
				assert.Equal(t, current.RunID, updated.RunID)
				assert.Equal(t, "creator", updated.Updater.ID)
				return 1, nil
			})
			require.NoError(t, f.s.Update(f.ctx, current, req))
		})
	}
}

func TestBuildScheduleServiceUpdateEnabled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset time.Duration
	}{
		{name: "same millisecond"},
		{name: "clock behind stored version", offset: time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				name := "disable"
				if enabled {
					name = "enable"
				}
				t.Run(name, func(t *testing.T) {
					f := newServiceFixture(t)
					version := time.Now().Add(tc.offset).UnixMilli()
					current := &interfaces.BuildSchedule{ID: "schedule", CatalogID: "catalog", CronExpr: "0 * * * *", Enabled: !enabled, UpdateTime: version, LastStatus: interfaces.ScheduleStatusFailed, LastMessage: "previous", RunID: "previous-trigger"}
					f.cs.EXPECT().CheckCatalogPermission(f.ctx, "catalog", gomock.Any(), true).Return(true, &interfaces.Catalog{ID: "catalog"}, nil)
					f.sa.EXPECT().UpdateEnabled(f.ctx, "schedule", enabled, gomock.Any(), version, version+1, interfaces.AccountInfo{ID: "creator", Type: "user"}).Return(int64(1), nil)
					require.NoError(t, f.s.UpdateEnabled(f.ctx, current, enabled))
					assert.Equal(t, interfaces.ScheduleStatusFailed, current.LastStatus)
					assert.Equal(t, "previous", current.LastMessage)
					assert.Equal(t, "previous-trigger", current.RunID)
				})
			}
		})
	}
}

func TestBuildScheduleServiceGetByID(t *testing.T) {
	t.Run("permission is checked before reference names are read", func(t *testing.T) {
		f := newServiceFixture(t)
		f.sa.EXPECT().GetByID(f.ctx, "schedule").Return(&interfaces.BuildSchedule{ID: "schedule", CatalogID: "catalog"}, nil)
		f.cs.EXPECT().CheckCatalogPermission(f.ctx, "catalog", gomock.Any(), true).Return(false, nil, nil)
		result, err := f.s.GetByID(f.ctx, "schedule")
		assert.Nil(t, result)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
	})
	t.Run("returns recent outcome with batched reference names", func(t *testing.T) {
		f := newServiceFixture(t)
		f.sa.EXPECT().GetByID(f.ctx, "schedule").Return(&interfaces.BuildSchedule{ID: "schedule", ResourceID: "resource", CatalogID: "catalog", LastStatus: interfaces.ScheduleStatusFailed, LastMessage: "safe error"}, nil)
		f.cs.EXPECT().CheckCatalogPermission(f.ctx, "catalog", gomock.Any(), true).Return(true, &interfaces.Catalog{ID: "catalog"}, nil)
		f.rs.EXPECT().InternalGetByIDs(f.ctx, []string{"resource"}).Return(map[string]*interfaces.Resource{"resource": {ID: "resource", Name: "table"}}, nil)
		f.cs.EXPECT().InternalGetByIDs(f.ctx, []string{"catalog"}).Return(map[string]*interfaces.Catalog{"catalog": {ID: "catalog", Name: "source"}}, nil)
		result, err := f.s.GetByID(f.ctx, "schedule")
		require.NoError(t, err)
		assert.Equal(t, "table", result.ResourceName)
		assert.Equal(t, "source", result.CatalogName)
		assert.Equal(t, "safe error", result.LastMessage)
	})
}

func TestBuildScheduleServiceDelete(t *testing.T) {
	f := newServiceFixture(t)
	current := &interfaces.BuildSchedule{ID: "schedule", CatalogID: "catalog"}
	f.cs.EXPECT().CheckCatalogPermission(f.ctx, "catalog", gomock.Any(), true).Return(true, &interfaces.Catalog{ID: "catalog"}, nil)
	f.sa.EXPECT().Delete(f.ctx, "schedule").Return(nil)
	// 只删除计划；BuildTaskService 没有任何调用。
	require.NoError(t, f.s.Delete(f.ctx, current))
}
