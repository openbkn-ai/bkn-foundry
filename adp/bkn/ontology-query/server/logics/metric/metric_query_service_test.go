// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package metric

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"ontology-query/common"
	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	dtype "ontology-query/interfaces/data_type"
	omock "ontology-query/interfaces/mock"
)

func Test_metricGroupByDimensions_analysisDimensions(t *testing.T) {
	Convey("metricGroupByDimensions respects analysis_dimensions\n", t, func() {
		ctx := context.Background()
		propMap := map[string]*cond.DataProperty{
			"warehouse_id": {Name: "warehouse_id", MappedField: cond.Field{Name: "warehouse_id_res"}},
			"item_code":    {Name: "item_code", MappedField: cond.Field{Name: "item_code_res"}},
			"region":       {Name: "region", MappedField: cond.Field{Name: "region_res"}},
			"evt_time":     {Name: "evt_time", MappedField: cond.Field{Name: "evt_time_res"}},
			"time_alias":   {Name: "time_alias", MappedField: cond.Field{Name: "evt_time_res"}},
		}
		def := &interfaces.MetricDefinition{
			TimeDimension: &interfaces.MetricTimeDimension{Property: "evt_time"},
			CalculationFormula: &interfaces.MetricCalculationFormula{
				GroupBy: []interfaces.MetricGroupBy{{Property: "region"}},
			},
			AnalysisDimensions: []interfaces.MetricAnalysisDimension{
				{Name: "warehouse_id"},
				{Name: "item_code"},
				{Name: "evt_time"},
				{Name: "time_alias"},
			},
		}

		Convey("Without request analysis_dimensions uses calculation_formula.group_by only\n", func() {
			dims, err := metricGroupByDimensions(ctx, def, &interfaces.MetricQueryRequest{}, propMap, "")
			So(err, ShouldBeNil)
			So(len(dims), ShouldEqual, 1)
			So(dims[0].PropertyName, ShouldEqual, "region")
			So(dims[0].ResourceFieldName, ShouldEqual, "region_res")
		})

		Convey("Request analysis_dimensions appends valid dimensions after fixed group_by and preserves query order\n", func() {
			dims, err := metricGroupByDimensions(ctx, def, &interfaces.MetricQueryRequest{
				AnalysisDimensions: []string{"item_code", "warehouse_id", "unknown"},
			}, propMap, "")
			So(err, ShouldBeNil)
			So(len(dims), ShouldEqual, 3)
			So(dims[0].PropertyName, ShouldEqual, "region")
			So(dims[0].ResourceFieldName, ShouldEqual, "region_res")
			So(dims[1].PropertyName, ShouldEqual, "item_code")
			So(dims[1].ResourceFieldName, ShouldEqual, "item_code_res")
			So(dims[2].PropertyName, ShouldEqual, "warehouse_id")
			So(dims[2].ResourceFieldName, ShouldEqual, "warehouse_id_res")
		})

		Convey("Non-trend request keeps time_dimension property as an analysis dimension\n", func() {
			dims, err := metricGroupByDimensions(ctx, def, &interfaces.MetricQueryRequest{
				AnalysisDimensions: []string{"evt_time", "warehouse_id"},
			}, propMap, "")
			So(err, ShouldBeNil)
			So(len(dims), ShouldEqual, 3)
			So(dims[0].PropertyName, ShouldEqual, "region")
			So(dims[0].ResourceFieldName, ShouldEqual, "region_res")
			So(dims[1].PropertyName, ShouldEqual, "evt_time")
			So(dims[1].ResourceFieldName, ShouldEqual, "evt_time_res")
			So(dims[2].PropertyName, ShouldEqual, "warehouse_id")
			So(dims[2].ResourceFieldName, ShouldEqual, "warehouse_id_res")
		})

		Convey("Trend request excludes aliases mapped to the time resource field\n", func() {
			dims, err := metricGroupByDimensions(ctx, def, &interfaces.MetricQueryRequest{
				AnalysisDimensions: []string{"time_alias", "warehouse_id"},
			}, propMap, "evt_time_res")
			So(err, ShouldBeNil)
			So(len(dims), ShouldEqual, 2)
			So(dims[0].PropertyName, ShouldEqual, "region")
			So(dims[0].ResourceFieldName, ShouldEqual, "region_res")
			So(dims[1].PropertyName, ShouldEqual, "warehouse_id")
			So(dims[1].ResourceFieldName, ShouldEqual, "warehouse_id_res")
		})

		Convey("Non-trend request keeps aliases mapped to the time resource field\n", func() {
			dims, err := metricGroupByDimensions(ctx, def, &interfaces.MetricQueryRequest{
				AnalysisDimensions: []string{"time_alias"},
			}, propMap, "")
			So(err, ShouldBeNil)
			So(len(dims), ShouldEqual, 2)
			So(dims[0].PropertyName, ShouldEqual, "region")
			So(dims[0].ResourceFieldName, ShouldEqual, "region_res")
			So(dims[1].PropertyName, ShouldEqual, "time_alias")
			So(dims[1].ResourceFieldName, ShouldEqual, "evt_time_res")
		})
	})
}

func Test_mapDataPropertyToResourceField_localizesErrors(t *testing.T) {
	propMap := map[string]*cond.DataProperty{
		"unmapped": {Name: "unmapped"},
	}
	tests := []struct {
		name     string
		language rest.Language
		property string
		expected string
	}{
		{
			name:     "Chinese invalid property",
			language: rest.SimplifiedChinese,
			property: "missing",
			expected: "属性 missing 不是对象类的数据属性。",
		},
		{
			name:     "English missing mapped field",
			language: rest.AmericanEnglish,
			property: "unmapped",
			expected: "Property unmapped has no mapped_field and cannot be pushed down to the resource.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := rest.WithLanguage(context.Background(), tt.language)
			_, err := mapDataPropertyToResourceField(ctx, tt.property, propMap)
			if err == nil {
				t.Fatal("mapDataPropertyToResourceField() returned nil error")
			}
			if err.Error() != tt.expected {
				t.Fatalf("error = %q, want %q", err.Error(), tt.expected)
			}
		})
	}
}

func Test_mergeConditions(t *testing.T) {
	Convey("mergeConditions combines non-nil trees with AND\n", t, func() {
		a := &cond.CondCfg{Operation: cond.OperationEq, Name: "warehouse", ValueOptCfg: cond.ValueOptCfg{Value: "wh-1"}}
		b := &cond.CondCfg{Operation: cond.OperationEq, Name: "stock_status", ValueOptCfg: cond.ValueOptCfg{Value: "可用"}}

		So(mergeConditions(nil, nil), ShouldBeNil)
		So(mergeConditions(a, nil), ShouldEqual, a)
		So(mergeConditions(nil, b), ShouldEqual, b)

		merged := mergeConditions(a, b)
		So(merged.Operation, ShouldEqual, cond.OperationAnd)
		So(merged.SubConds, ShouldHaveLength, 2)
		So(merged.SubConds[0], ShouldEqual, a)
		So(merged.SubConds[1], ShouldEqual, b)
	})
}

func Test_buildResourceDataQueryParams_conditionMerge(t *testing.T) {
	Convey("buildResourceDataQueryParams merges definition and query-time conditions with AND\n", t, func() {
		ctx := context.Background()
		svc := &metricQueryService{appSetting: &common.AppSetting{}}
		ot := interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				DataProperties: []cond.DataProperty{
					{Name: "warehouse", Type: dtype.DATATYPE_STRING, MappedField: cond.Field{Name: "warehouse_res"}},
					{Name: "stock_status", Type: dtype.DATATYPE_STRING, MappedField: cond.Field{Name: "stock_status_res"}},
					{Name: "amount", Type: dtype.DATATYPE_DOUBLE, MappedField: cond.Field{Name: "amount_res"}},
				},
			},
		}
		def := &interfaces.MetricDefinition{
			CalculationFormula: &interfaces.MetricCalculationFormula{
				Condition: &cond.CondCfg{
					Operation:   cond.OperationIn,
					Name:        "warehouse",
					ValueOptCfg: cond.ValueOptCfg{Value: []any{"昆山成品仓"}},
				},
				Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: interfaces.MetricAggrSum},
			},
		}

		Convey("empty query body uses definition condition only\n", func() {
			params, _, err := svc.buildResourceDataQueryParams(ctx, def, &interfaces.MetricQueryRequest{}, ot)
			So(err, ShouldBeNil)
			So(params.FilterCondition["operation"], ShouldEqual, cond.OperationIn)
			So(params.FilterCondition["field"], ShouldEqual, "warehouse_res")
		})

		Convey("query-time condition ANDs with definition condition\n", func() {
			params, _, err := svc.buildResourceDataQueryParams(ctx, def, &interfaces.MetricQueryRequest{
				Condition: &cond.CondCfg{
					Operation: cond.OperationEq,
					Name:      "stock_status",
					ValueOptCfg: cond.ValueOptCfg{
						Value: "可用",
					},
				},
			}, ot)
			So(err, ShouldBeNil)
			So(params.FilterCondition["operation"], ShouldEqual, cond.OperationAnd)
			subs, ok := params.FilterCondition["sub_conditions"].([]any)
			So(ok, ShouldBeTrue)
			So(len(subs), ShouldEqual, 2)
		})

		Convey("definition, query-time, and time conditions nest as two-level AND\n", func() {
			defTime := *def
			defTime.TimeDimension = &interfaces.MetricTimeDimension{Property: "evt_time"}
			otTime := ot
			otTime.DataProperties = append(otTime.DataProperties,
				cond.DataProperty{Name: "evt_time", Type: dtype.DATATYPE_DATETIME, MappedField: cond.Field{Name: "evt_time_res"}},
			)
			instant := true
			start := int64(1_000)
			end := int64(2_000)
			params, _, err := svc.buildResourceDataQueryParams(ctx, &defTime, &interfaces.MetricQueryRequest{
				Condition: &cond.CondCfg{
					Operation:   cond.OperationEq,
					Name:        "stock_status",
					ValueOptCfg: cond.ValueOptCfg{Value: "可用"},
				},
				Time: &interfaces.MetricTimeWindow{
					Start: &start, End: &end, Instant: &instant,
				},
			}, otTime)
			So(err, ShouldBeNil)
			So(params.FilterCondition["operation"], ShouldEqual, cond.OperationAnd)
			topSubs, ok := params.FilterCondition["sub_conditions"].([]any)
			So(ok, ShouldBeTrue)
			So(len(topSubs), ShouldEqual, 2)
			defQueryAnd, ok := topSubs[0].(map[string]any)
			So(ok, ShouldBeTrue)
			So(defQueryAnd["operation"], ShouldEqual, cond.OperationAnd)
			innerSubs, ok := defQueryAnd["sub_conditions"].([]any)
			So(ok, ShouldBeTrue)
			So(len(innerSubs), ShouldEqual, 2)
			timeLeaf, ok := topSubs[1].(map[string]any)
			So(ok, ShouldBeTrue)
			So(timeLeaf["operation"], ShouldEqual, cond.OperationRange)
			So(timeLeaf["field"], ShouldEqual, "evt_time_res")
		})
	})
}

func Test_metricQueryService_buildResourceDataQueryParams_analysisDimensions(t *testing.T) {
	Convey("buildResourceDataQueryParams pushes analysis_dimensions to group_by\n", t, func() {
		ctx := context.Background()
		svc := &metricQueryService{appSetting: &common.AppSetting{}}
		def := &interfaces.MetricDefinition{
			TimeDimension: &interfaces.MetricTimeDimension{Property: "evt_time"},
			CalculationFormula: &interfaces.MetricCalculationFormula{
				Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: "sum"},
				GroupBy:     []interfaces.MetricGroupBy{{Property: "evt_time"}},
			},
			AnalysisDimensions: []interfaces.MetricAnalysisDimension{
				{Name: "warehouse_id"},
				{Name: "item_code"},
				{Name: "evt_time"},
				{Name: "time_alias"},
			},
		}
		ot := interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				DataProperties: []cond.DataProperty{
					{Name: "amount", Type: dtype.DATATYPE_DOUBLE, MappedField: cond.Field{Name: "amount_res"}},
					{Name: "evt_time", Type: dtype.DATATYPE_DATETIME, MappedField: cond.Field{Name: "evt_time_res"}},
					{Name: "time_alias", Type: dtype.DATATYPE_DATETIME, MappedField: cond.Field{Name: "evt_time_res"}},
					{Name: "warehouse_id", Type: dtype.DATATYPE_STRING, MappedField: cond.Field{Name: "warehouse_id_res"}},
					{Name: "item_code", Type: dtype.DATATYPE_STRING, MappedField: cond.Field{Name: "item_code_res"}},
					{Name: "region", Type: dtype.DATATYPE_STRING, MappedField: cond.Field{Name: "region_res"}},
				},
			},
		}
		instant := false
		step := "year"
		start := int64(1_000)
		end := int64(2_000)

		params, trend, err := svc.buildResourceDataQueryParams(ctx, def, &interfaces.MetricQueryRequest{
			Time: &interfaces.MetricTimeWindow{Start: &start, End: &end, Instant: &instant, Step: &step},
		}, ot)
		So(err, ShouldBeNil)
		So(trend, ShouldNotBeNil)
		So(len(params.GroupBy), ShouldEqual, 1)
		So(params.GroupBy[0]["property"], ShouldEqual, "evt_time_res")
		So(params.GroupBy[0]["calendar_interval"], ShouldEqual, "year")

		params, trend, err = svc.buildResourceDataQueryParams(ctx, def, &interfaces.MetricQueryRequest{
			AnalysisDimensions: []string{"warehouse_id", "item_code"},
			Time:               &interfaces.MetricTimeWindow{Start: &start, End: &end, Instant: &instant, Step: &step},
		}, ot)
		So(err, ShouldBeNil)
		So(trend, ShouldNotBeNil)
		So(len(params.GroupBy), ShouldEqual, 3)
		So(params.GroupBy[0]["property"], ShouldEqual, "warehouse_id_res")
		So(params.GroupBy[1]["property"], ShouldEqual, "item_code_res")
		So(params.GroupBy[2]["property"], ShouldEqual, "evt_time_res")
		So(params.GroupBy[2]["calendar_interval"], ShouldEqual, "year")

		defFixedGroup := *def
		defFixedGroup.CalculationFormula = &interfaces.MetricCalculationFormula{
			Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: "sum"},
			GroupBy:     []interfaces.MetricGroupBy{{Property: "region"}},
		}
		params, trend, err = svc.buildResourceDataQueryParams(ctx, &defFixedGroup, &interfaces.MetricQueryRequest{
			AnalysisDimensions: []string{"warehouse_id", "item_code"},
			Time:               &interfaces.MetricTimeWindow{Start: &start, End: &end, Instant: &instant, Step: &step},
		}, ot)
		So(err, ShouldBeNil)
		So(trend, ShouldNotBeNil)
		So(len(params.GroupBy), ShouldEqual, 4)
		So(params.GroupBy[0]["property"], ShouldEqual, "region_res")
		So(params.GroupBy[1]["property"], ShouldEqual, "warehouse_id_res")
		So(params.GroupBy[2]["property"], ShouldEqual, "item_code_res")
		So(params.GroupBy[3]["property"], ShouldEqual, "evt_time_res")
		So(params.GroupBy[3]["calendar_interval"], ShouldEqual, "year")

		params, trend, err = svc.buildResourceDataQueryParams(ctx, def, &interfaces.MetricQueryRequest{
			AnalysisDimensions: []string{"warehouse_id", "time_alias"},
			Time:               &interfaces.MetricTimeWindow{Start: &start, End: &end, Instant: &instant, Step: &step},
		}, ot)
		So(err, ShouldBeNil)
		So(trend, ShouldNotBeNil)
		So(len(params.GroupBy), ShouldEqual, 2)
		So(params.GroupBy[0]["property"], ShouldEqual, "warehouse_id_res")
		So(params.GroupBy[1]["property"], ShouldEqual, "evt_time_res")
		So(params.GroupBy[1]["calendar_interval"], ShouldEqual, "year")

		instant = true
		params, trend, err = svc.buildResourceDataQueryParams(ctx, def, &interfaces.MetricQueryRequest{
			Time: &interfaces.MetricTimeWindow{Start: &start, End: &end, Instant: &instant},
		}, ot)
		So(err, ShouldBeNil)
		So(trend, ShouldBeNil)
		So(len(params.GroupBy), ShouldEqual, 1)
		So(params.GroupBy[0]["property"], ShouldEqual, "evt_time_res")
		_, hasCalendarInterval := params.GroupBy[0]["calendar_interval"]
		So(hasCalendarInterval, ShouldBeFalse)

		params, trend, err = svc.buildResourceDataQueryParams(ctx, def, &interfaces.MetricQueryRequest{
			AnalysisDimensions: []string{"time_alias"},
			Time:               &interfaces.MetricTimeWindow{Start: &start, End: &end, Instant: &instant},
		}, ot)
		So(err, ShouldBeNil)
		So(trend, ShouldBeNil)
		So(len(params.GroupBy), ShouldEqual, 1)
		So(params.GroupBy[0]["property"], ShouldEqual, "evt_time_res")
		_, hasCalendarInterval = params.GroupBy[0]["calendar_interval"]
		So(hasCalendarInterval, ShouldBeFalse)
	})
}

func Test_convert2TimeSeries_excludesTrendTimeFromDimensions(t *testing.T) {
	Convey("convert2TimeSeries excludes trend time_dimension from analysis dimension series key\n", t, func() {
		ctx := context.Background()
		def := interfaces.MetricDefinition{
			TimeDimension: &interfaces.MetricTimeDimension{Property: "evt_time"},
			AnalysisDimensions: []interfaces.MetricAnalysisDimension{
				{Name: "warehouse_id"},
				{Name: "time_alias"},
			},
		}
		propMap := map[string]*cond.DataProperty{
			"warehouse_id": {Name: "warehouse_id", MappedField: cond.Field{Name: "warehouse_id_res"}},
			"evt_time":     {Name: "evt_time", MappedField: cond.Field{Name: "evt_time_res"}},
			"time_alias":   {Name: "time_alias", MappedField: cond.Field{Name: "evt_time_res"}},
		}
		step := "day"
		query := &interfaces.MetricQueryRequest{
			AnalysisDimensions: []string{"warehouse_id", "time_alias"},
			Time:               &interfaces.MetricTimeWindow{Step: &step},
		}
		trend := &trendMeta{
			step:         step,
			timeProperty: "evt_time",
			timeResField: "evt_time_res",
		}
		datas := &interfaces.DatasetQueryResponse{
			Entries: []map[string]any{
				{"warehouse_id_res": "wh-1", "evt_time_res": int64(1_700_000_000_000), "__value": 1.0},
				{"warehouse_id_res": "wh-1", "evt_time_res": int64(1_700_086_400_000), "__value": 2.0},
			},
		}

		series, err := convert2TimeSeries(ctx, def, datas, query, trend, propMap, false)
		So(err, ShouldBeNil)
		So(len(series), ShouldEqual, 1)
		for _, item := range series {
			So(item.Labels, ShouldResemble, map[string]string{"warehouse_id": "wh-1"})
			So(len(item.Times), ShouldEqual, 2)
			So(item.Values, ShouldResemble, []any{1.0, 2.0})
		}
	})
}

func Test_metricQueryService_QueryMetricData(t *testing.T) {
	Convey("QueryMetricData\n", t, func() {
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		oma := omock.NewMockOntologyManagerAccess(ctrl)
		vba := omock.NewMockVegaBackendAccess(ctrl)

		svc := &metricQueryService{
			appSetting: &common.AppSetting{},
			oma:        oma,
			vba:        vba,
		}

		def := &interfaces.MetricDefinition{
			ID:        "m1",
			KnID:      "kn1",
			ScopeType: interfaces.ScopeTypeObjectType,
			ScopeRef:  "ot1",
			UnitType:  "numUnit",
			Unit:      "none",
			CalculationFormula: &interfaces.MetricCalculationFormula{
				Condition:   &cond.CondCfg{Operation: cond.OperationEq, Name: "f1", ValueOptCfg: cond.ValueOptCfg{Value: 1}},
				Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: "sum"},
			},
		}

		Convey("Success\n", func() {
			oma.EXPECT().GetMetricDefinition(gomock.Any(), "kn1", "main", "m1").Return(def, true, nil)
			oma.EXPECT().GetObjectType(gomock.Any(), "kn1", "main", "ot1").Return(interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
					DataProperties: []cond.DataProperty{
						{Name: "f1", Type: dtype.DATATYPE_STRING, MappedField: cond.Field{Name: "f1_res"}},
						{Name: "amount", Type: dtype.DATATYPE_DOUBLE, MappedField: cond.Field{Name: "amount_res"}},
					},
				},
			}, true, nil)
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).Return(&interfaces.DatasetQueryResponse{
				Entries: []map[string]any{{"__value": 42.0}},
			}, nil)

			out, err := svc.QueryMetricData(ctx, "kn1", "main", "m1", &interfaces.MetricQueryRequest{})
			So(err, ShouldBeNil)
			So(len(out.Datas), ShouldEqual, 1)
			So(out.Datas[0].Values[0], ShouldEqual, 42.0)
			So(out.Model.UnitType, ShouldEqual, "numUnit")
		})

		Convey("Trend query uses calendar step, time group_by, time sort, and uniquery-style time block\n", func() {
			defTrend := &interfaces.MetricDefinition{
				ID:        "m1",
				KnID:      "kn1",
				ScopeType: interfaces.ScopeTypeObjectType,
				ScopeRef:  "ot1",
				UnitType:  "numUnit",
				Unit:      "none",
				TimeDimension: &interfaces.MetricTimeDimension{
					Property: "evt_time",
				},
				CalculationFormula: &interfaces.MetricCalculationFormula{
					Condition:   &cond.CondCfg{Operation: cond.OperationEq, Name: "f1", ValueOptCfg: cond.ValueOptCfg{Value: 1}},
					Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: "sum"},
				},
			}
			instant := false
			step := "month"
			start := int64(1_000)
			end := int64(2_000)
			var captured *interfaces.ResourceDataQueryParams
			oma.EXPECT().GetMetricDefinition(gomock.Any(), "kn1", "main", "m1").Return(defTrend, true, nil)
			oma.EXPECT().GetObjectType(gomock.Any(), "kn1", "main", "ot1").Return(interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
					DataProperties: []cond.DataProperty{
						{Name: "f1", Type: dtype.DATATYPE_STRING, MappedField: cond.Field{Name: "f1_res"}},
						{Name: "amount", Type: dtype.DATATYPE_DOUBLE, MappedField: cond.Field{Name: "amount_res"}},
						{Name: "evt_time", Type: dtype.DATATYPE_DATETIME, MappedField: cond.Field{Name: "evt_time_res"}},
					},
				},
			}, true, nil)
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, p *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					captured = p
					return &interfaces.DatasetQueryResponse{
						Entries: []map[string]any{{"__value": 3.0, "evt_time_res": 1700000000000.0}},
					}, nil
				},
			)

			out, err := svc.QueryMetricData(ctx, "kn1", "main", "m1", &interfaces.MetricQueryRequest{
				Time: &interfaces.MetricTimeWindow{
					Start: &start, End: &end, Instant: &instant, Step: &step,
				},
			})
			So(err, ShouldBeNil)
			So(captured, ShouldNotBeNil)
			So(captured.GroupBy[0]["property"], ShouldEqual, "evt_time_res")
			So(captured.GroupBy[0]["calendar_interval"], ShouldEqual, "month")
			So(out.Step, ShouldEqual, "month")
			So(out.IsCalendar, ShouldBeTrue)
			So(out.Datas[0].Times[0], ShouldEqual, 1700000000000.0)
		})

		Convey("Trend query resolves time range from default_range_policy when start/end omitted\n", func() {
			defTrend := &interfaces.MetricDefinition{
				ID:        "m1",
				KnID:      "kn1",
				ScopeType: interfaces.ScopeTypeObjectType,
				ScopeRef:  "ot1",
				UnitType:  "numUnit",
				Unit:      "none",
				TimeDimension: &interfaces.MetricTimeDimension{
					Property:           "evt_time",
					DefaultRangePolicy: interfaces.MetricTimeDefaultRangePolicyLast1h,
				},
				CalculationFormula: &interfaces.MetricCalculationFormula{
					Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: "sum"},
				},
			}
			instant := false
			step := "day"
			var captured *interfaces.ResourceDataQueryParams
			oma.EXPECT().GetMetricDefinition(gomock.Any(), "kn1", "main", "m1").Return(defTrend, true, nil)
			oma.EXPECT().GetObjectType(gomock.Any(), "kn1", "main", "ot1").Return(interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
					DataProperties: []cond.DataProperty{
						{Name: "amount", Type: dtype.DATATYPE_DOUBLE, MappedField: cond.Field{Name: "amount_res"}},
						{Name: "evt_time", Type: dtype.DATATYPE_DATETIME, MappedField: cond.Field{Name: "evt_time_res"}},
					},
				},
			}, true, nil)
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, p *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					captured = p
					return &interfaces.DatasetQueryResponse{
						Entries: []map[string]any{{"__value": 1.0, "evt_time_res": 1.0}},
					}, nil
				},
			)

			_, err := svc.QueryMetricData(ctx, "kn1", "main", "m1", &interfaces.MetricQueryRequest{
				Time: &interfaces.MetricTimeWindow{
					Instant: &instant,
					Step:    &step,
				},
			})
			So(err, ShouldBeNil)
			So(captured, ShouldNotBeNil)
			So(captured.GroupBy[0]["property"], ShouldEqual, "evt_time_res")
			So(captured.GroupBy[0]["calendar_interval"], ShouldEqual, "day")
		})

		Convey("Instant query maps time window to vega range filter condition\n", func() {
			defInstant := &interfaces.MetricDefinition{
				ID:        "m1",
				KnID:      "kn1",
				ScopeType: interfaces.ScopeTypeObjectType,
				ScopeRef:  "ot1",
				UnitType:  "numUnit",
				Unit:      "none",
				TimeDimension: &interfaces.MetricTimeDimension{
					Property: "evt_time",
				},
				CalculationFormula: &interfaces.MetricCalculationFormula{
					Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: "sum"},
				},
			}
			instant := true
			start := int64(1_000)
			end := int64(2_000)
			var captured *interfaces.ResourceDataQueryParams
			oma.EXPECT().GetMetricDefinition(gomock.Any(), "kn1", "main", "m1").Return(defInstant, true, nil)
			oma.EXPECT().GetObjectType(gomock.Any(), "kn1", "main", "ot1").Return(interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
					DataProperties: []cond.DataProperty{
						{Name: "amount", Type: dtype.DATATYPE_DOUBLE, MappedField: cond.Field{Name: "amount_res"}},
						{Name: "evt_time", Type: dtype.DATATYPE_DATETIME, MappedField: cond.Field{Name: "evt_time_res"}},
					},
				},
			}, true, nil)
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, p *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					captured = p
					return &interfaces.DatasetQueryResponse{
						Entries: []map[string]any{{"__value": 2.0}},
					}, nil
				},
			)

			_, err := svc.QueryMetricData(ctx, "kn1", "main", "m1", &interfaces.MetricQueryRequest{
				Time: &interfaces.MetricTimeWindow{
					Start: &start, End: &end, Instant: &instant,
				},
			})
			So(err, ShouldBeNil)
			So(captured, ShouldNotBeNil)
			So(captured.FilterCondition["field"], ShouldEqual, "evt_time_res")
			So(captured.FilterCondition["operation"], ShouldEqual, cond.OperationRange)
			So(captured.FilterCondition["value"], ShouldResemble, []any{float64(start), float64(end) + 1})
		})

		Convey("Not found when bkn returns nil definition\n", func() {
			oma.EXPECT().GetMetricDefinition(gomock.Any(), "kn1", "main", "m1").Return(nil, false, nil)
			_, err := svc.QueryMetricData(ctx, "kn1", "main", "m1", &interfaces.MetricQueryRequest{})
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, oerrors.OntologyQuery_Metric_NotFound)
		})
	})
}

// 趋势 + 日历 day 曾误走 ParseDuration("day")→step=0，产生 -28800000ms 等错误时间轴；fill_null 须走日历对齐。
func Test_correctingTime_trendCalendarDay(t *testing.T) {
	Convey("correctingTime uses calendar path for trend day (not ParseDuration)\n", t, func() {
		instant := false
		step := "day"
		start := int64(1_776_729_600_000)
		end := int64(1_776_816_000_000)
		q := &interfaces.MetricQueryRequest{
			Time: &interfaces.MetricTimeWindow{Start: &start, End: &end, Instant: &instant, Step: &step},
		}
		fs, fe := correctingTime(q, time.UTC)
		So(fs, ShouldNotEqual, -28800000)
		So(fe >= fs, ShouldBeTrue)
	})
}

// 同环比「同期」须与 convert2TimeSeries 的日历分桶键一致；仅靠毫秒相等会错配桶，导致增长值=本期-错误的同期。
func Test_lookupSamePeriodBaseValue_timeStrAlignment(t *testing.T) {
	Convey("lookupSamePeriodBaseValue matches calendar TimeStr when millis differ\n", t, func() {
		step := "day"
		msTarget := int64(1_776_787_200_000)
		key := common.FormatTimeMiliis(msTarget, step)
		prev := interfaces.BknMetricData{
			Times:    []any{int64(0), int64(999)}, // 与分桶毫秒不一致，仅靠 ptm==compareDate 会对不上
			TimeStrs: []string{"1970-01-01", key},
			Values:   []any{0.0, 2.0},
		}
		v, ok := lookupSamePeriodBaseValue(prev, msTarget, step)
		So(ok, ShouldBeTrue)
		So(v, ShouldEqual, 2.0)
	})
}

func Test_metricQueryService_DryRunMetricData(t *testing.T) {
	Convey("DryRunMetricData\n", t, func() {
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		oma := omock.NewMockOntologyManagerAccess(ctrl)
		vba := omock.NewMockVegaBackendAccess(ctrl)

		svc := &metricQueryService{
			appSetting: &common.AppSetting{},
			oma:        oma,
			vba:        vba,
		}

		Convey("Fails when kn_id mismatches metric_config.kn_id\n", func() {
			body := &interfaces.MetricDryRunRequest{
				MetricConfig: &interfaces.MetricDefinition{
					ID:         "x",
					KnID:       "other",
					MetricType: "atomic",
					ScopeType:  interfaces.ScopeTypeObjectType,
					ScopeRef:   "ot1",
					CalculationFormula: &interfaces.MetricCalculationFormula{
						Aggregation: interfaces.MetricAggregation{Property: "p", Aggr: "sum"},
					},
				},
			}
			_, err := svc.DryRunMetricData(ctx, "kn1", "main", body)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Success without persisting metric_id\n", func() {
			body := &interfaces.MetricDryRunRequest{
				MetricConfig: &interfaces.MetricDefinition{
					ID:         "tmp",
					KnID:       "kn1",
					MetricType: "atomic",
					ScopeType:  interfaces.ScopeTypeObjectType,
					ScopeRef:   "ot1",
					UnitType:   "numUnit",
					Unit:       "none",
					CalculationFormula: &interfaces.MetricCalculationFormula{
						Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: "sum"},
					},
				},
			}
			oma.EXPECT().GetObjectType(gomock.Any(), "kn1", "main", "ot1").Return(interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
					DataProperties: []cond.DataProperty{
						{Name: "amount", Type: dtype.DATATYPE_DOUBLE, MappedField: cond.Field{Name: "amount_res"}},
					},
				},
			}, true, nil)
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).Return(&interfaces.DatasetQueryResponse{
				Entries: []map[string]any{{"__value": 1.0}},
			}, nil)

			out, err := svc.DryRunMetricData(ctx, "kn1", "main", body)
			So(err, ShouldBeNil)
			So(out.Datas[0].Values[0], ShouldEqual, 1.0)
		})
	})
}
