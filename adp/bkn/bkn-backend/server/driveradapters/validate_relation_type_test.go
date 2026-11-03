// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func Test_ValidateRelationType(t *testing.T) {
	Convey("Test ValidateRelationType\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid relation type\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:               "rt1",
					RTName:             "relation1",
					SourceObjectTypeID: "ot1",
					TargetObjectTypeID: "ot2",
					Type:               interfaces.RELATION_TYPE_DIRECT,
					MappingRules: []interfaces.Mapping{
						{
							SourceProp: interfaces.SimpleProperty{Name: "prop1"},
							TargetProp: interfaces.SimpleProperty{Name: "prop2"},
						},
					},
				},
				CommonInfo: interfaces.CommonInfo{
					Tags:          []string{"tag1", "tag2", "tag3"},
					Comment:       "test comment",
					Icon:          "icon1",
					Color:         "color1",
					BKNRawContent: "bkn1",
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldBeNil)
		})

		Convey("Success with strictMode false despite empty endpoints and mapping\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:               "rt-relaxed",
					RTName:             "relation_relaxed",
					SourceObjectTypeID: "",
					TargetObjectTypeID: "",
					Type:               "",
					MappingRules:       nil,
				},
			}
			err := ValidateRelationType(ctx, rt, false)
			So(err, ShouldBeNil)
		})

		Convey("Failed with invalid ID\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:   "_invalid_id",
					RTName: "relation1",
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with empty name\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:   "rt1",
					RTName: "",
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldNotBeNil)
			httpErr := err.(*httperrors.HTTPError)
			So(httpErr.ErrorCode, ShouldEqual, berrors.BknBackend_RelationType_NullParameter_Name)
		})

		Convey("Failed with invalid type\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:   "rt1",
					RTName: "relation1",
					Type:   "invalid_type",
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with mapping rules but empty type\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:   "rt1",
					RTName: "relation1",
					Type:   "",
					MappingRules: []map[string]any{
						{
							"source_property": map[string]string{"name": "prop1"},
							"target_property": map[string]string{"name": "prop2"},
						},
					},
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Success with direct mapping rules\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:               "rt1",
					RTName:             "relation1",
					SourceObjectTypeID: "ot1",
					TargetObjectTypeID: "ot2",
					Type:               interfaces.RELATION_TYPE_DIRECT,
					MappingRules: []map[string]any{
						{
							"source_property": map[string]string{"name": "prop1"},
							"target_property": map[string]string{"name": "prop2"},
						},
					},
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldBeNil)
		})

		Convey("Success with indirect relation backed by a resource\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:               "rt-data-view",
					RTName:             "data view relation",
					SourceObjectTypeID: "ot1",
					TargetObjectTypeID: "ot2",
					Type:               interfaces.RELATION_TYPE_INDIRECT,
					MappingRules: map[string]any{
						"backing_data_source":  map[string]any{"id": "resource-1", "type": "resource"},
						"source_mapping_rules": []map[string]any{{"source_property": map[string]any{"name": "source_id"}, "target_property": map[string]any{"name": "resource_source_id"}}},
						"target_mapping_rules": []map[string]any{{"source_property": map[string]any{"name": "resource_target_id"}, "target_property": map[string]any{"name": "target_id"}}},
					},
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldBeNil)
			_, ok := rt.MappingRules.(*interfaces.InDirectMapping)
			So(ok, ShouldBeTrue)
		})

		Convey("Rejects legacy data_view relation type\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID: "rt-legacy-type", RTName: "legacy type", Type: "data_view",
					MappingRules: map[string]any{
						"backing_data_source":  map[string]any{"id": "resource-1", "type": "resource"},
						"source_mapping_rules": []map[string]any{},
						"target_mapping_rules": []map[string]any{},
					},
				},
			}
			err := ValidateRelationType(ctx, rt, false)
			So(err, ShouldNotBeNil)
		})

		Convey("Rejects legacy data_view backing data source\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID: "rt-legacy-backing", RTName: "legacy backing", Type: interfaces.RELATION_TYPE_INDIRECT,
					MappingRules: map[string]any{"backing_data_source": map[string]any{"id": "view-1", "type": "data_view"}},
				},
			}
			err := ValidateRelationType(ctx, rt, false)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with direct mapping rules empty source prop\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:   "rt1",
					RTName: "relation1",
					Type:   interfaces.RELATION_TYPE_DIRECT,
					MappingRules: []map[string]any{
						{
							"source_property": map[string]string{"name": ""},
							"target_property": map[string]string{"name": "prop2"},
						},
					},
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with direct mapping rules empty target prop\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:   "rt1",
					RTName: "relation1",
					Type:   interfaces.RELATION_TYPE_DIRECT,
					MappingRules: []map[string]any{
						{
							"source_property": map[string]string{"name": "prop1"},
							"target_property": map[string]string{"name": ""},
						},
					},
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with direct mapping rules invalid format\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:         "rt1",
					RTName:       "relation1",
					Type:         interfaces.RELATION_TYPE_DIRECT,
					MappingRules: "invalid_format",
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed with invalid type in validateMappingRules\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:   "rt1",
					RTName: "relation1",
					Type:   "invalid_type",
					MappingRules: []map[string]any{
						{
							"source_property": map[string]string{"name": "prop1"},
							"target_property": map[string]string{"name": "prop2"},
						},
					},
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Success with filtered_cross_join mapping rules\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:               "rt-fcj",
					RTName:             "fcj",
					SourceObjectTypeID: "ot1",
					TargetObjectTypeID: "ot2",
					Type:               interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN,
					MappingRules: map[string]any{
						"source_condition": map[string]any{"field": "a", "operation": "==", "value": 1},
						"target_condition": map[string]any{"field": "b", "operation": "==", "value": 2},
					},
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldBeNil)
			_, ok := rt.MappingRules.(*interfaces.FilteredCrossJoinMapping)
			So(ok, ShouldBeTrue)
		})

		Convey("Success with filtered_cross_join omitting target_condition (optional)\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:               "rt-fcj2",
					RTName:             "fcj2",
					SourceObjectTypeID: "ot1",
					TargetObjectTypeID: "ot2",
					Type:               interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN,
					MappingRules: map[string]any{
						"source_condition": map[string]any{"field": "a", "operation": "==", "value": 1},
					},
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldBeNil)
		})

		Convey("Success with filtered_cross_join empty mapping (both sides unconstrained)\n", func() {
			rt := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:               "rt-fcj3",
					RTName:             "fcj3",
					SourceObjectTypeID: "ot1",
					TargetObjectTypeID: "ot2",
					Type:               interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN,
					MappingRules:       map[string]any{},
				},
			}
			err := ValidateRelationType(ctx, rt, true)
			So(err, ShouldBeNil)
		})
	})
}

func TestValidateRelationTypeLocalizesInvalidParameterDetails(t *testing.T) {
	testCases := []struct {
		name     string
		language i18n.Language
		want     string
	}{
		{
			name:     "English",
			language: i18n.AmericanEnglish,
			want:     "Relation type must be direct, indirect, or filtered_cross_join; received unsupported.",
		},
		{
			name:     "SimplifiedChinese",
			language: i18n.SimplifiedChinese,
			want:     "关系类型仅支持 direct、indirect 和 filtered_cross_join，当前为 unsupported。",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := i18n.WithLanguage(context.Background(), testCase.language)
			relationType := &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:   "rt1",
					RTName: "relation1",
					Type:   "unsupported",
				},
			}

			err := ValidateRelationType(ctx, relationType, true)
			if err == nil {
				t.Fatal("ValidateRelationType() error = nil, want invalid parameter error")
			}
			httpErr, ok := err.(*httperrors.HTTPError)
			if !ok {
				t.Fatalf("error type = %T, want *httperrors.HTTPError", err)
			}
			if got, ok := httpErr.ErrorDetails.(string); !ok || got != testCase.want {
				t.Fatalf("error_details = %#v, want %q", httpErr.ErrorDetails, testCase.want)
			}
		})
	}
}

func Test_ValidateRelationTypes(t *testing.T) {
	Convey("Test ValidateRelationTypes\n", t, func() {
		ctx := context.Background()
		knID := "kn1"

		makeRT := func(id, name string) *interfaces.RelationType {
			return &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID:               id,
					RTName:             name,
					SourceObjectTypeID: "ot1",
					TargetObjectTypeID: "ot2",
					Type:               interfaces.RELATION_TYPE_DIRECT,
					MappingRules: []interfaces.Mapping{
						{
							SourceProp: interfaces.SimpleProperty{Name: "prop1"},
							TargetProp: interfaces.SimpleProperty{Name: "prop2"},
						},
					},
				},
			}
		}

		Convey("Success with two relation types having the same name\n", func() {
			relationTypes := []*interfaces.RelationType{
				makeRT("rt1", "same_name"),
				makeRT("rt2", "same_name"),
			}
			err := ValidateRelationTypes(ctx, knID, relationTypes, true)
			So(err, ShouldBeNil)
			So(relationTypes[0].KNID, ShouldEqual, knID)
			So(relationTypes[1].KNID, ShouldEqual, knID)
		})

		Convey("Failed when two relation types have the same ID\n", func() {
			relationTypes := []*interfaces.RelationType{
				makeRT("rt1", "name1"),
				makeRT("rt1", "name2"),
			}
			err := ValidateRelationTypes(ctx, knID, relationTypes, true)
			So(err, ShouldNotBeNil)
			httpErr := err.(*httperrors.HTTPError)
			So(httpErr.ErrorCode, ShouldEqual, berrors.BknBackend_RelationType_Duplicated_IDInFile)
		})

		Convey("Success with multiple distinct relation types\n", func() {
			relationTypes := []*interfaces.RelationType{
				makeRT("rt1", "name1"),
				makeRT("rt2", "name2"),
				makeRT("rt3", "name1"),
			}
			err := ValidateRelationTypes(ctx, knID, relationTypes, true)
			So(err, ShouldBeNil)
		})
	})
}

func TestRelationTypeValidationDetailsRespectLanguage(t *testing.T) {
	testCases := []struct {
		name     string
		language i18n.Language
		want     string
	}{
		{"English", i18n.AmericanEnglish, "Relation type module type must be relation_type."},
		{"SimplifiedChinese", i18n.SimplifiedChinese, "关系类模块类型必须为 relation_type。"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateRelationTypes(
				i18n.WithLanguage(context.Background(), testCase.language),
				"kn-1",
				[]*interfaces.RelationType{{ModuleType: "object_type"}},
				true,
			)
			if err == nil {
				t.Fatal("ValidateRelationTypes() error = nil, want invalid module type error")
			}
			httpErr := err.(*httperrors.HTTPError)
			if got, ok := httpErr.ErrorDetails.(string); !ok || got != testCase.want {
				t.Fatalf("error_details = %#v, want %q", httpErr.ErrorDetails, testCase.want)
			}
		})
	}
}

// The relation properties of indirect mapping_rules are read as JSON from a request body, and kept
// from a mapping that is already typed, such as one converted from a BKN file.
func TestValidateIndirectMappingRulesReadsRelationProperties(t *testing.T) {
	Convey("relation properties of indirect mapping_rules", t, func() {
		ctx := context.Background()
		var body struct {
			MappingRules any `json:"mapping_rules"`
		}
		So(json.Unmarshal([]byte(`{"mapping_rules":{
			"backing_data_source":{"type":"resource","id":"res_employment"},
			"source_mapping_rules":[{"source_property":{"name":"id"},"target_property":{"name":"emp_id"}}],
			"target_mapping_rules":[{"source_property":{"name":"dept_id"},"target_property":{"name":"id"}}],
			"properties":[{"name":"since","type":"date","constraints":{"range":{"min":"2020-01-01"}}}]}}`), &body), ShouldBeNil)

		mapping, err := validateInDirectMappingRules(ctx, body.MappingRules, true)
		So(err, ShouldBeNil)
		So(len(mapping.Properties), ShouldEqual, 1)
		So(mapping.Properties[0].Name, ShouldEqual, "since")
		So(mapping.Properties[0].Constraints.Range.Min, ShouldEqual, "2020-01-01")

		typed, err := validateInDirectMappingRules(ctx, mapping, true)
		So(err, ShouldBeNil)
		So(typed.Properties, ShouldResemble, mapping.Properties)

		delete(body.MappingRules.(map[string]any), "properties")
		mapping, err = validateInDirectMappingRules(ctx, body.MappingRules, true)
		So(err, ShouldBeNil)
		So(mapping.Properties, ShouldBeNil)
	})
}

// Only an indirect relation has relation properties (SPECIFICATION.md §7.6). A direct or
// filtered_cross_join request that carries them is refused, as a BKN file declaring them is, instead
// of being saved without them (#2892).
func TestValidateRelationTypeRejectsPropertiesOfNonIndirect(t *testing.T) {
	const property = `[{"name":"supply_unit","type":"string","mapped_field":{"name":"unit"},"constraints":{"length":{"max":32}}}]`
	decode := func(t *testing.T, body string) *interfaces.RelationType {
		t.Helper()
		var rt interfaces.RelationType
		if err := json.Unmarshal([]byte(body), &rt); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return &rt
	}
	crossJoin := func(properties string) string {
		return `{"id":"supplier_product","name":"supplies","source_object_type_id":"supplier",
			"target_object_type_id":"product","type":"filtered_cross_join","cardinality":"many_to_many",
			"mapping_rules":{"source_condition":null,"target_condition":null,"properties":` + properties + `}}`
	}
	direct := `{"id":"supplier_product","name":"supplies","source_object_type_id":"supplier",
		"target_object_type_id":"product","type":"direct","mapping_rules":{"properties":` + property + `}}`

	refused := []struct {
		name   string
		body   string
		strict bool
		typ    string
	}{
		{"filtered_cross_join, strict", crossJoin(property), true, interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN},
		{"filtered_cross_join, not strict", crossJoin(property), false, interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN},
		{"direct, strict", direct, true, interfaces.RELATION_TYPE_DIRECT},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			for _, lang := range []struct {
				language i18n.Language
				want     string
			}{
				{i18n.AmericanEnglish, "Only indirect relations have relation properties (mapping_rules.properties), but type is " + tc.typ + ". Remove the properties, or change the type to indirect."},
				{i18n.SimplifiedChinese, "只有 indirect 关系可以声明关系属性（mapping_rules.properties），当前类型为 " + tc.typ + "。请去掉关系属性，或把类型改为 indirect。"},
			} {
				err := ValidateRelationType(i18n.WithLanguage(context.Background(), lang.language), decode(t, tc.body), tc.strict)
				httpErr, ok := err.(*httperrors.HTTPError)
				if !ok {
					t.Fatalf("error = %#v, want *httperrors.HTTPError", err)
				}
				if httpErr.HTTPCode != 400 || httpErr.ErrorCode != berrors.BknBackend_RelationType_InvalidParameter_Declarations {
					t.Fatalf("error = %d %s, want 400 %s", httpErr.HTTPCode, httpErr.ErrorCode,
						berrors.BknBackend_RelationType_InvalidParameter_Declarations)
				}
				if got, _ := httpErr.ErrorDetails.(string); got != lang.want {
					t.Fatalf("error_details = %q, want %q", got, lang.want)
				}
			}
		})
	}

	// A filtered_cross_join without relation properties, or with an empty list, is saved as before.
	for _, properties := range []string{"null", "[]"} {
		rt := decode(t, crossJoin(properties))
		if err := ValidateRelationType(context.Background(), rt, true); err != nil {
			t.Fatalf("properties %s: %v", properties, err)
		}
		if _, ok := rt.MappingRules.(*interfaces.FilteredCrossJoinMapping); !ok {
			t.Fatalf("properties %s: mapping_rules = %T, want *FilteredCrossJoinMapping", properties, rt.MappingRules)
		}
	}
	rt := decode(t, `{"id":"supplier_product","name":"supplies","source_object_type_id":"supplier",
		"target_object_type_id":"product","type":"filtered_cross_join","mapping_rules":{"source_condition":null,"target_condition":null}}`)
	if err := ValidateRelationType(context.Background(), rt, true); err != nil {
		t.Fatalf("filtered_cross_join without properties: %v", err)
	}

	// An indirect relation keeps its relation properties.
	rt = decode(t, `{"id":"supplier_product","name":"supplies","source_object_type_id":"supplier",
		"target_object_type_id":"product","type":"indirect","mapping_rules":{
		"backing_data_source":{"type":"resource","id":"res_supply"},
		"source_mapping_rules":[{"source_property":{"name":"supplier_id"},"target_property":{"name":"supplier_id"}}],
		"target_mapping_rules":[{"source_property":{"name":"product_id"},"target_property":{"name":"product_id"}}],
		"properties":`+property+`}}`)
	if err := ValidateRelationType(context.Background(), rt, true); err != nil {
		t.Fatalf("indirect with properties: %v", err)
	}
	if props := rt.RelationProperties(); len(props) != 1 || props[0].Name != "supply_unit" {
		t.Fatalf("indirect relation properties = %#v", props)
	}
}

// A PUT that turns a relation into filtered_cross_join and keeps its relation properties is refused
// before anything is saved, so the stored relation stays as it was (#2892).
func TestUpdateRelationTypeRefusesPropertiesOfNonIndirectBeforeSaving(t *testing.T) {
	test := setGinMode()
	defer test()
	engine := gin.New()
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	as := bmock.NewMockAuthService(mockCtrl)
	rts := bmock.NewMockRelationTypeService(mockCtrl)
	kns := bmock.NewMockKNService(mockCtrl)
	MockNewRelationTypeRestHandler(&common.AppSetting{}, as, rts, kns).RegisterPublic(engine)
	as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{}, nil)
	kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", gomock.Any()).Return("kn1", true, nil)
	rts.EXPECT().CheckRelationTypeExistByID(gomock.Any(), "kn1", gomock.Any(), "supplier_product").Return("supplies", true, nil)
	// No UpdateRelationType call is expected: gomock fails the test if the handler saves.

	body := `{"id":"supplier_product","name":"supplies","source_object_type_id":"supplier",
		"target_object_type_id":"product","type":"filtered_cross_join","cardinality":"many_to_many",
		"mapping_rules":{"source_condition":null,"target_condition":null,
		"properties":[{"name":"supply_unit","type":"string","mapped_field":{"name":"unit"}}]}}`
	req := httptest.NewRequest(http.MethodPut,
		"/api/bkn-backend/v1/knowledge-networks/kn1/relation-types/supplier_product?strict_mode=true",
		bytes.NewReader([]byte(body)))
	req.Header.Set(interfaces.CONTENT_TYPE_NAME, interfaces.CONTENT_TYPE_JSON)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	var resp httperrors.BaseError
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.ErrorCode != berrors.BknBackend_RelationType_InvalidParameter_Declarations {
		t.Fatalf("error_code = %s, want %s", resp.ErrorCode, berrors.BknBackend_RelationType_InvalidParameter_Declarations)
	}
}
