// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"

	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func validStrictCreateMetric() *interfaces.MetricDefinition {
	return &interfaces.MetricDefinition{
		Name:       "m1",
		MetricType: interfaces.MetricTypeAtomic,
		UnitType:   "numUnit",
		Unit:       "none",
		ScopeType:  interfaces.ScopeTypeObjectType,
		ScopeRef:   "ot1",
		CalculationFormula: &interfaces.MetricCalculationFormula{
			Aggregation: interfaces.MetricAggregation{Property: "p", Aggr: interfaces.MetricAggrSum},
		},
	}
}

func Test_ValidateMetricRequest(t *testing.T) {
	Convey("Test ValidateMetricRequest\n", t, func() {
		ctx := context.Background()

		Convey("Failed with subgraph scope in strict mode\n", func() {
			r := validStrictCreateMetric()
			r.ScopeType = interfaces.ScopeTypeSubgraph
			err := ValidateMetricRequest(ctx, r, true)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with empty scope_ref in strict mode\n", func() {
			r := validStrictCreateMetric()
			r.ScopeRef = "   "
			err := ValidateMetricRequest(ctx, r, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with non-atomic metric_type in strict mode\n", func() {
			r := validStrictCreateMetric()
			r.MetricType = interfaces.MetricTypeDerived
			err := ValidateMetricRequest(ctx, r, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with missing aggregation property in strict mode\n", func() {
			r := validStrictCreateMetric()
			r.CalculationFormula.Aggregation.Property = ""
			err := ValidateMetricRequest(ctx, r, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Success with valid payload in strict mode\n", func() {
			r := validStrictCreateMetric()
			err := ValidateMetricRequest(ctx, r, true)
			So(err, ShouldBeNil)
		})

		Convey("Success with minimal fields in non-strict mode\n", func() {
			r := validStrictCreateMetric()
			r.ScopeType = ""
			r.ScopeRef = ""
			r.UnitType = ""
			r.Unit = ""
			r.MetricType = ""
			// Non-strict mode allows omitting formula; when calculation_formula is provided, aggregation.property and aggr are still required.
			r.CalculationFormula = nil
			err := ValidateMetricRequest(ctx, r, false)
			So(err, ShouldBeNil)
		})
	})
}

func Test_ValidateMetricRequests(t *testing.T) {
	Convey("Test ValidateMetricRequests\n", t, func() {
		ctx := context.Background()

		Convey("Failed with duplicate metric name in batch\n", func() {
			e := validStrictCreateMetric()
			e.Name = "dup"
			err := ValidateMetricRequests(ctx, []*interfaces.MetricDefinition{e, e}, true)
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_validateMetricCond(t *testing.T) {
	ctx := context.Background()

	Convey("validateMetricCond accepts and/or composite conditions\n", t, func() {
		err := validateMetricCond(ctx, &cond.CondCfg{
			Operation: cond.OperationAnd,
			SubConds: []*cond.CondCfg{
				{Field: "warehouse", Operation: cond.OperationIn, ValueOptCfg: cond.ValueOptCfg{Value: []any{"昆山成品仓"}}},
				{Field: "stock_status", Operation: cond.OperationEq, ValueOptCfg: cond.ValueOptCfg{Value: "可用"}},
			},
		})
		So(err, ShouldBeNil)
	})

	Convey("validateMetricCond accepts single atomic condition\n", t, func() {
		err := validateMetricCond(ctx, &cond.CondCfg{
			Field:       "status",
			Operation:   cond.OperationEq,
			ValueOptCfg: cond.ValueOptCfg{Value: "Active"},
		})
		So(err, ShouldBeNil)
	})

	Convey("validateMetricCond rejects knn in metric condition tree\n", t, func() {
		err := validateMetricCond(ctx, &cond.CondCfg{
			Operation: cond.OperationAnd,
			SubConds: []*cond.CondCfg{
				{Field: "warehouse", Operation: cond.OperationKNN, ValueOptCfg: cond.ValueOptCfg{Value: "x"}},
			},
		})
		So(err, ShouldNotBeNil)
		httpErr := err.(*rest.HTTPError)
		So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_UnsupportConditionOperation)
	})

	Convey("validateMetricCond rejects empty sub_conditions for and/or\n", t, func() {
		err := validateMetricCond(ctx, &cond.CondCfg{
			Operation: cond.OperationAnd,
			SubConds:  []*cond.CondCfg{},
		})
		So(err, ShouldNotBeNil)
		httpErr := err.(*rest.HTTPError)
		So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_InvalidParameter_Condition)
	})
}

func TestValidateMetricRequestsLocalizesInvalidParameterDetails(t *testing.T) {
	testCases := []struct {
		name     string
		language string
		want     string
	}{
		{"English", rest.AmericanEnglish, "Duplicate metric name in the request body: duplicate."},
		{"SimplifiedChinese", rest.SimplifiedChinese, "请求体中存在重复的指标名称：duplicate。"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			first := validStrictCreateMetric()
			first.Name = "duplicate"
			second := validStrictCreateMetric()
			second.Name = "duplicate"

			err := ValidateMetricRequests(
				rest.WithLanguage(context.Background(), testCase.language),
				[]*interfaces.MetricDefinition{first, second},
				true,
			)
			if err == nil {
				t.Fatal("ValidateMetricRequests() error = nil, want duplicate name error")
			}
			httpErr, ok := err.(*rest.HTTPError)
			if !ok {
				t.Fatalf("error type = %T, want *rest.HTTPError", err)
			}
			if got, ok := httpErr.BaseError.ErrorDetails.(string); !ok || got != testCase.want {
				t.Fatalf("error_details = %#v, want %q", httpErr.BaseError.ErrorDetails, testCase.want)
			}
		})
	}
}

func TestValidateMetricConditionDetailsRespectLanguage(t *testing.T) {
	testCases := []struct {
		name     string
		language string
		want     string
	}{
		{"English", rest.AmericanEnglish, "The == operation requires a single value."},
		{"SimplifiedChinese", rest.SimplifiedChinese, "== 操作要求单个值。"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateMetricCond(
				rest.WithLanguage(context.Background(), testCase.language),
				&cond.CondCfg{
					Field:     "field",
					Operation: cond.OperationEq,
					ValueOptCfg: cond.ValueOptCfg{
						Value: []any{"value"},
					},
				},
			)
			if err == nil {
				t.Fatal("validateMetricCond() error = nil, want invalid condition value error")
			}
			httpErr, ok := err.(*rest.HTTPError)
			if !ok {
				t.Fatalf("error type = %T, want *rest.HTTPError", err)
			}
			if got, ok := httpErr.BaseError.ErrorDetails.(string); !ok || got != testCase.want {
				t.Fatalf("error_details = %#v, want %q", httpErr.BaseError.ErrorDetails, testCase.want)
			}
		})
	}
}
