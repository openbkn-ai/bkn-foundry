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
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func (ots *objectTypeService) validateLogicMetricProperty(ctx context.Context, objectType *interfaces.ObjectType, lp *interfaces.LogicProperty) error {
	if lp == nil || lp.DataSource == nil || strings.TrimSpace(lp.DataSource.ID) == "" {
		return nil
	}

	def, err := ots.ma.GetMetricByID(ctx, objectType.KNID, objectType.Branch, lp.DataSource.ID)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(invalidParameterDetail(ctx, "MetricLookupFailed", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID}))
	}
	if def == nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(invalidParameterDetail(ctx, "MetricNotFound", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "metric": lp.DataSource.ID}))
	}
	if strings.TrimSpace(def.ScopeRef) != strings.TrimSpace(objectType.OTID) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(invalidParameterDetail(ctx, "MetricScopeMismatch", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "scopeRef": def.ScopeRef, "objectTypeID": objectType.OTID}))
	}
	return nil
}

func (ots *objectTypeService) enrichLogicMetricProperty(ctx context.Context, objectType *interfaces.ObjectType, logicProp *interfaces.LogicProperty, idx int) {
	if logicProp == nil || logicProp.DataSource == nil || strings.TrimSpace(logicProp.DataSource.ID) == "" {
		return
	}
	def, err := ots.ma.GetMetricByID(ctx, objectType.KNID, objectType.Branch, logicProp.DataSource.ID)
	if err != nil || def == nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Object type [%s]'s logic property [%s] KN metric [%s] not found, error: %v",
			objectType.OTID, logicProp.Name, logicProp.DataSource.ID, err))
		return
	}
	objectType.LogicProperties[idx].DataSource.Name = def.Name
	if len(def.AnalysisDimensions) > 0 {
		dims := make([]interfaces.Field, 0, len(def.AnalysisDimensions))
		for _, ad := range def.AnalysisDimensions {
			dims = append(dims, interfaces.Field{
				Name:        ad.Name,
				DisplayName: ad.DisplayName,
			})
		}
		objectType.LogicProperties[idx].AnalysisDims = dims
	}
	processKNMetricPropertyParamComment(ctx, logicProp, def, objectType, idx)
}

func processKNMetricPropertyParamComment(ctx context.Context, logicProp *interfaces.LogicProperty, def *interfaces.MetricDefinition,
	objectType *interfaces.ObjectType, j int) {

	dimDisplay := map[string]string{}
	for _, ad := range def.AnalysisDimensions {
		dimDisplay[ad.Name] = ad.DisplayName
	}
	for k, param := range logicProp.Parameters {
		if display, ok := dimDisplay[param.Name]; ok && display != "" {
			comment := display
			objectType.LogicProperties[j].Parameters[k].Comment = &comment
			continue
		}
		switch param.Name {
		case "instant":
			comment := invalidParameterDetail(ctx, "MetricInstantComment", nil)
			objectType.LogicProperties[j].Parameters[k].Comment = &comment
		case "start":
			comment := invalidParameterDetail(ctx, "MetricStartComment", nil)
			objectType.LogicProperties[j].Parameters[k].Comment = &comment
		case "end":
			comment := invalidParameterDetail(ctx, "MetricEndComment", nil)
			objectType.LogicProperties[j].Parameters[k].Comment = &comment
		case "step":
			comment := invalidParameterDetail(ctx, "MetricStepComment", nil)
			objectType.LogicProperties[j].Parameters[k].Comment = &comment
		default:
			otellog.LogWarn(ctx, fmt.Sprintf("Object type [%s]'s logic property [%s]'s parameter[%s] not found in KN metric[%s]",
				objectType.OTID, logicProp.Name, param.Name, def.ID))
		}
	}
}
