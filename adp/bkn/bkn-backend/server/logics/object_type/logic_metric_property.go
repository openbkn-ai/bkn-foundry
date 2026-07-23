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

	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/rest"

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
			WithErrorDetails(fmt.Sprintf("对象类[%s]逻辑属性[%s]的 KN 指标[%s]获取失败: %s",
				objectType.OTName, lp.Name, lp.DataSource.ID, err.Error()))
	}
	if def == nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("对象类[%s]逻辑属性[%s]的 KN 指标[%s]不存在，须绑定 MetricDefinition.id",
				objectType.OTName, lp.Name, lp.DataSource.ID))
	}
	if strings.TrimSpace(def.ScopeRef) != strings.TrimSpace(objectType.OTID) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("对象类[%s]逻辑属性[%s]引用的指标 scope_ref[%s]须等于当前对象类 id[%s]",
				objectType.OTName, lp.Name, def.ScopeRef, objectType.OTID))
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
			comment := "是否是即时查询。可选，默认为 false。当 instant = true 时，表示即时查询；当 instant = false 时，表示范围查询。"
			objectType.LogicProperties[j].Parameters[k].Comment = &comment
		case "start":
			comment := "指标查询的开始时间。 start=<unix_timestamp>，单位到毫秒。 例如: 1646360670123"
			objectType.LogicProperties[j].Parameters[k].Comment = &comment
		case "end":
			comment := "指标查询的结束时间。end=<unix_timestamp>，单位到毫秒。例如: 1646471470123"
			objectType.LogicProperties[j].Parameters[k].Comment = &comment
		case "step":
			comment := "范围查询的步长。当 instant 为 false 时, 必须。step=<time_durations>，用一个数字，后面跟时间单位来定义。"
			objectType.LogicProperties[j].Parameters[k].Comment = &comment
		default:
			otellog.LogWarn(ctx, fmt.Sprintf("Object type [%s]'s logic property [%s]'s parameter[%s] not found in KN metric[%s]",
				objectType.OTID, logicProp.Name, param.Name, def.ID))
		}
	}
}
