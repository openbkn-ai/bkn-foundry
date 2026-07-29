// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog_health_check_schedule

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/robfig/cron/v3"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/permission"
)

var (
	chcsServiceOnce sync.Once
	chcsService     interfaces.CatalogHealthCheckScheduleService
)

type catalogHealthCheckScheduleService struct {
	appSetting *common.AppSetting
	ca         interfaces.CatalogAccess
	sa         interfaces.CatalogHealthCheckScheduleAccess
	ps         interfaces.PermissionService
}

func NewCatalogHealthCheckScheduleService(appSetting *common.AppSetting) interfaces.CatalogHealthCheckScheduleService {
	chcsServiceOnce.Do(func() {
		ps := permission.NewPermissionService(appSetting)
		chcsService = &catalogHealthCheckScheduleService{
			appSetting: appSetting,
			ca:         logics.CA,
			sa:         logics.CHCSA,
			ps:         ps,
		}
	})
	return chcsService
}

func (chcss *catalogHealthCheckScheduleService) Create(ctx context.Context, catalog *interfaces.Catalog,
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

	now := time.Now().UnixMilli()
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
		next, err := cron.ParseStandard(req.CronExpr)
		if err != nil {
			span.SetStatus(codes.Error, "Invalid cron expression")
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InvalidParameter).WithErrorDetails(fmt.Sprintf("invalid cron_expr: %v", err))
		}
		schedule.NextRun = next.Next(time.Now()).UnixMilli()
	}

	if err := chcss.sa.Create(ctx, schedule); err != nil {
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
		return nil, err
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
		return nil, err
	}
	if catalog == nil || catalog.Type != interfaces.CatalogTypePhysical {
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

	now := time.Now().UnixMilli()
	account := interfaces.AccountInfo{}
	if value := ctx.Value(interfaces.ACCOUNT_INFO_KEY); value != nil {
		account = value.(interfaces.AccountInfo)
	}

	schedule, err := chcss.sa.GetByCatalogID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get health check schedule failed")
		otellog.LogError(ctx, "Get catalog health check schedule failed", err)
		return nil, err
	}

	schedule.Mode = req.Mode
	schedule.Updater = account
	schedule.UpdateTime = now
	switch req.Mode {
	case interfaces.CatalogHealthCheckScheduleModeInherit:
		schedule.CronExpr = ""
		schedule.NextRun = 0
	case interfaces.CatalogHealthCheckScheduleModeEnabled:
		schedule.CronExpr = req.CronExpr
		next, parseErr := cron.ParseStandard(req.CronExpr)
		if parseErr != nil {
			span.SetStatus(codes.Error, "Invalid cron expression")
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InvalidParameter).WithErrorDetails(fmt.Sprintf("invalid cron_expr: %v", parseErr))
		}
		schedule.NextRun = next.Next(time.Now()).UnixMilli()
	case interfaces.CatalogHealthCheckScheduleModeDisabled:
		schedule.NextRun = 0
	}

	if err := chcss.sa.Update(ctx, schedule); err != nil {
		span.SetStatus(codes.Error, "Update health check schedule failed")
		otellog.LogError(ctx, "Update catalog health check schedule failed", err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return schedule, nil
}

func (chcss *catalogHealthCheckScheduleService) DeleteByCatalogIDs(ctx context.Context, catalogIDs []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogHealthCheckScheduleService.DeleteByCatalogIDs")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_ids").StringSlice(catalogIDs))

	if err := chcss.sa.DeleteByCatalogIDs(ctx, catalogIDs); err != nil {
		span.SetStatus(codes.Error, "Delete health check schedules failed")
		otellog.LogError(ctx, "Delete catalog health check schedules failed", err)
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
