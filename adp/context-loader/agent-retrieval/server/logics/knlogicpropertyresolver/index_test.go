// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knlogicpropertyresolver

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func TestLogicPropertyErrorsLocalizeOwnedMessages(t *testing.T) {
	service := &knLogicPropertyResolverService{}
	for _, tt := range []struct {
		locale      string
		missingWant string
		failedWant  string
	}{
		{locale: "zh-CN", missingWant: "生成的动态参数缺少必需输入项。", failedWant: "生成动态参数失败。"},
		{locale: "en-US", missingWant: "Generated dynamic parameters are missing required input values.", failedWant: "Unable to generate dynamic parameters."},
	} {
		t.Run(tt.locale, func(t *testing.T) {
			ctx := common.SetLanguageToCtx(context.Background(), tt.locale)
			missing := service.buildMissingParamsError(ctx, []interfaces.MissingPropertyParams{{Property: "region"}}, nil)
			missingHTTPError := missing.(*infraerrors.HTTPError)
			if got := missingHTTPError.ErrorDetails.(string); !strings.Contains(got, tt.missingWant) {
				t.Fatalf("missing error details = %q, want localized text %q", got, tt.missingWant)
			}

			failed := service.buildGenerationFailedError(ctx, []interfaces.MissingPropertyParams{{Property: "region", ErrorMsg: "provider timeout"}})
			failedHTTPError := failed.(*infraerrors.HTTPError)
			if got := failedHTTPError.ErrorDetails.(string); !strings.Contains(got, tt.failedWant) {
				t.Fatalf("generation error details = %q, want localized text %q", got, tt.failedWant)
			}
			if got := failedHTTPError.ErrorDetails.(string); !strings.Contains(got, "DYNAMIC_PARAMS_GENERATION_FAILED") {
				t.Fatalf("generation error details lost stable discriminator: %q", got)
			}
			if got := failedHTTPError.ErrorDetails.(string); !strings.Contains(got, "provider timeout") {
				t.Fatalf("generation error details lost upstream diagnostic: %q", got)
			}
		})
	}
}

// TestValidateRequest_Success test validateRequest success scenario.
func TestValidateRequest_Success(t *testing.T) {
	convey.Convey("TestValidateRequest_Success", t, func() {
		service := &knLogicPropertyResolverService{}

		req := &interfaces.ResolveLogicPropertiesRequest{
			KnID:  "kn-001",
			OtID:  "ot-001",
			Query: "测试查询",
			InstanceIdentities: []map[string]interface{}{
				{"id": "obj-001"},
			},
			Properties: []string{"prop1", "prop2"},
		}

		err := service.validateRequest(req)
		convey.So(err, convey.ShouldBeNil)
	})
}

// TestValidateRequest_MissingKnID test validateRequest missing KnID.
func TestValidateRequest_MissingKnID(t *testing.T) {
	convey.Convey("TestValidateRequest_MissingKnID", t, func() {
		service := &knLogicPropertyResolverService{}

		req := &interfaces.ResolveLogicPropertiesRequest{
			KnID:  "",
			OtID:  "ot-001",
			Query: "测试查询",
			InstanceIdentities: []map[string]interface{}{
				{"id": "obj-001"},
			},
			Properties: []string{"prop1"},
		}

		err := service.validateRequest(req)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "kn_id")
	})
}

// TestValidateRequest_MissingOtID test validateRequest missing OtID.
func TestValidateRequest_MissingOtID(t *testing.T) {
	convey.Convey("TestValidateRequest_MissingOtID", t, func() {
		service := &knLogicPropertyResolverService{}

		req := &interfaces.ResolveLogicPropertiesRequest{
			KnID:  "kn-001",
			OtID:  "",
			Query: "测试查询",
			InstanceIdentities: []map[string]interface{}{
				{"id": "obj-001"},
			},
			Properties: []string{"prop1"},
		}

		err := service.validateRequest(req)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "ot_id")
	})
}

// TestValidateRequest_MissingQuery test validateRequest missing Query.
func TestValidateRequest_MissingQuery(t *testing.T) {
	convey.Convey("TestValidateRequest_MissingQuery", t, func() {
		service := &knLogicPropertyResolverService{}

		req := &interfaces.ResolveLogicPropertiesRequest{
			KnID:  "kn-001",
			OtID:  "ot-001",
			Query: "",
			InstanceIdentities: []map[string]interface{}{
				{"id": "obj-001"},
			},
			Properties: []string{"prop1"},
		}

		err := service.validateRequest(req)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "query")
	})
}

// TestValidateRequest_EmptyInstanceIdentities Test validateRequest empty InstanceIdentities.
func TestValidateRequest_EmptyInstanceIdentities(t *testing.T) {
	convey.Convey("TestValidateRequest_EmptyInstanceIdentities", t, func() {
		service := &knLogicPropertyResolverService{}

		req := &interfaces.ResolveLogicPropertiesRequest{
			KnID:               "kn-001",
			OtID:               "ot-001",
			Query:              "测试查询",
			InstanceIdentities: []map[string]interface{}{},
			Properties:         []string{"prop1"},
		}

		err := service.validateRequest(req)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "_instance_identities")
	})
}

// TestValidateRequest_EmptyProperties Test validateRequest empty Properties.
func TestValidateRequest_EmptyProperties(t *testing.T) {
	convey.Convey("TestValidateRequest_EmptyProperties", t, func() {
		service := &knLogicPropertyResolverService{}

		req := &interfaces.ResolveLogicPropertiesRequest{
			KnID:  "kn-001",
			OtID:  "ot-001",
			Query: "测试查询",
			InstanceIdentities: []map[string]interface{}{
				{"id": "obj-001"},
			},
			Properties: []string{},
		}

		err := service.validateRequest(req)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "properties")
	})
}

// TestValidateMetricParams_Success_Instant test validateMetricParams instant query is successful.
func TestValidateMetricParams_Success_Instant(t *testing.T) {
	convey.Convey("TestValidateMetricParams_Success_Instant", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knLogicPropertyResolverService{
			logger: mockLogger,
		}

		property := &interfaces.LogicPropertyDef{
			Name: "test_metric",
			Type: interfaces.LogicPropertyTypeMetric,
		}

		params := map[string]any{
			"instant": true,
			"start":   int64(1704067200000), // 2024-01-01
			"end":     int64(1706745600000), // 2024-02-01
		}

		ctx := context.Background()
		err := service.validateMetricParams(ctx, property, params)
		convey.So(err, convey.ShouldBeNil)
	})
}

// TestValidateMetricParams_Success_Trend Test validateMetricParams trend query successful.
func TestValidateMetricParams_Success_Trend(t *testing.T) {
	convey.Convey("TestValidateMetricParams_Success_Trend", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knLogicPropertyResolverService{
			logger: mockLogger,
		}

		property := &interfaces.LogicPropertyDef{
			Name: "test_metric",
			Type: interfaces.LogicPropertyTypeMetric,
		}

		params := map[string]any{
			"instant": false,
			"start":   int64(1704067200000),
			"end":     int64(1706745600000),
			"step":    "day",
		}

		ctx := context.Background()
		err := service.validateMetricParams(ctx, property, params)
		convey.So(err, convey.ShouldBeNil)
	})
}

// TestValidateMetricParams_MissingStart test validateMetricParams missing start.
func TestValidateMetricParams_MissingStart(t *testing.T) {
	convey.Convey("TestValidateMetricParams_MissingStart", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knLogicPropertyResolverService{
			logger: mockLogger,
		}

		property := &interfaces.LogicPropertyDef{
			Name: "test_metric",
			Type: interfaces.LogicPropertyTypeMetric,
		}

		params := map[string]any{
			"instant": true,
			"end":     int64(1706745600000),
		}

		ctx := context.Background()
		err := service.validateMetricParams(ctx, property, params)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "start")
	})
}

// TestValidateMetricParams_MissingEnd test validateMetricParams missing end.
func TestValidateMetricParams_MissingEnd(t *testing.T) {
	convey.Convey("TestValidateMetricParams_MissingEnd", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knLogicPropertyResolverService{
			logger: mockLogger,
		}

		property := &interfaces.LogicPropertyDef{
			Name: "test_metric",
			Type: interfaces.LogicPropertyTypeMetric,
		}

		params := map[string]any{
			"instant": true,
			"start":   int64(1704067200000),
		}

		ctx := context.Background()
		err := service.validateMetricParams(ctx, property, params)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "end")
	})
}

// TestValidateMetricParams_InstantWithStep tests instant=true but has step error.
func TestValidateMetricParams_InstantWithStep(t *testing.T) {
	convey.Convey("TestValidateMetricParams_InstantWithStep", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knLogicPropertyResolverService{
			logger: mockLogger,
		}

		property := &interfaces.LogicPropertyDef{
			Name: "test_metric",
			Type: interfaces.LogicPropertyTypeMetric,
		}

		params := map[string]any{
			"instant": true,
			"start":   int64(1704067200000),
			"end":     int64(1706745600000),
			"step":    "day", // instant=true should not have step.
		}

		ctx := context.Background()
		err := service.validateMetricParams(ctx, property, params)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "step")
	})
}

// TestValidateMetricParams_TrendWithoutStep tests instant=false but no step error.
func TestValidateMetricParams_TrendWithoutStep(t *testing.T) {
	convey.Convey("TestValidateMetricParams_TrendWithoutStep", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knLogicPropertyResolverService{
			logger: mockLogger,
		}

		property := &interfaces.LogicPropertyDef{
			Name: "test_metric",
			Type: interfaces.LogicPropertyTypeMetric,
		}

		params := map[string]any{
			"instant": false,
			"start":   int64(1704067200000),
			"end":     int64(1706745600000),
			// Missing step.
		}

		ctx := context.Background()
		err := service.validateMetricParams(ctx, property, params)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "step")
	})
}

// TestValidateMetricParams_InvalidStep tests for invalid step values.
func TestValidateMetricParams_InvalidStep(t *testing.T) {
	convey.Convey("TestValidateMetricParams_InvalidStep", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knLogicPropertyResolverService{
			logger: mockLogger,
		}

		property := &interfaces.LogicPropertyDef{
			Name: "test_metric",
			Type: interfaces.LogicPropertyTypeMetric,
		}

		params := map[string]any{
			"instant": false,
			"start":   int64(1704067200000),
			"end":     int64(1706745600000),
			"step":    "invalid_step",
		}

		ctx := context.Background()
		err := service.validateMetricParams(ctx, property, params)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "invalid step")
	})
}

// TestValidateTimestamp_Int64 tests the timestamp of type int64.
func TestValidateTimestamp_Int64(t *testing.T) {
	convey.Convey("TestValidateTimestamp_Int64", t, func() {
		service := &knLogicPropertyResolverService{}
		ctx := context.Background()

		// Valid timestamp.
		err := service.validateTimestamp(ctx, int64(1704067200000), "start", "test_prop")
		convey.So(err, convey.ShouldBeNil)

		// Invalid timestamp (too small)
		err = service.validateTimestamp(ctx, int64(100000000000), "start", "test_prop")
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestValidateTimestamp_Float64 tests float64 type timestamp.
func TestValidateTimestamp_Float64(t *testing.T) {
	convey.Convey("TestValidateTimestamp_Float64", t, func() {
		service := &knLogicPropertyResolverService{}
		ctx := context.Background()

		// Valid timestamp.
		err := service.validateTimestamp(ctx, float64(1704067200000), "start", "test_prop")
		convey.So(err, convey.ShouldBeNil)
	})
}

// TestValidateTimestamp_InvalidType Test timestamp of invalid type.
func TestValidateTimestamp_InvalidType(t *testing.T) {
	convey.Convey("TestValidateTimestamp_InvalidType", t, func() {
		service := &knLogicPropertyResolverService{}
		ctx := context.Background()

		// Invalid type.
		err := service.validateTimestamp(ctx, "not_a_number", "start", "test_prop")
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "must be a number")
	})
}

// TestExtractLogicProperties_Success test extractLogicProperties success.
func TestExtractLogicProperties_Success(t *testing.T) {
	convey.Convey("TestExtractLogicProperties_Success", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knLogicPropertyResolverService{
			logger: mockLogger,
		}

		objectType := &interfaces.ObjectType{
			ID: "ot-001",
			LogicProperties: []*interfaces.LogicPropertyDef{
				{Name: "prop1", Type: interfaces.LogicPropertyTypeMetric},
				{Name: "prop2", Type: interfaces.LogicPropertyTypeTool},
				{Name: "prop3", Type: interfaces.LogicPropertyTypeMetric},
			},
		}

		ctx := context.Background()
		result, err := service.extractLogicProperties(ctx, objectType, []string{"prop1", "prop2"})
		convey.So(err, convey.ShouldBeNil)
		convey.So(len(result), convey.ShouldEqual, 2)
		convey.So(result["prop1"], convey.ShouldNotBeNil)
		convey.So(result["prop2"], convey.ShouldNotBeNil)
	})
}

// TestExtractLogicProperties_NoLogicProperties The test object type has no logical properties.
func TestExtractLogicProperties_NoLogicProperties(t *testing.T) {
	convey.Convey("TestExtractLogicProperties_NoLogicProperties", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knLogicPropertyResolverService{
			logger: mockLogger,
		}

		objectType := &interfaces.ObjectType{
			ID:              "ot-001",
			LogicProperties: []*interfaces.LogicPropertyDef{},
		}

		ctx := context.Background()
		_, err := service.extractLogicProperties(ctx, objectType, []string{"prop1"})
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestExtractLogicProperties_PropertyNotFound The property requested by the test does not exist.
func TestExtractLogicProperties_PropertyNotFound(t *testing.T) {
	convey.Convey("TestExtractLogicProperties_PropertyNotFound", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knLogicPropertyResolverService{
			logger: mockLogger,
		}

		objectType := &interfaces.ObjectType{
			ID: "ot-001",
			LogicProperties: []*interfaces.LogicPropertyDef{
				{Name: "prop1", Type: interfaces.LogicPropertyTypeMetric},
			},
		}

		ctx := context.Background()
		_, err := service.extractLogicProperties(ctx, objectType, []string{"prop1", "nonexistent_prop"})
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "nonexistent_prop")
	})
}

// A caller that supplies every input parameter should not have to invent a
// question for a model that is no longer asked anything.
func TestValidateRequestAcceptsSuppliedParametersInsteadOfQuery(t *testing.T) {
	convey.Convey("TestValidateRequestAcceptsSuppliedParametersInsteadOfQuery", t, func() {
		service := &knLogicPropertyResolverService{}

		req := &interfaces.ResolveLogicPropertiesRequest{
			KnID:               "kn-001",
			OtID:               "ot-001",
			InstanceIdentities: []map[string]interface{}{{"id": "obj-001"}},
			Properties:         []string{"forecast_qty_sum"},
			DynamicParams: map[string]map[string]any{
				"forecast_qty_sum": {"closestatus_title": "未关闭"},
			},
		}
		convey.So(service.validateRequest(req), convey.ShouldBeNil)

		// One property supplied, another not: the question is still needed.
		req.Properties = append(req.Properties, "open_forecast_count")
		err := service.validateRequest(req)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "query")
	})
}

// Only value_from=input is asked of a caller: property is read by the server
// from the instance and const is fixed at modelling time, so supplying the
// input ones is enough to skip generation.
func TestMissingInputParamsCountsOnlyWhatACallerSupplies(t *testing.T) {
	convey.Convey("TestMissingInputParamsCountsOnlyWhatACallerSupplies", t, func() {
		property := &interfaces.LogicPropertyDef{
			Name: "forecast_qty_sum",
			Type: interfaces.LogicPropertyTypeMetric,
			Parameters: []interfaces.PropertyParameter{
				{Name: "material_number", ValueFrom: "property", Value: "material_number"},
				{Name: "closestatus_title", ValueFrom: "input"},
				{Name: "instant", ValueFrom: "input", IfSystemGenerate: true},
				{Name: "fixed", ValueFrom: "const", Value: 1},
			},
		}

		convey.So(missingInputParams(property, map[string]any{}),
			convey.ShouldResemble, []string{"closestatus_title", "instant"})
		convey.So(missingInputParams(property, map[string]any{"closestatus_title": "未关闭"}),
			convey.ShouldResemble, []string{"instant"})
		convey.So(missingInputParams(property, map[string]any{"closestatus_title": "未关闭", "instant": true}),
			convey.ShouldBeEmpty)
	})
}

// Skipping generation must not skip validation: a caller that pins a broken
// time window hears about it here, not from the engine.
func TestResolveSinglePropertyParamsValidatesWhatTheCallerSupplied(t *testing.T) {
	convey.Convey("TestResolveSinglePropertyParamsValidatesWhatTheCallerSupplied", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		service := &knLogicPropertyResolverService{logger: mockLogger}

		property := &interfaces.LogicPropertyDef{
			Name: "forecast_qty_sum",
			Type: interfaces.LogicPropertyTypeMetric,
			Parameters: []interfaces.PropertyParameter{
				{Name: "instant", ValueFrom: "input", IfSystemGenerate: true},
				{Name: "start", ValueFrom: "input", IfSystemGenerate: true},
				{Name: "end", ValueFrom: "input", IfSystemGenerate: true},
			},
		}
		req := &interfaces.ResolveLogicPropertiesRequest{
			KnID: "kn-001", OtID: "ot-001",
			Properties: []string{"forecast_qty_sum"},
			DynamicParams: map[string]map[string]any{
				"forecast_qty_sum": {
					"instant": true,
					"start":   int64(1704067200000),
					"end":     int64(1706745600000),
				},
			},
		}

		params, source, missing, err := service.resolveSinglePropertyParams(
			context.Background(), req, "forecast_qty_sum", property, nil)
		convey.So(err, convey.ShouldBeNil)
		convey.So(missing, convey.ShouldBeNil)
		convey.So(source, convey.ShouldEqual, paramsSourceCaller)
		convey.So(params["start"], convey.ShouldEqual, int64(1704067200000))

		// The same path refuses a window the engine would have rejected later.
		req.DynamicParams["forecast_qty_sum"]["start"] = int64(1)
		_, _, _, err = service.resolveSinglePropertyParams(
			context.Background(), req, "forecast_qty_sum", property, nil)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// Tool properties had no validation at all: whatever a model produced, or a
// caller now supplies, went straight to the engine.
func TestValidateToolParamsChecksDeclaredTypes(t *testing.T) {
	convey.Convey("TestValidateToolParamsChecksDeclaredTypes", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		service := &knLogicPropertyResolverService{logger: mockLogger}

		property := &interfaces.LogicPropertyDef{
			Name: "exchange_rate",
			Type: interfaces.LogicPropertyTypeTool,
			Parameters: []interfaces.PropertyParameter{
				{Name: "target_currency", Type: "string", ValueFrom: "input"},
				{Name: "rounding", Type: "integer", ValueFrom: "input"},
				{Name: "live", Type: "boolean", ValueFrom: "input"},
			},
		}
		ctx := context.Background()

		convey.So(service.validateToolParams(ctx, property, map[string]any{
			"target_currency": "USD", "rounding": float64(2), "live": true,
		}), convey.ShouldBeNil)

		// A number where a string is declared is what a model gets wrong.
		convey.So(service.validateToolParams(ctx, property, map[string]any{
			"target_currency": 1,
		}), convey.ShouldNotBeNil)

		// A tool's own schema may name arguments the property does not
		// declare, and those travelled long before this check existed.
		convey.So(service.validateToolParams(ctx, property, map[string]any{
			"undeclared": "value",
		}), convey.ShouldBeNil)
	})
}

// A business filter is something only the caller knows, so leaving one out
// must not be read as "complete". Validation cannot see it: it checks the
// metric time window and nothing else, which is how a set missing
// closestatus_title used to reach the engine as if the caller had meant it.
func TestResolveSinglePropertyParamsGeneratesWhenABusinessInputIsMissing(t *testing.T) {
	convey.Convey("TestResolveSinglePropertyParamsGeneratesWhenABusinessInputIsMissing", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		service := &knLogicPropertyResolverService{logger: mockLogger}

		property := &interfaces.LogicPropertyDef{
			Name: "open_forecast_count",
			Type: interfaces.LogicPropertyTypeMetric,
			Parameters: []interfaces.PropertyParameter{
				{Name: "material_number", ValueFrom: "property", Value: "material_number"},
				{Name: "closestatus_title", ValueFrom: "input"},
				{Name: "instant", ValueFrom: "input", IfSystemGenerate: true},
				{Name: "start", ValueFrom: "input", IfSystemGenerate: true},
				{Name: "end", ValueFrom: "input", IfSystemGenerate: true},
			},
		}
		// Everything the time window needs, and no closestatus_title. The set
		// validates, so only the business-input check keeps it out of the
		// caller path.
		req := &interfaces.ResolveLogicPropertiesRequest{
			KnID: "kn-001", OtID: "ot-001",
			Properties: []string{"open_forecast_count"},
			DynamicParams: map[string]map[string]any{
				"open_forecast_count": {
					"instant": true,
					"start":   int64(1704067200000),
					"end":     int64(1706745600000),
				},
			},
		}

		// No query, so generation cannot run either: the error says what the
		// caller has to supply rather than pretending the set was complete.
		_, source, _, err := service.resolveSinglePropertyParams(
			context.Background(), req, "open_forecast_count", property, nil)
		convey.So(source, convey.ShouldBeEmpty)
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "closestatus_title")

		var callerErr callerParamError
		convey.So(stderrors.As(err, &callerErr), convey.ShouldBeTrue)
	})
}

// A value the caller got wrong is a 400, not a generation failure: it should
// name the parameter rather than raise an alert about the model.
func TestResolveSinglePropertyParamsMarksCallerMistakes(t *testing.T) {
	convey.Convey("TestResolveSinglePropertyParamsMarksCallerMistakes", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		service := &knLogicPropertyResolverService{logger: mockLogger}

		property := &interfaces.LogicPropertyDef{
			Name: "forecast_qty_sum",
			Type: interfaces.LogicPropertyTypeMetric,
			Parameters: []interfaces.PropertyParameter{
				{Name: "instant", ValueFrom: "input", IfSystemGenerate: true},
				{Name: "start", ValueFrom: "input", IfSystemGenerate: true},
				{Name: "end", ValueFrom: "input", IfSystemGenerate: true},
			},
		}
		req := &interfaces.ResolveLogicPropertiesRequest{
			KnID: "kn-001", OtID: "ot-001",
			Query:      "任意问题",
			Properties: []string{"forecast_qty_sum"},
			DynamicParams: map[string]map[string]any{
				"forecast_qty_sum": {"instant": true, "start": int64(1), "end": int64(1706745600000)},
			},
		}

		_, _, _, err := service.resolveSinglePropertyParams(
			context.Background(), req, "forecast_qty_sum", property, nil)
		convey.So(err, convey.ShouldNotBeNil)
		var callerErr callerParamError
		convey.So(stderrors.As(err, &callerErr), convey.ShouldBeTrue)
	})
}

// if_system_generate is optional and a definition may leave it off. Without a
// fallback, an instant query on such a definition is impossible to supply:
// step is demanded as a business input, and validation refuses it as soon as
// it is given.
func TestMissingCallerInputParamsRecognisesTheTimeWindowWithoutTheFlag(t *testing.T) {
	convey.Convey("TestMissingCallerInputParamsRecognisesTheTimeWindowWithoutTheFlag", t, func() {
		property := &interfaces.LogicPropertyDef{
			Name: "product_total_count",
			Type: interfaces.LogicPropertyTypeMetric,
			Parameters: []interfaces.PropertyParameter{
				{Name: "instant", Type: "boolean", ValueFrom: "input"},
				{Name: "start", Type: "integer", ValueFrom: "input"},
				{Name: "end", Type: "integer", ValueFrom: "input"},
				{Name: "step", Type: "string", ValueFrom: "input"},
			},
		}
		supplied := map[string]any{"instant": true, "start": int64(1704067200000), "end": int64(1706745600000)}
		convey.So(missingCallerInputParams(property, supplied), convey.ShouldBeEmpty)

		// A business filter on the same property is still the caller's to give.
		property.Parameters = append(property.Parameters,
			interfaces.PropertyParameter{Name: "closestatus_title", Type: "string", ValueFrom: "input"})
		convey.So(missingCallerInputParams(property, supplied),
			convey.ShouldResemble, []string{"closestatus_title"})

		// A tool property has no time window, so nothing is excused there.
		tool := &interfaces.LogicPropertyDef{
			Name: "exchange_rate",
			Type: interfaces.LogicPropertyTypeTool,
			Parameters: []interfaces.PropertyParameter{
				{Name: "start", Type: "integer", ValueFrom: "input"},
			},
		}
		convey.So(missingCallerInputParams(tool, map[string]any{}),
			convey.ShouldResemble, []string{"start"})
	})
}
