// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog_health_check_schedule

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func newCatalogHealthCheckScheduleServiceForTest(t *testing.T) *catalogHealthCheckScheduleService {
	t.Helper()
	defaultCronSchedule, err := cron.ParseStandard(defaultCatalogHealthCheckCronExpr)
	require.NoError(t, err)
	return &catalogHealthCheckScheduleService{defaultCronSchedule: defaultCronSchedule}
}

func TestCatalogHealthCheckScheduleServiceGetByCatalogID(t *testing.T) {
	t.Run("delegates to schedule access", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		service := &catalogHealthCheckScheduleService{sa: sa}
		expected := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1"}

		sa.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(expected, nil)

		got, err := service.GetByCatalogID(context.Background(), "catalog-1")

		require.NoError(t, err)
		assert.Same(t, expected, got)
	})

	t.Run("returns internal error when schedule does not exist", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		service := &catalogHealthCheckScheduleService{sa: sa}
		sa.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(nil, sql.ErrNoRows)

		got, err := service.GetByCatalogID(context.Background(), "catalog-1")

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_GetFailed, httpErr.BaseError.ErrorCode)
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), sql.ErrNoRows.Error())
		assert.Nil(t, got)
	})

	t.Run("redacts database errors", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		service := &catalogHealthCheckScheduleService{sa: sa}
		sensitiveError := "dial tcp db.internal:3306: connection refused"
		sa.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(nil, errors.New(sensitiveError))

		got, err := service.GetByCatalogID(context.Background(), "catalog-1")

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_GetFailed, httpErr.BaseError.ErrorCode)
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), sensitiveError)
		assert.Nil(t, got)
	})
}

func TestCatalogHealthCheckScheduleServiceCreate(t *testing.T) {
	t.Run("creates default inherit schedule without modify permission check", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		service := newCatalogHealthCheckScheduleServiceForTest(t)
		service.sa = sa
		account := interfaces.AccountInfo{ID: "user-1", Type: interfaces.ACCESSOR_TYPE_USER}
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, account)
		beforeCreate := time.Now().UnixMilli()

		sa.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&interfaces.CatalogHealthCheckSchedule{})).DoAndReturn(func(_ context.Context, _ *sql.Tx, schedule *interfaces.CatalogHealthCheckSchedule) error {
			assert.Equal(t, "catalog-1", schedule.CatalogID)
			assert.Equal(t, interfaces.CatalogHealthCheckScheduleModeInherit, schedule.Mode)
			assert.Empty(t, schedule.CronExpr)
			assert.Greater(t, schedule.NextRun, beforeCreate)
			assert.Equal(t, account, schedule.Creator)
			assert.Equal(t, account, schedule.Updater)
			return nil
		})

		got, err := service.Create(ctx, nil, &interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, nil)

		require.NoError(t, err)
		assert.Equal(t, interfaces.CatalogHealthCheckScheduleModeInherit, got.Mode)
	})

	t.Run("rejects invalid custom schedule before persistence", func(t *testing.T) {
		service := &catalogHealthCheckScheduleService{}

		got, err := service.Create(context.Background(), nil, &interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, &interfaces.CatalogHealthCheckScheduleRequest{
			Mode: interfaces.CatalogHealthCheckScheduleModeEnabled,
		})

		require.ErrorContains(t, err, "cron_expr is required")
		assert.Nil(t, got)
	})

	t.Run("rejects custom schedule more frequent than hourly before persistence", func(t *testing.T) {
		service := &catalogHealthCheckScheduleService{}

		got, err := service.Create(context.Background(), nil,
			&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical},
			&interfaces.CatalogHealthCheckScheduleRequest{
				Mode:     interfaces.CatalogHealthCheckScheduleModeEnabled,
				CronExpr: "*/30 * * * *",
			},
		)

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Nil(t, got)
	})

	t.Run("returns schedule access create error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		service := newCatalogHealthCheckScheduleServiceForTest(t)
		service.sa = sa
		createErr := errors.New("schedule insert failed")
		sa.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(createErr)

		got, err := service.Create(context.Background(), nil, &interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, nil)

		require.ErrorIs(t, err, createErr)
		assert.Nil(t, got)
	})
}

func TestCatalogHealthCheckScheduleServiceUpdate(t *testing.T) {
	t.Run("rejects invalid request before access", func(t *testing.T) {
		service := &catalogHealthCheckScheduleService{}

		got, err := service.Update(context.Background(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{
			Mode: interfaces.CatalogHealthCheckScheduleModeEnabled,
		})

		require.ErrorContains(t, err, "cron_expr is required")
		assert.Nil(t, got)
	})

	t.Run("rejects non physical catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		service := &catalogHealthCheckScheduleService{ca: ca}
		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{Type: interfaces.CatalogTypeLogical}, nil)

		got, err := service.Update(context.Background(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{
			Mode: interfaces.CatalogHealthCheckScheduleModeInherit,
		})

		require.ErrorContains(t, err, "only supported for physical catalogs")
		assert.Nil(t, got)
	})

	t.Run("returns not found when catalog does not exist", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		service := &catalogHealthCheckScheduleService{ca: ca}
		ca.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		got, err := service.Update(context.Background(), "missing", &interfaces.CatalogHealthCheckScheduleRequest{
			Mode: interfaces.CatalogHealthCheckScheduleModeInherit,
		})

		httpErr, ok := err.(*rest.HTTPError)
		require.True(t, ok)
		assert.Equal(t, http.StatusNotFound, httpErr.HTTPCode)
		assert.Nil(t, got)
	})

	t.Run("redacts catalog access error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		service := &catalogHealthCheckScheduleService{ca: ca}
		catalogErr := errors.New("catalog database unavailable")
		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(nil, catalogErr)

		got, err := service.Update(context.Background(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{Mode: interfaces.CatalogHealthCheckScheduleModeInherit})

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_GetFailed, httpErr.BaseError.ErrorCode)
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), catalogErr.Error())
		assert.Nil(t, got)
	})

	t.Run("requires catalog modify permission", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		service := &catalogHealthCheckScheduleService{ca: ca, ps: ps}
		permissionErr := errors.New("permission denied")

		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "catalog-1"}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(permissionErr)

		got, err := service.Update(context.Background(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{
			Mode: interfaces.CatalogHealthCheckScheduleModeInherit,
		})

		require.ErrorIs(t, err, permissionErr)
		assert.Nil(t, got)
	})

	t.Run("uses internal catalog modify permission", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		service := newCatalogHealthCheckScheduleServiceForTest(t)
		service.ca = ca
		service.sa = sa
		service.ps = ps
		current := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1"}

		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical, Internal: true}, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{Type: interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG, ID: "catalog-1"}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		sa.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(current, nil)
		sa.EXPECT().Update(gomock.Any(), current).Return(nil)

		got, err := service.Update(context.Background(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{Mode: interfaces.CatalogHealthCheckScheduleModeInherit})

		require.NoError(t, err)
		assert.Same(t, current, got)
	})

	t.Run("returns missing schedule error without creating a row", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		service := newCatalogHealthCheckScheduleServiceForTest(t)
		service.ca = ca
		service.sa = sa
		service.ps = ps

		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "catalog-1"}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		sa.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(nil, sql.ErrNoRows)

		got, err := service.Update(context.Background(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{
			Mode:     interfaces.CatalogHealthCheckScheduleModeEnabled,
			CronExpr: "0 * * * *",
		})

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_GetFailed, httpErr.BaseError.ErrorCode)
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), sql.ErrNoRows.Error())
		assert.Nil(t, got)
	})

	t.Run("disables existing schedule without clearing its cron or last run", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		service := &catalogHealthCheckScheduleService{ca: ca, sa: sa, ps: ps}
		current := &interfaces.CatalogHealthCheckSchedule{
			CatalogID: "catalog-1",
			Mode:      interfaces.CatalogHealthCheckScheduleModeEnabled,
			CronExpr:  "0 * * * *",
			LastRun:   123,
			NextRun:   456,
		}

		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "catalog-1"}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		sa.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(current, nil)
		sa.EXPECT().Update(gomock.Any(), current).DoAndReturn(func(_ context.Context, schedule *interfaces.CatalogHealthCheckSchedule) error {
			assert.Equal(t, interfaces.CatalogHealthCheckScheduleModeDisabled, schedule.Mode)
			assert.Equal(t, "0 * * * *", schedule.CronExpr)
			assert.Equal(t, int64(123), schedule.LastRun)
			assert.Zero(t, schedule.NextRun)
			return nil
		})

		got, err := service.Update(context.Background(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{
			Mode: interfaces.CatalogHealthCheckScheduleModeDisabled,
		})

		require.NoError(t, err)
		assert.Same(t, current, got)
	})

	t.Run("clears cron and schedules the next global run when changing to inherit", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		service := newCatalogHealthCheckScheduleServiceForTest(t)
		service.ca = ca
		service.sa = sa
		service.ps = ps
		current := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1", CronExpr: "0 * * * *", NextRun: 456}
		beforeUpdate := time.Now().UnixMilli()

		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "catalog-1"}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		sa.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(current, nil)
		sa.EXPECT().Update(gomock.Any(), current).Return(nil)

		got, err := service.Update(context.Background(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{
			Mode: interfaces.CatalogHealthCheckScheduleModeInherit,
		})

		require.NoError(t, err)
		assert.Empty(t, got.CronExpr)
		assert.Greater(t, got.NextRun, beforeUpdate)
	})

	t.Run("redacts schedule access error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		service := newCatalogHealthCheckScheduleServiceForTest(t)
		service.ca = ca
		service.sa = sa
		service.ps = ps
		accessErr := errors.New("database unavailable")

		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "catalog-1"}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		sa.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(nil, accessErr)

		got, err := service.Update(context.Background(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{
			Mode: interfaces.CatalogHealthCheckScheduleModeInherit,
		})

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_GetFailed, httpErr.BaseError.ErrorCode)
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), accessErr.Error())
		assert.Nil(t, got)
	})

	t.Run("redacts schedule update error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		sa := vmock.NewMockCatalogHealthCheckScheduleAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		service := newCatalogHealthCheckScheduleServiceForTest(t)
		service.ca = ca
		service.sa = sa
		service.ps = ps
		current := &interfaces.CatalogHealthCheckSchedule{CatalogID: "catalog-1"}
		updateErr := errors.New("schedule update failed")

		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "catalog-1"}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		sa.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(current, nil)
		sa.EXPECT().Update(gomock.Any(), current).Return(updateErr)

		got, err := service.Update(context.Background(), "catalog-1", &interfaces.CatalogHealthCheckScheduleRequest{Mode: interfaces.CatalogHealthCheckScheduleModeInherit})

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_UpdateFailed, httpErr.BaseError.ErrorCode)
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), updateErr.Error())
		assert.Nil(t, got)
	})
}
