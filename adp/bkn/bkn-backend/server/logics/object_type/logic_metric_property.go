// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

const objectMetricV1SourcePrefix = "object_metric_v1:"

func parseObjectMetricV1SourceID(sourceID string) (string, int, bool) {
	if !strings.HasPrefix(sourceID, objectMetricV1SourcePrefix) {
		return "", 0, false
	}
	metricID, rawVersion, found := strings.Cut(strings.TrimPrefix(sourceID, objectMetricV1SourcePrefix), "@")
	if !found || metricID == "" {
		return "", 0, false
	}
	version, err := strconv.Atoi(rawVersion)
	return metricID, version, err == nil && version > 0
}

func validateObjectMetricV1PropertyUniqueness(ctx context.Context, objectType *interfaces.ObjectType) error {
	seen := make(map[string]string)
	for _, property := range objectType.LogicProperties {
		if property == nil || property.Type != interfaces.LOGIC_PROPERTY_TYPE_METRIC || property.DataSource == nil {
			continue
		}
		metricID, _, ok := parseObjectMetricV1SourceID(property.DataSource.ID)
		if !ok {
			continue
		}
		if previous, exists := seen[metricID]; exists {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.ObjectMetricPropertyAlreadyBound", map[string]any{
				"objectType": objectType.OTName, "metric": metricID, "property": previous,
			})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
		}
		seen[metricID] = property.Name
	}
	return nil
}

func (ots *objectTypeService) validateLogicMetricProperty(ctx context.Context, objectType *interfaces.ObjectType, lp *interfaces.LogicProperty) error {
	if lp == nil || lp.DataSource == nil || strings.TrimSpace(lp.DataSource.ID) == "" {
		return nil
	}
	metricID, version, ok := parseObjectMetricV1SourceID(lp.DataSource.ID)
	if !ok {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.MetricNotFound", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	if logics.OMA == nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.ObjectMetricExtensionRequired", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
	}
	record, err := logics.OMA.GetObjectMetric(ctx, objectType.KNID, objectType.Branch, metricID, version)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.MetricLookupFailed", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	if record == nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.MetricNotFound", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
	}
	status := record.Definition.Lifecycle.Status
	if status != "published" && status != "deprecated" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.ObjectMetricLifecycleInvalid", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID, "status": status})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	if record.Definition.CalculationScope != interfaces.ObjectMetricScopeInstance {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.ObjectMetricCalculationScopeInvalid", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID, "scope": record.Definition.CalculationScope})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	if record.Definition.OwnerObjectTypeID != objectType.OTID {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail.ObjectMetricOwnerMismatch", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID, "ownerObjectTypeID": record.Definition.OwnerObjectTypeID, "objectTypeID": objectType.OTID})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).WithErrorDetails(localizedText)
	}
	return nil
}

func (ots *objectTypeService) enrichLogicMetricProperty(ctx context.Context, objectType *interfaces.ObjectType, logicProp *interfaces.LogicProperty, idx int) {
	if logicProp == nil || logicProp.DataSource == nil || strings.TrimSpace(logicProp.DataSource.ID) == "" {
		return
	}
	metricID, version, ok := parseObjectMetricV1SourceID(logicProp.DataSource.ID)
	if !ok {
		return
	}
	if logics.OMA == nil {
		return
	}
	record, err := logics.OMA.GetObjectMetric(ctx, objectType.KNID, objectType.Branch, metricID, version)
	if err == nil && record != nil {
		objectType.LogicProperties[idx].DataSource.Name = record.Definition.Name
	}
}
