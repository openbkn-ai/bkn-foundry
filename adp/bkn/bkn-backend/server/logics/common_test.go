// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func Test_BuildDslQuery(t *testing.T) {
	Convey("Test BuildDslQuery\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid query string and query\n", func() {
			queryStr := `{"match_all": {}}`
			query := &interfaces.ConceptsQuery{
				Limit: 10,
				Sort: []*interfaces.SortParams{
					{
						Field:     "name",
						Direction: "asc",
					},
				},
			}
			dsl, err := BuildDslQuery(ctx, queryStr, query)
			So(err, ShouldBeNil)
			So(dsl, ShouldNotBeNil)
			So(dsl["size"], ShouldEqual, 10)
			So(dsl["track_scores"], ShouldEqual, true)
			sort, ok := dsl["sort"].([]map[string]any)
			So(ok, ShouldBeTrue)
			So(len(sort), ShouldEqual, 1)
			So(sort[0]["name"], ShouldEqual, "asc")
		})

		Convey("Success with empty sort\n", func() {
			queryStr := `{"match_all": {}}`
			query := &interfaces.ConceptsQuery{
				Limit: 10,
				Sort:  []*interfaces.SortParams{},
			}
			dsl, err := BuildDslQuery(ctx, queryStr, query)
			So(err, ShouldBeNil)
			So(dsl, ShouldNotBeNil)
			sort, ok := dsl["sort"].([]map[string]any)
			So(ok, ShouldBeTrue)
			So(len(sort), ShouldEqual, 0)
		})

		Convey("Success with multiple sort params\n", func() {
			queryStr := `{"match_all": {}}`
			query := &interfaces.ConceptsQuery{
				Limit: 20,
				Sort: []*interfaces.SortParams{
					{
						Field:     "name",
						Direction: "asc",
					},
					{
						Field:     "create_time",
						Direction: "desc",
					},
				},
			}
			dsl, err := BuildDslQuery(ctx, queryStr, query)
			So(err, ShouldBeNil)
			So(dsl, ShouldNotBeNil)
			sort, ok := dsl["sort"].([]map[string]any)
			So(ok, ShouldBeTrue)
			So(len(sort), ShouldEqual, 2)
			So(sort[0]["name"], ShouldEqual, "asc")
			So(sort[1]["create_time"], ShouldEqual, "desc")
		})

		Convey("Failed with invalid JSON query string\n", func() {
			queryStr := `{"match_all": {invalid json}`
			query := &interfaces.ConceptsQuery{
				Limit: 10,
			}
			dsl, err := BuildDslQuery(ctx, queryStr, query)
			So(err, ShouldNotBeNil)
			So(dsl, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_InternalError_UnMarshalDataFailed)
		})

		Convey("Success with complex query string\n", func() {
			queryStr := `{"bool": {"must": [{"term": {"status": "active"}}]}}`
			query := &interfaces.ConceptsQuery{
				Limit: 15,
				Sort: []*interfaces.SortParams{
					{
						Field:     "id",
						Direction: "asc",
					},
				},
			}
			dsl, err := BuildDslQuery(ctx, queryStr, query)
			So(err, ShouldBeNil)
			So(dsl, ShouldNotBeNil)
			queryMap, ok := dsl["query"].(map[string]any)
			So(ok, ShouldBeTrue)
			So(queryMap, ShouldNotBeNil)
		})
	})
}

func Test_VegaResourceIndexCaps(t *testing.T) {
	Convey("Test VegaResourceIndexCaps\n", t, func() {
		fulltextAndVector := &interfaces.Property{
			Name: "material_name",
			Type: "string",
			Features: []interfaces.PropertyFeature{
				{FeatureType: interfaces.FieldFeatureType_Fulltext},
				{FeatureType: interfaces.FieldFeatureType_Vector},
			},
		}
		keywordOnly := &interfaces.Property{
			Name: "material_number",
			Type: "string",
			Features: []interfaces.PropertyFeature{
				{FeatureType: interfaces.FieldFeatureType_Keyword},
			},
		}
		plain := &interfaces.Property{Name: "qty", Type: "decimal"}

		Convey("Nil resource returns nil\n", func() {
			So(VegaResourceIndexCaps(nil), ShouldBeNil)
		})

		Convey("Resource without a local index returns nil even when features exist\n", func() {
			res := &interfaces.VegaResource{
				SchemaDefinition: []*interfaces.Property{fulltextAndVector},
			}
			So(VegaResourceIndexCaps(res), ShouldBeNil)
		})

		Convey("Indexed resource derives caps per field\n", func() {
			res := &interfaces.VegaResource{
				LocalIndexName:   "vega-build-res-task",
				SchemaDefinition: []*interfaces.Property{fulltextAndVector, keywordOnly, plain, nil},
			}

			caps := VegaResourceIndexCaps(res)
			So(len(caps), ShouldEqual, 2)
			So(caps["material_name"].Fulltext, ShouldBeTrue)
			So(caps["material_name"].Vector, ShouldBeTrue)
			So(caps["material_name"].Keyword, ShouldBeFalse)
			So(caps["material_number"].Keyword, ShouldBeTrue)
			So(caps["material_number"].Fulltext, ShouldBeFalse)

			// 没有任何 feature 的字段不进结果，取到的是零值
			_, exists := caps["qty"]
			So(exists, ShouldBeFalse)
			So(caps["qty"].Fulltext, ShouldBeFalse)
		})
	})
}

func TestVegaResourceIndexCaps_RefPropertyRedirectsCapability(t *testing.T) {
	res := &interfaces.VegaResource{
		LocalIndexName: "vega-build-abc",
		SchemaDefinition: []*interfaces.Property{
			{
				Name: "fulltext_summary",
				Features: []interfaces.PropertyFeature{
					{FeatureType: interfaces.FieldFeatureType_Fulltext, RefProperty: "summary"},
				},
			},
			{
				Name: "summary",
				Features: []interfaces.PropertyFeature{
					{FeatureType: interfaces.FieldFeatureType_Keyword},
				},
			},
		},
	}

	caps := VegaResourceIndexCaps(res)

	if _, exists := caps["fulltext_summary"]; exists {
		t.Fatalf("capability must land on the referenced field, not the declaring property")
	}
	got := caps["summary"]
	if !got.Fulltext || !got.Keyword {
		t.Fatalf("summary should carry both the redirected fulltext and its own keyword, got %+v", got)
	}
}

func TestVegaResourceIndexCaps_VectorCapabilityFollowsFeature(t *testing.T) {
	res := &interfaces.VegaResource{
		LocalIndexName: "vega-build-abc",
		SchemaDefinition: []*interfaces.Property{
			{
				Name: "stadium_name",
				Features: []interfaces.PropertyFeature{
					{FeatureType: interfaces.FieldFeatureType_Vector},
					{FeatureType: interfaces.FieldFeatureType_Fulltext},
				},
			},
			{Name: "stadium_id"},
		},
	}

	caps := VegaResourceIndexCaps(res)

	if !caps["stadium_name"].Vector || !caps["stadium_name"].Fulltext {
		t.Fatalf("both features must survive on the same field, got %+v", caps["stadium_name"])
	}
	if _, exists := caps["stadium_id"]; exists {
		t.Fatalf("a field without features has no index capability, got %+v", caps)
	}
}

func TestVegaResourceIndexCaps_NoVectorFieldWithoutLocalIndex(t *testing.T) {
	res := &interfaces.VegaResource{
		SchemaDefinition: []*interfaces.Property{
			{Name: "stadium_name", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.FieldFeatureType_Vector}}},
		},
	}

	if caps := VegaResourceIndexCaps(res); len(caps) != 0 {
		t.Fatalf("declared features without a built index must yield no capability, got %+v", caps)
	}
}
