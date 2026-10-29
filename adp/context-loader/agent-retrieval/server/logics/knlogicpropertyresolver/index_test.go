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

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func TestLogicPropertyErrorsLocalizeOwnedMessages(t *testing.T) {
	service := &knLogicPropertyResolverService{}
	for _, tt := range []struct {
		locale      common.Language
		missingWant string
		failedWant  string
	}{
		{locale: common.SimplifiedChinese, missingWant: "生成的动态参数缺少必需输入项。", failedWant: "生成动态参数失败。"},
		{locale: common.AmericanEnglish, missingWant: "Generated dynamic parameters are missing required input values.", failedWant: "Unable to generate dynamic parameters."},
	} {
		t.Run(string(tt.locale), func(t *testing.T) {
			ctx := common.SetLanguageToCtx(context.Background(), tt.locale)
			missing := service.buildMissingParamsError(ctx, []interfaces.MissingPropertyParams{{Property: "region"}}, nil)
			missingHTTPError := missing.(*httperrors.HTTPError)
			if got := missingHTTPError.ErrorDetails.(string); !strings.Contains(got, tt.missingWant) {
				t.Fatalf("missing error details = %q, want localized text %q", got, tt.missingWant)
			}

			failed := service.buildGenerationFailedError(ctx, []interfaces.MissingPropertyParams{{Property: "region", ErrorMsg: "provider timeout"}})
			failedHTTPError := failed.(*httperrors.HTTPError)
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
				{Name: "prop1", Type: interfaces.LogicPropertyTypeTool},
				{Name: "prop2", Type: interfaces.LogicPropertyTypeTool},
				{Name: "prop3", Type: interfaces.LogicPropertyTypeTool},
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
				{Name: "prop1", Type: interfaces.LogicPropertyTypeTool},
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
				"forecast_qty_sum": {"closestatus_title": "open"},
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
			Type: interfaces.LogicPropertyTypeTool,
			Parameters: []interfaces.PropertyParameter{
				{Name: "material_number", ValueFrom: "property", Value: "material_number"},
				{Name: "closestatus_title", ValueFrom: "input"},
				{Name: "instant", ValueFrom: "input", IfSystemGenerate: true},
				{Name: "fixed", ValueFrom: "const", Value: 1},
			},
		}

		convey.So(missingInputParams(property, map[string]any{}),
			convey.ShouldResemble, []string{"closestatus_title", "instant"})
		convey.So(missingInputParams(property, map[string]any{"closestatus_title": "open"}),
			convey.ShouldResemble, []string{"instant"})
		convey.So(missingInputParams(property, map[string]any{"closestatus_title": "open", "instant": true}),
			convey.ShouldBeEmpty)
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

func TestGenerateSinglePropertyParamsRejectsRemovedMetricType(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger)
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any())
	service := &knLogicPropertyResolverService{logger: mockLogger}
	_, _, err := service.generateSinglePropertyParams(
		context.Background(),
		&interfaces.ResolveLogicPropertiesRequest{},
		"legacy_metric_property",
		&interfaces.LogicPropertyDef{Name: "legacy_metric_property", Type: interfaces.LogicPropertyType("metric")},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "unknown property type: metric") {
		t.Fatalf("expected removed metric type to be rejected, got %v", err)
	}
}
