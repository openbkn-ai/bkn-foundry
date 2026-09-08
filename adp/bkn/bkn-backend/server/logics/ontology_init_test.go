// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
	mock_interfaces "bkn-backend/interfaces/mock"
	"bkn-backend/logics/model_factory"
)

// ── bknCatalogRequest ─────────────────────────────────────────────────────────

// Regression for issue #7: the BKN logical namespace catalog must be created with
// Enabled=true, otherwise it lands disabled and BKN search/query fails with
// VegaBackend.Catalog.IsDisabled.
func Test_bknCatalogRequest(t *testing.T) {
	Convey("Test bknCatalogRequest\n", t, func() {
		req := bknCatalogRequest()

		Convey("Catalog is enabled at creation (issue #7)\n", func() {
			So(req.Enabled, ShouldBeTrue)
		})

		Convey("Uses the BKN catalog id and name\n", func() {
			So(req.ID, ShouldEqual, interfaces.BKN_CATALOG_ID)
			So(req.Name, ShouldEqual, interfaces.BKN_CATALOG_NAME)
		})
	})
}

func TestBKNConceptDatasetIncludesEmptyIndexConfig(t *testing.T) {
	data, err := json.Marshal(interfaces.BKN_CONCEPT_DATASET)
	if err != nil {
		t.Fatalf("marshal BKN concept dataset: %v", err)
	}

	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatalf("unmarshal BKN concept dataset: %v", err)
	}

	indexConfig, ok := request["index_config"]
	if !ok {
		t.Fatal("create dataset request must include index_config")
	}
	if config, ok := indexConfig.(map[string]any); !ok || len(config) != 0 {
		t.Fatalf("index_config = %#v, want empty object", indexConfig)
	}
}

func TestInitPassesResolvedEmbeddingModelToVega(t *testing.T) {
	Convey("Init passes the resolved embedding model when creating the dataset (issue #625)\n", t, func() {
		ctrl := gomock.NewController(t)
		mfs := mock_interfaces.NewMockModelFactoryService(ctrl)
		vbs := mock_interfaces.NewMockVegaBackendService(ctrl)
		patches := gomonkey.ApplyFunc(model_factory.NewModelFactoryService, func(_ *common.AppSetting, _ interfaces.ModelFactoryAccess) interfaces.ModelFactoryService { return mfs })
		Reset(func() {
			patches.Reset()
		})

		ctx := context.Background()
		mfs.EXPECT().GetDefaultModel(ctx).Return(&interfaces.SmallModel{
			ModelID:      "2091780333946146816",
			ModelName:    "text-embedding-v4",
			EmbeddingDim: 1024,
		}, nil)
		vbs.EXPECT().GetCatalogByID(ctx, interfaces.BKN_CATALOG_ID).Return(&interfaces.Catalog{ID: interfaces.BKN_CATALOG_ID}, nil)
		vbs.EXPECT().GetResourceByID(ctx, interfaces.BKN_DATASET_ID).Return(nil, nil)
		vbs.EXPECT().CreateResource(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, resource *interfaces.VegaResource) error {
			So(resource.IndexConfig, ShouldNotBeNil)
			So(resource.IndexConfig.DefaultFulltextAnalyzer, ShouldEqual, "standard")
			So(resource.IndexConfig.DefaultEmbeddingModel, ShouldEqual, "2091780333946146816")
			return nil
		})

		err := Init(ctx, &common.AppSetting{ServerSetting: common.ServerSetting{DefaultSmallModelEnabled: true}}, vbs)
		So(err, ShouldBeNil)
	})
}

func TestInitPassesResolvedEmbeddingModelWhenRecreatingDataset(t *testing.T) {
	Convey("Init passes the resolved embedding model when recreating the dataset (issue #625)\n", t, func() {
		ctrl := gomock.NewController(t)
		mfs := mock_interfaces.NewMockModelFactoryService(ctrl)
		vbs := mock_interfaces.NewMockVegaBackendService(ctrl)
		patches := gomonkey.ApplyFunc(model_factory.NewModelFactoryService, func(_ *common.AppSetting, _ interfaces.ModelFactoryAccess) interfaces.ModelFactoryService { return mfs })
		Reset(func() {
			patches.Reset()
		})

		ctx := context.Background()
		mfs.EXPECT().GetDefaultModel(ctx).Return(&interfaces.SmallModel{
			ModelID:      "2091780333946146816",
			ModelName:    "text-embedding-v4",
			EmbeddingDim: 1024,
		}, nil)
		vbs.EXPECT().GetCatalogByID(ctx, interfaces.BKN_CATALOG_ID).Return(&interfaces.Catalog{ID: interfaces.BKN_CATALOG_ID}, nil)
		vbs.EXPECT().GetResourceByID(ctx, interfaces.BKN_DATASET_ID).Return(&interfaces.VegaResource{
			ID:               interfaces.BKN_DATASET_ID,
			SchemaDefinition: []*interfaces.Property{{Name: "stale"}},
		}, nil)
		vbs.EXPECT().DeleteResource(ctx, interfaces.BKN_DATASET_ID).Return(nil)
		vbs.EXPECT().CreateResource(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, resource *interfaces.VegaResource) error {
			So(resource.IndexConfig.DefaultEmbeddingModel, ShouldEqual, "2091780333946146816")
			return nil
		})

		err := Init(ctx, &common.AppSetting{ServerSetting: common.ServerSetting{DefaultSmallModelEnabled: true}}, vbs)
		So(err, ShouldBeNil)
	})
}

func TestInitRecreatesDatasetWhenEmbeddingModelIDDiffers(t *testing.T) {
	Convey("Init recreates the dataset when its embedding model reference differs (issue #1142)\n", t, func() {
		ctrl := gomock.NewController(t)
		mfs := mock_interfaces.NewMockModelFactoryService(ctrl)
		vbs := mock_interfaces.NewMockVegaBackendService(ctrl)
		patches := gomonkey.ApplyFunc(model_factory.NewModelFactoryService, func(_ *common.AppSetting, _ interfaces.ModelFactoryAccess) interfaces.ModelFactoryService { return mfs })
		Reset(func() {
			patches.Reset()
		})

		ctx := context.Background()
		model := &interfaces.SmallModel{
			ModelID:      "2091780333946146816",
			ModelName:    "text-embedding-v4",
			EmbeddingDim: 1024,
		}
		mfs.EXPECT().GetDefaultModel(ctx).Return(model, nil)
		vbs.EXPECT().GetCatalogByID(ctx, interfaces.BKN_CATALOG_ID).Return(&interfaces.Catalog{ID: interfaces.BKN_CATALOG_ID}, nil)
		vbs.EXPECT().GetResourceByID(ctx, interfaces.BKN_DATASET_ID).Return(&interfaces.VegaResource{
			ID:               interfaces.BKN_DATASET_ID,
			SchemaDefinition: interfaces.GetBKNConceptSchemaDefinition(model.EmbeddingDim, true),
			// Healthy index: without it the managed-index check short-circuits and this case
			// never reaches the comparison it exists to exercise.
			LocalIndexName:   "vega-dataset-01",
			LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			IndexConfig:      &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: model.ModelName},
		}, nil)
		vbs.EXPECT().DeleteResource(ctx, interfaces.BKN_DATASET_ID).Return(nil)
		vbs.EXPECT().CreateResource(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, resource *interfaces.VegaResource) error {
			So(resource.IndexConfig.DefaultEmbeddingModel, ShouldEqual, model.ModelID)
			return nil
		})

		err := Init(ctx, &common.AppSetting{ServerSetting: common.ServerSetting{DefaultSmallModelEnabled: true}}, vbs)
		So(err, ShouldBeNil)
	})
}

func TestInitKeepsDatasetWhenSchemaAndEmbeddingModelIDMatch(t *testing.T) {
	Convey("Init keeps the dataset when its schema and embedding model ID match (issue #1142)\n", t, func() {
		ctrl := gomock.NewController(t)
		mfs := mock_interfaces.NewMockModelFactoryService(ctrl)
		vbs := mock_interfaces.NewMockVegaBackendService(ctrl)
		patches := gomonkey.ApplyFunc(model_factory.NewModelFactoryService, func(_ *common.AppSetting, _ interfaces.ModelFactoryAccess) interfaces.ModelFactoryService { return mfs })
		Reset(func() {
			patches.Reset()
		})

		ctx := context.Background()
		model := &interfaces.SmallModel{
			ModelID:      "2091780333946146816",
			ModelName:    "text-embedding-v4",
			EmbeddingDim: 1024,
		}
		mfs.EXPECT().GetDefaultModel(ctx).Return(model, nil)
		vbs.EXPECT().GetCatalogByID(ctx, interfaces.BKN_CATALOG_ID).Return(&interfaces.Catalog{ID: interfaces.BKN_CATALOG_ID}, nil)
		vbs.EXPECT().GetResourceByID(ctx, interfaces.BKN_DATASET_ID).Return(&interfaces.VegaResource{
			ID:               interfaces.BKN_DATASET_ID,
			SchemaDefinition: interfaces.GetBKNConceptSchemaDefinition(model.EmbeddingDim, true),
			IndexConfig:      &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: model.ModelID},
			// A dataset is only keepable when the index behind it can still be written. A row that
			// matches on schema and model but has no managed index accepts nothing.
			LocalIndexName:   "vega-dataset-01",
			LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
		}, nil)

		err := Init(ctx, &common.AppSetting{ServerSetting: common.ServerSetting{DefaultSmallModelEnabled: true}}, vbs)
		So(err, ShouldBeNil)
	})
}

func TestBKNConceptDatasetRequest(t *testing.T) {
	Convey("Dataset request keeps the global template immutable\n", t, func() {
		request := bknConceptDatasetRequest(nil, "text-embedding-v4")

		So(request, ShouldNotEqual, interfaces.BKN_CONCEPT_DATASET)
		So(request.IndexConfig.DefaultFulltextAnalyzer, ShouldEqual, "standard")
		So(request.IndexConfig.DefaultEmbeddingModel, ShouldEqual, "text-embedding-v4")
		So(interfaces.BKN_CONCEPT_DATASET.IndexConfig.DefaultEmbeddingModel, ShouldEqual, "")
	})
}

func TestBKNConceptDatasetSchemaHasNoFeatureRefProperties(t *testing.T) {
	seen := make(map[string]struct{})
	displayNames := make(map[string]string)
	for _, prop := range interfaces.GetBKNConceptSchemaDefinition(768, true) {
		if _, exists := seen[prop.Name]; exists {
			t.Fatalf("duplicate dataset property %q", prop.Name)
		}
		seen[prop.Name] = struct{}{}
		if previous, exists := displayNames[prop.DisplayName]; exists {
			t.Fatalf("duplicate dataset display name %q for properties %q and %q", prop.DisplayName, previous, prop.Name)
		}
		displayNames[prop.DisplayName] = prop.Name
		for _, feature := range prop.Features {
			if feature.RefProperty != "" {
				t.Fatalf("dataset feature %q on property %q must not set ref_property", feature.FeatureName, prop.Name)
			}
		}
	}
}

// ── comparePropertyFeature ────────────────────────────────────────────────────

func Test_comparePropertyFeature(t *testing.T) {
	Convey("Test comparePropertyFeature\n", t, func() {
		base := &interfaces.PropertyFeature{
			FeatureName: "keyword",
			FeatureType: "keyword",
			RefProperty: "name",
			IsDefault:   true,
			IsNative:    false,
			Config:      map[string]any{"dim": 768},
		}

		Convey("Equal features returns true\n", func() {
			other := &interfaces.PropertyFeature{
				FeatureName: "keyword",
				FeatureType: "keyword",
				RefProperty: "name",
				IsDefault:   true,
				IsNative:    false,
				Config:      map[string]any{"dim": 768},
			}
			So(comparePropertyFeature(base, other), ShouldBeTrue)
		})

		Convey("Different FeatureName returns false\n", func() {
			other := *base
			other.FeatureName = "vector"
			So(comparePropertyFeature(base, &other), ShouldBeFalse)
		})

		Convey("Different FeatureType returns false\n", func() {
			other := *base
			other.FeatureType = "fulltext"
			So(comparePropertyFeature(base, &other), ShouldBeFalse)
		})

		Convey("Different RefProperty returns false\n", func() {
			other := *base
			other.RefProperty = "title"
			So(comparePropertyFeature(base, &other), ShouldBeFalse)
		})

		Convey("Different IsDefault returns false\n", func() {
			other := *base
			other.IsDefault = false
			So(comparePropertyFeature(base, &other), ShouldBeFalse)
		})

		Convey("Different IsNative returns false\n", func() {
			other := *base
			other.IsNative = true
			So(comparePropertyFeature(base, &other), ShouldBeFalse)
		})

		Convey("Different Config length returns false\n", func() {
			other := *base
			other.Config = map[string]any{"dim": 768, "extra": "x"}
			So(comparePropertyFeature(base, &other), ShouldBeFalse)
		})

		Convey("Missing Config key returns false\n", func() {
			other := *base
			other.Config = map[string]any{"other_key": 768}
			So(comparePropertyFeature(base, &other), ShouldBeFalse)
		})

		Convey("Different Config value returns false\n", func() {
			other := *base
			other.Config = map[string]any{"dim": 512}
			So(comparePropertyFeature(base, &other), ShouldBeFalse)
		})

		Convey("Both nil Config returns true\n", func() {
			f1 := &interfaces.PropertyFeature{FeatureName: "f", Config: nil}
			f2 := &interfaces.PropertyFeature{FeatureName: "f", Config: nil}
			So(comparePropertyFeature(f1, f2), ShouldBeTrue)
		})
	})
}

// ── compareProperty ───────────────────────────────────────────────────────────

func Test_compareProperty(t *testing.T) {
	Convey("Test compareProperty\n", t, func() {
		base := &interfaces.Property{
			Name:        "content",
			Type:        "text",
			DisplayName: "Content",
			Description: "desc",
			Features: []interfaces.PropertyFeature{
				{FeatureName: "keyword", FeatureType: "keyword"},
			},
		}

		Convey("Equal properties returns true\n", func() {
			other := &interfaces.Property{
				Name:        "content",
				Type:        "text",
				DisplayName: "Content",
				Description: "desc",
				Features: []interfaces.PropertyFeature{
					{FeatureName: "keyword", FeatureType: "keyword"},
				},
			}
			So(compareProperty(base, other), ShouldBeTrue)
		})

		Convey("Different Name returns false\n", func() {
			other := *base
			other.Name = "title"
			So(compareProperty(base, &other), ShouldBeFalse)
		})

		Convey("Different Type returns false\n", func() {
			other := *base
			other.Type = "keyword"
			So(compareProperty(base, &other), ShouldBeFalse)
		})

		Convey("Different DisplayName returns false\n", func() {
			other := *base
			other.DisplayName = "Other"
			So(compareProperty(base, &other), ShouldBeFalse)
		})

		Convey("Different Description returns false\n", func() {
			other := *base
			other.Description = "other desc"
			So(compareProperty(base, &other), ShouldBeFalse)
		})

		Convey("Different Features length returns false\n", func() {
			other := *base
			other.Features = []interfaces.PropertyFeature{
				{FeatureName: "keyword"},
				{FeatureName: "vector"},
			}
			So(compareProperty(base, &other), ShouldBeFalse)
		})

		Convey("Feature not found in other returns false\n", func() {
			other := *base
			other.Features = []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: "fulltext"},
			}
			So(compareProperty(base, &other), ShouldBeFalse)
		})

		Convey("Feature differs in other returns false\n", func() {
			other := *base
			other.Features = []interfaces.PropertyFeature{
				{FeatureName: "keyword", FeatureType: "fulltext"},
			}
			So(compareProperty(base, &other), ShouldBeFalse)
		})

		Convey("No features, all fields equal returns true\n", func() {
			p1 := &interfaces.Property{Name: "id", Type: "long"}
			p2 := &interfaces.Property{Name: "id", Type: "long"}
			So(compareProperty(p1, p2), ShouldBeTrue)
		})
	})
}

// ── deepCompareSchemas ────────────────────────────────────────────────────────

func Test_deepCompareSchemas(t *testing.T) {
	Convey("Test deepCompareSchemas\n", t, func() {
		p1 := &interfaces.Property{Name: "id", Type: "long"}
		p2 := &interfaces.Property{Name: "content", Type: "text"}

		Convey("Equal schemas returns true\n", func() {
			s1 := []*interfaces.Property{p1, p2}
			s2 := []*interfaces.Property{
				{Name: "id", Type: "long"},
				{Name: "content", Type: "text"},
			}
			So(deepCompareSchemas(s1, s2), ShouldBeTrue)
		})

		Convey("Both empty returns true\n", func() {
			So(deepCompareSchemas(nil, nil), ShouldBeTrue)
		})

		Convey("Different lengths returns false\n", func() {
			So(deepCompareSchemas([]*interfaces.Property{p1}, []*interfaces.Property{p1, p2}), ShouldBeFalse)
		})

		Convey("Same length but missing property name in schema2 returns false\n", func() {
			s1 := []*interfaces.Property{{Name: "id", Type: "long"}}
			s2 := []*interfaces.Property{{Name: "other", Type: "long"}}
			So(deepCompareSchemas(s1, s2), ShouldBeFalse)
		})

		Convey("Same properties but different field value returns false\n", func() {
			s1 := []*interfaces.Property{{Name: "id", Type: "long"}}
			s2 := []*interfaces.Property{{Name: "id", Type: "keyword"}}
			So(deepCompareSchemas(s1, s2), ShouldBeFalse)
		})
	})
}

// ── datasetRebuildReason ──────────────────────────────────────────────────────

// A concept dataset row can outlive the index behind it. Vega clears the managed index name and
// marks it unavailable whenever a schema update is classified as build-related, and the row that
// remains still matches on schema and embedding model. Adoption used to accept such a row, and
// every concept write afterwards failed with 400 "dataset resource has no available local index" —
// permanently, because startup never questioned the row again. Observed on a real environment:
// creating a knowledge network answered InsertOpenSearchDataFailed / WriteDatasetDocument returned
// HTTP 400, and the only way out was a human deleting the resource by hand.
func Test_datasetRebuildReason(t *testing.T) {
	Convey("接手存量概念数据集前要先问它还能不能写\n", t, func() {
		schema := []*interfaces.Property{{Name: "name", Type: "text"}}
		healthy := &interfaces.VegaResource{
			ID:               interfaces.BKN_DATASET_ID,
			SchemaDefinition: schema,
			IndexConfig:      &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: "model-1"},
			LocalIndexName:   "vega-dataset-01",
			LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
		}

		Convey("健康的数据集不重建", func() {
			So(datasetRebuildReason(healthy, schema, "model-1"), ShouldEqual, "")
		})

		Convey("托管索引名为空要重建", func() {
			broken := *healthy
			broken.LocalIndexName = ""
			So(datasetRebuildReason(&broken, schema, "model-1"), ShouldNotEqual, "")
		})

		Convey("stale 不重建——索引还在，写入照常，重建会白白删光概念文档", func() {
			// vega marks a dataset stale when a build-relevant change lands, and keeps the index
			// name. The write path only requires a name, so documents still land; what degrades is
			// retrieval, until vega rebuilds the index through its own path. Deleting the resource
			// here would trade a degraded ranking for lost data.
			behind := *healthy
			behind.LocalIndexStatus = "stale"
			So(datasetRebuildReason(&behind, schema, "model-1"), ShouldEqual, "")
		})

		Convey("托管索引不可用要重建", func() {
			broken := *healthy
			broken.LocalIndexStatus = interfaces.ResourceLocalIndexStatusUnavailable
			So(datasetRebuildReason(&broken, schema, "model-1"), ShouldNotEqual, "")
		})

		Convey("schema 变了要重建", func() {
			changed := *healthy
			So(datasetRebuildReason(&changed, []*interfaces.Property{{Name: "other", Type: "text"}}, "model-1"),
				ShouldNotEqual, "")
		})

		Convey("embedding 模型变了要重建", func() {
			changed := *healthy
			changed.IndexConfig = &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: "model-2"}
			So(datasetRebuildReason(&changed, schema, "model-1"), ShouldNotEqual, "")
		})

		Convey("资源不存在要重建", func() {
			So(datasetRebuildReason(nil, schema, "model-1"), ShouldNotEqual, "")
		})
	})
}
