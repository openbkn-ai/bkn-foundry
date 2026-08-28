// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func Test_ValidateObjectType(t *testing.T) {
	Convey("Test ValidateObjectType\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid object type\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldBeNil)
		})

		Convey("Failed with invalid ID\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "_invalid_id",
					OTName: "object1",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with empty name\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_NullParameter_Name)
		})

		Convey("Failed with empty primary keys\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{},
					DisplayKey:  "prop1",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_NullParameter_PrimaryKeys)
		})

		Convey("Failed with invalid primary key type\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "float",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with empty display key\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_NullParameter_DisplayKey)
		})

		Convey("Failed with invalid data source type\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					DataSource: &interfaces.ResourceInfo{
						Type: "invalid_type",
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Success with data source type resource\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res-1",
					},
				},
			}
			err := ValidateObjectType(ctx, ot, false)
			So(err, ShouldBeNil)
		})

		Convey("Failed with primary key not in data properties\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop2"},
					DisplayKey:  "prop1",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with display key not in data properties\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop2",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with invalid display key type\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
						{
							Name:        "prop2",
							Type:        "binary",
							DisplayName: "prop2",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop2",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with invalid incremental key type\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
						{
							Name:        "prop2",
							Type:        "float",
							DisplayName: "prop2",
						},
					},
					PrimaryKeys:    []string{"prop1"},
					DisplayKey:     "prop1",
					IncrementalKey: "prop2",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with incremental key not in data properties\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys:    []string{"prop1"},
					DisplayKey:     "prop1",
					IncrementalKey: "prop2",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Success with valid incremental key\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
						{
							Name:        "prop2",
							Type:        "integer",
							DisplayName: "prop2",
						},
					},
					PrimaryKeys:    []string{"prop1"},
					DisplayKey:     "prop1",
					IncrementalKey: "prop2",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldBeNil)
		})

		Convey("Failed with invalid logic property type\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        "invalid_type",
							DisplayName: "logic1",
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with logic property type mismatch with data source\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        "metric",
							DisplayName: "logic1",
							DataSource: &interfaces.ResourceInfo{
								Type: "operator",
								ID:   "res1",
							},
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with logic property empty parameter name\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        "metric",
							DisplayName: "logic1",
							DataSource: &interfaces.ResourceInfo{
								Type: "metric",
								ID:   "metric-model-1",
							},
							Parameters: []interfaces.Parameter{
								{
									Name: "",
								},
							},
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with empty logic property type\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        "",
							DisplayName: "logic1",
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with logic property data source missing id\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        "metric",
							DisplayName: "logic1",
							DataSource: &interfaces.ResourceInfo{
								Type: "metric",
								ID:   "",
							},
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Success with valid tool logic property\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        interfaces.LOGIC_PROPERTY_TYPE_TOOL,
							DisplayName: "logic1",
							DataSource: &interfaces.ResourceInfo{
								Type:   interfaces.LOGIC_PROPERTY_TYPE_TOOL,
								BoxID:  "box1",
								ToolID: "tool1",
							},
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldBeNil)
		})

		Convey("Success with valid tool logic property result path\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        interfaces.LOGIC_PROPERTY_TYPE_TOOL,
							DisplayName: "logic1",
							DataSource: &interfaces.ResourceInfo{
								Type:       interfaces.LOGIC_PROPERTY_TYPE_TOOL,
								BoxID:      "box1",
								ToolID:     "tool1",
								ResultPath: "$.data.result",
							},
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldBeNil)
		})

		Convey("Failed with invalid tool logic property result path\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        interfaces.LOGIC_PROPERTY_TYPE_TOOL,
							DisplayName: "logic1",
							DataSource: &interfaces.ResourceInfo{
								Type:       interfaces.LOGIC_PROPERTY_TYPE_TOOL,
								BoxID:      "box1",
								ToolID:     "tool1",
								ResultPath: "$.data[",
							},
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with tool logic property missing tool id\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        interfaces.LOGIC_PROPERTY_TYPE_TOOL,
							DisplayName: "logic1",
							DataSource: &interfaces.ResourceInfo{
								Type:  interfaces.LOGIC_PROPERTY_TYPE_TOOL,
								BoxID: "box1",
							},
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Success with valid logic property metric type\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        "metric",
							DisplayName: "logic1",
							DataSource: &interfaces.ResourceInfo{
								Type: "metric",
								ID:   "metric-model-1",
							},
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldBeNil)
		})

		Convey("Success with metric logic property on resource object type\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "prop1",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "resource1",
					},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name:        "logic1",
							Type:        "metric",
							DisplayName: "logic1",
							DataSource: &interfaces.ResourceInfo{
								Type: "metric",
								ID:   "metric-model-1",
							},
						},
					},
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldBeNil)
		})

		Convey("Failed with invalid data property\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "",
							Type:        "string",
							DisplayName: "prop1",
						},
					},
					PrimaryKeys: []string{"prop1"},
					DisplayKey:  "prop1",
				},
			}
			err := ValidateObjectType(ctx, ot, true)
			So(err, ShouldNotBeNil)
		})
	})
}

func TestValidateObjectTypeLocalizesInvalidParameterDetails(t *testing.T) {
	testCases := []struct {
		name     string
		language string
		want     string
	}{
		{"English", rest.AmericanEnglish, "Object type customer uses unsupported data-source type table; only resource is supported."},
		{"SimplifiedChinese", rest.SimplifiedChinese, "对象类 customer 的数据来源类型 table 不支持，仅支持 resource。"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:       "customer",
					OTName:     "customer",
					DataSource: &interfaces.ResourceInfo{Type: "table"},
				},
			}
			err := ValidateObjectType(rest.WithLanguage(context.Background(), testCase.language), objectType, false)
			if err == nil {
				t.Fatal("ValidateObjectType() error = nil, want invalid parameter error")
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

func Test_ValidatePropertyName(t *testing.T) {
	Convey("Test ValidatePropertyName\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid property name\n", func() {
			err := ValidatePropertyName(ctx, "validProp")
			So(err, ShouldBeNil)
		})

		Convey("Failed with empty property name\n", func() {
			err := ValidatePropertyName(ctx, "")
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_NullParameter_PropertyName)
		})

		Convey("Failed with property name starting with underscore\n", func() {
			err := ValidatePropertyName(ctx, "_invalidProp")
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_ValidateDataProperties(t *testing.T) {
	Convey("Test ValidateDataProperties\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid data properties\n", func() {
			propertyNames := []string{"prop1", "prop2"}
			dataProperties := []*interfaces.DataProperty{
				{
					Name:        "prop1",
					Type:        "string",
					DisplayName: "prop1",
				},
				{
					Name:        "prop2",
					Type:        "integer",
					DisplayName: "prop2",
				},
			}
			err := ValidateDataProperties(ctx, propertyNames, dataProperties, true)
			So(err, ShouldBeNil)
		})

		Convey("Failed with length mismatch\n", func() {
			propertyNames := []string{"prop1"}
			dataProperties := []*interfaces.DataProperty{
				{
					Name:        "prop1",
					Type:        "string",
					DisplayName: "prop1",
				},
				{
					Name:        "prop2",
					Type:        "integer",
					DisplayName: "prop2",
				},
			}
			err := ValidateDataProperties(ctx, propertyNames, dataProperties, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with property not in URL\n", func() {
			propertyNames := []string{"prop1"}
			dataProperties := []*interfaces.DataProperty{
				{
					Name:        "prop2",
					Type:        "string",
					DisplayName: "prop2",
				},
			}
			err := ValidateDataProperties(ctx, propertyNames, dataProperties, true)
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_ValidateDataProperty(t *testing.T) {
	Convey("Test ValidateDataProperty\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid data property\n", func() {
			prop := &interfaces.DataProperty{
				Name:        "prop1",
				Type:        "string",
				DisplayName: "prop1",
			}
			err := ValidateDataProperty(ctx, prop, true)
			So(err, ShouldBeNil)
		})

		Convey("Failed with invalid property type\n", func() {
			prop := &interfaces.DataProperty{
				Name:        "prop1",
				Type:        "invalid_type",
				DisplayName: "prop1",
			}
			err := ValidateDataProperty(ctx, prop, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with empty mapped field name\n", func() {
			prop := &interfaces.DataProperty{
				Name:        "prop1",
				Type:        "string",
				DisplayName: "prop1",
				MappedField: &interfaces.Field{
					Name: "",
				},
			}
			err := ValidateDataProperty(ctx, prop, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with retired property index_config\n", func() {
			var prop interfaces.DataProperty
			err := json.Unmarshal([]byte(`{"name":"prop1","type":"string","display_name":"prop1","index_config":{}}`), &prop)
			So(err, ShouldBeNil)
			So(ValidateDataProperty(ctx, &prop, true), ShouldNotBeNil)
		})
	})
}
