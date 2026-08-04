// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog_health_check_schedule

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/robfig/cron/v3"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics/permission"
)

var (
	chcsServiceOnce sync.Once
	chcsService     interfaces.CatalogHealthCheckScheduleService
)

const defaultCatalogHealthCheckCronExpr = "0 * * * *"

type catalogHealthCheckScheduleService struct {
	appSetting          *common.AppSetting
	defaultCronSchedule cron.Schedule
	ca                  interfaces.CatalogAccess
	sa                  interfaces.CatalogHealthCheckScheduleAccess
	ps                  interfaces.PermissionService
}

func NewCatalogHealthCheckScheduleService(appSetting *common.AppSetting) interfaces.CatalogHealthCheckScheduleService {
	chcsServiceOnce.Do(func() {
		defaultCronExpr := defaultCatalogHealthCheckCronExpr
		if appSetting.CatalogHealthCheck.CronExpr != "" {
			defaultCronExpr = appSetting.CatalogHealthCheck.CronExpr
		}
		defaultCronSchedule, err := ParseCronExpr(defaultCronExpr)
		if err != nil {
			logger.Fatalf("Invalid global catalog health check cron expression: %v", err)
		}
		ps := permission.NewPermissionService(appSetting)
		chcsService = &catalogHealthCheckScheduleService{
			appSetting:          appSetting,
			defaultCronSchedule: defaultCronSchedule,
			ca:                  logics.CA,
			sa:                  logics.CHCSA,
			ps:                  ps,
		}
	})
	return chcsService
}

func (chcss *catalogHealthCheckScheduleService) Create(ctx context.Context, tx *sql.Tx, catalog *interfaces.Catalog,
	req *interfaces.CatalogHealthCheckScheduleRequest) (*interfaces.CatalogHealthCheckSchedule, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogHealthCheckScheduleService.Create")
	defer span.End()

	if catalog != nil {
		span.SetAttributes(attr.Key("catalog_id").String(catalog.ID))
	}

	if catalog == nil || catalog.Type != interfaces.CatalogTypePhysical {
		span.SetStatus(codes.Error, "Catalog is not physical")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter).WithErrorDetails("health check schedules are only supported for physical catalogs")
	}

	if req == nil {
		req = &interfaces.CatalogHealthCheckScheduleRequest{
			Mode: interfaces.CatalogHealthCheckScheduleModeInherit,
		}
	}

	if err := validateRequest(req); err != nil {
		span.SetStatus(codes.Error, "Invalid health check schedule request")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter).WithErrorDetails(err.Error())
	}

	nowTime := time.Now()
	now := nowTime.UnixMilli()
	account := interfaces.AccountInfo{}
	if value := ctx.Value(interfaces.ACCOUNT_INFO_KEY); value != nil {
		account = value.(interfaces.AccountInfo)
	}

	schedule := &interfaces.CatalogHealthCheckSchedule{
		CatalogID:  catalog.ID,
		Mode:       req.Mode,
		Creator:    account,
		CreateTime: now,
		Updater:    account,
		UpdateTime: now,
	}

	if req.Mode == interfaces.CatalogHealthCheckScheduleModeEnabled {
		schedule.CronExpr = req.CronExpr
	}
	if req.Mode != interfaces.CatalogHealthCheckScheduleModeDisabled {
		nextRun, err := chcss.nextRun(req.Mode, req.CronExpr, nowTime)
		if err != nil {
			span.SetStatus(codes.Error, "Invalid cron expression")
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InvalidParameter).WithErrorDetails(fmt.Sprintf("invalid cron_expr: %v", err))
		}
		schedule.NextRun = nextRun
	}

	if err := chcss.sa.Create(ctx, tx, schedule); err != nil {
		span.SetStatus(codes.Error, "Create health check schedule failed")
		otellog.LogError(ctx, "Create catalog health check schedule failed", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return schedule, nil
}

func (chcss *catalogHealthCheckScheduleService) GetByCatalogID(ctx context.Context,
	catalogID string) (*interfaces.CatalogHealthCheckSchedule, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogHealthCheckScheduleService.GetByCatalogID")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	schedule, err := chcss.sa.GetByCatalogID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get health check schedule failed")
		otellog.LogError(ctx, "Get catalog health check schedule failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).
			WithErrorDetails("failed to get catalog health check schedule")
	}

	span.SetStatus(codes.Ok, "")
	return schedule, nil
}

func (chcss *catalogHealthCheckScheduleService) Update(ctx context.Context, catalogID string,
	req *interfaces.CatalogHealthCheckScheduleRequest) (*interfaces.CatalogHealthCheckSchedule, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogHealthCheckScheduleService.Update")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(catalogID))

	if err := validateRequest(req); err != nil {
		span.SetStatus(codes.Error, "Invalid health check schedule request")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter).WithErrorDetails(err.Error())
	}

	catalog, err := chcss.ca.GetByID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		otellog.LogError(ctx, "Get catalog for health check schedule failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).
			WithErrorDetails("failed to get catalog for health check schedule")
	}
	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}
	if catalog.Type != interfaces.CatalogTypePhysical {
		span.SetStatus(codes.Error, "Catalog is not physical")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter).WithErrorDetails("health check schedules are only supported for physical catalogs")
	}

	resourceType := interfaces.AUTH_RESOURCE_TYPE_CATALOG
	if catalog.Internal {
		resourceType = interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG
	}
	if err := chcss.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: resourceType,
		ID:   catalogID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
		span.SetStatus(codes.Error, "Check catalog modify permission failed")
		otellog.LogError(ctx, "Check catalog health check schedule permission failed", err)
		return nil, err
	}

	nowTime := time.Now()
	now := nowTime.UnixMilli()
	account := interfaces.AccountInfo{}
	if value := ctx.Value(interfaces.ACCOUNT_INFO_KEY); value != nil {
		account = value.(interfaces.AccountInfo)
	}

	schedule, err := chcss.sa.GetByCatalogID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get health check schedule failed")
		otellog.LogError(ctx, "Get catalog health check schedule failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).
			WithErrorDetails("failed to get catalog health check schedule")
	}

	schedule.Mode = req.Mode
	schedule.Updater = account
	schedule.UpdateTime = now
	switch req.Mode {
	case interfaces.CatalogHealthCheckScheduleModeInherit:
		schedule.CronExpr = ""
	case interfaces.CatalogHealthCheckScheduleModeEnabled:
		schedule.CronExpr = req.CronExpr
	case interfaces.CatalogHealthCheckScheduleModeDisabled:
		schedule.NextRun = 0
	}
	if req.Mode != interfaces.CatalogHealthCheckScheduleModeDisabled {
		nextRun, parseErr := chcss.nextRun(req.Mode, req.CronExpr, nowTime)
		if parseErr != nil {
			span.SetStatus(codes.Error, "Invalid cron expression")
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InvalidParameter).WithErrorDetails(fmt.Sprintf("invalid cron_expr: %v", parseErr))
		}
		schedule.NextRun = nextRun
	}

	if err := chcss.sa.Update(ctx, schedule); err != nil {
		span.SetStatus(codes.Error, "Update health check schedule failed")
		otellog.LogError(ctx, "Update catalog health check schedule failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).
			WithErrorDetails("failed to update catalog health check schedule")
	}

	span.SetStatus(codes.Ok, "")
	return schedule, nil
}

func (chcss *catalogHealthCheckScheduleService) nextRun(mode, cronExpr string, now time.Time) (int64, error) {
	if mode == interfaces.CatalogHealthCheckScheduleModeInherit {
		return chcss.defaultCronSchedule.Next(now).UnixMilli(), nil
	}

	schedule, err := ParseCronExpr(cronExpr)
	if err != nil {
		return 0, err
	}
	return schedule.Next(now).UnixMilli(), nil
}

func (chcss *catalogHealthCheckScheduleService) DeleteByCatalogIDs(ctx context.Context, tx *sql.Tx, catalogIDs []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogHealthCheckScheduleService.DeleteByCatalogIDs")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_ids").StringSlice(catalogIDs))

	if err := chcss.sa.DeleteByCatalogIDs(ctx, tx, catalogIDs); err != nil {
		span.SetStatus(codes.Error, "Delete health check schedules failed")
		otellog.LogError(ctx, "Delete catalog health check schedules failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
