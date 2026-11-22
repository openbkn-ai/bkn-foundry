// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func ValidateCatalogRequest(ctx context.Context, req *interfaces.CatalogRequest) error {
	if err := validateID(ctx, req.ID); err != nil {
		return err
	}
	if err := validateName(ctx, req.Name); err != nil {
		return err
	}
	if err := ValidateTags(ctx, req.Tags); err != nil {
		return err
	}
	if err := validateDescription(ctx, req.Description); err != nil {
		return err
	}
	if err := validateConnectorConfig(ctx, req.ConnectorCfg); err != nil {
		return err
	}
	return nil
}

// ValidateUpdateCatalogRequest 统一校验路径标识、目录字段及乐观更新版本。
func ValidateUpdateCatalogRequest(ctx context.Context, req *interfaces.CatalogRequest, pathID string) error {
	if req.ID != "" && req.ID != pathID {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_IDMismatch).
			WithErrorDetails(fmt.Sprintf("path id %q != body id %q", pathID, req.ID))
	}
	req.ID = pathID
	if err := ValidateCatalogRequest(ctx, req); err != nil {
		return err
	}
	return validateExpectedUpdateTime(ctx, req.ExpectedUpdateTime)
}

// ValidateCatalogConnectionTestRequest 校验未保存配置的必要连接字段。
func ValidateCatalogConnectionTestRequest(ctx context.Context, req *interfaces.CatalogConnectionTestRequest) error {
	if req.ConnectorType == "" || req.ConnectorCfg == nil {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Catalog_InvalidParameter).
			WithErrorDetails("connector_type and connector_config are required")
	}
	return nil
}

func ValidateCatalogListQueryParams(ctx context.Context, params interfaces.CatalogsQueryParams) error {
	if err := validateCatalogTypeQueryParam(ctx, params.Type); err != nil {
		return err
	}
	if err := validateCatalogHealthCheckStatusQueryParam(ctx, params.HealthCheckStatus); err != nil {
		return err
	}
	return nil
}

func validateCatalogTypeQueryParam(ctx context.Context, typ string) error {
	if typ == "" {
		return nil
	}

	switch typ {
	case interfaces.CatalogTypePhysical,
		interfaces.CatalogTypeLogical:
		return nil
	default:
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Catalog_InvalidParameter_Type).
			WithErrorDetails(fmt.Sprintf("invalid type: %s", typ))
	}
}

func validateCatalogHealthCheckStatusQueryParam(ctx context.Context, status string) error {
	if status == "" {
		return nil
	}

	switch status {
	case interfaces.CatalogHealthStatusHealthy,
		interfaces.CatalogHealthStatusDegraded,
		interfaces.CatalogHealthStatusUnhealthy,
		interfaces.CatalogHealthStatusOffline,
		interfaces.CatalogHealthStatusUnchecked:
		return nil
	default:
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Catalog_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("invalid health_check_status: %s", status))
	}
}
