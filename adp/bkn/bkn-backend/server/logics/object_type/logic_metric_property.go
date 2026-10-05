// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

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

func (ots *objectTypeService) validateLogicMetricProperty(ctx context.Context, objectType *interfaces.ObjectType, lp *interfaces.LogicProperty) error {
	if lp == nil || lp.DataSource == nil || strings.TrimSpace(lp.DataSource.ID) == "" {
		return nil
	}
	metricID, version, ok := parseObjectMetricV1SourceID(lp.DataSource.ID)
	if !ok {
		// Legacy references remain readable until the explicit migration job has
		// converted all persisted object types. New bindings never create them.
		def, err := ots.ma.GetMetricByID(ctx, objectType.KNID, objectType.Branch, lp.DataSource.ID)
		if err != nil || def == nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(invalidParameterDetail(ctx, "MetricNotFound", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID}))
		}
		if strings.TrimSpace(def.ScopeRef) != strings.TrimSpace(objectType.OTID) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(invalidParameterDetail(ctx, "MetricScopeMismatch", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "scopeRef": def.ScopeRef, "objectTypeID": objectType.OTID}))
		}
		return nil
	}
	if logics.OMA == nil {
		return rest.NewHTTPError(ctx, http.StatusServiceUnavailable, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(invalidParameterDetail(ctx, "MetricLookupFailed", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID}))
	}
	record, err := logics.OMA.GetObjectMetric(ctx, objectType.KNID, objectType.Branch, metricID, version)
	if err != nil || record == nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(invalidParameterDetail(ctx, "MetricNotFound", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID}))
	}
	if record.Definition.Lifecycle.Status != "published" || record.Definition.CalculationScope != interfaces.ObjectMetricScopeInstance || record.Definition.OwnerObjectTypeID != objectType.OTID {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(invalidParameterDetail(ctx, "MetricScopeMismatch", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "scopeRef": record.Definition.OwnerObjectTypeID, "objectTypeID": objectType.OTID}))
	}
	return nil
}

func (ots *objectTypeService) enrichLogicMetricProperty(ctx context.Context, objectType *interfaces.ObjectType, logicProp *interfaces.LogicProperty, idx int) {
	if logicProp == nil || logicProp.DataSource == nil || strings.TrimSpace(logicProp.DataSource.ID) == "" {
		return
	}
	metricID, version, ok := parseObjectMetricV1SourceID(logicProp.DataSource.ID)
	if !ok {
		def, err := ots.ma.GetMetricByID(ctx, objectType.KNID, objectType.Branch, logicProp.DataSource.ID)
		if err != nil || def == nil {
			otellog.LogWarn(ctx, fmt.Sprintf("Object type [%s]'s legacy metric property [%s] metric [%s] not found, error: %v",
				objectType.OTID, logicProp.Name, logicProp.DataSource.ID, err))
			return
		}
		objectType.LogicProperties[idx].DataSource.Name = def.Name
		if len(def.AnalysisDimensions) > 0 {
			dims := make([]interfaces.Field, 0, len(def.AnalysisDimensions))
			for _, dimension := range def.AnalysisDimensions {
				dims = append(dims, interfaces.Field{Name: dimension.Name, DisplayName: dimension.DisplayName})
			}
			objectType.LogicProperties[idx].AnalysisDims = dims
		}
		processLegacyMetricPropertyParamComment(ctx, logicProp, def, objectType, idx)
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

func processLegacyMetricPropertyParamComment(ctx context.Context, logicProp *interfaces.LogicProperty, def *interfaces.MetricDefinition,
	objectType *interfaces.ObjectType, index int) {
	dimensionDisplay := map[string]string{}
	for _, dimension := range def.AnalysisDimensions {
		dimensionDisplay[dimension.Name] = dimension.DisplayName
	}
	for parameterIndex, parameter := range logicProp.Parameters {
		if display, ok := dimensionDisplay[parameter.Name]; ok && display != "" {
			comment := display
			objectType.LogicProperties[index].Parameters[parameterIndex].Comment = &comment
			continue
		}
		messageID := ""
		switch parameter.Name {
		case "instant":
			messageID = "MetricInstantComment"
		case "start":
			messageID = "MetricStartComment"
		case "end":
			messageID = "MetricEndComment"
		case "step":
			messageID = "MetricStepComment"
		}
		if messageID != "" {
			comment := invalidParameterDetail(ctx, messageID, nil)
			objectType.LogicProperties[index].Parameters[parameterIndex].Comment = &comment
		}
	}
}
