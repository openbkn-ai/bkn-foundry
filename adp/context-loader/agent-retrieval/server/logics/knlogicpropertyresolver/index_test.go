// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knlogicpropertyresolver

import (
	"context"
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
