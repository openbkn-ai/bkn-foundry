// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestBatchBuildExecuteType(t *testing.T) {
	incrementalTask := &interfaces.BuildTask{
		Mode:        interfaces.BuildTaskModeBatch,
		ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
	}

	assert.Equal(t, interfaces.BuildTaskExecuteTypeIncremental, batchBuildExecuteType(incrementalTask))
	fullTask := &interfaces.BuildTask{Mode: interfaces.BuildTaskModeBatch, ExecuteType: interfaces.BuildTaskExecuteTypeFull}
	assert.Equal(t, interfaces.BuildTaskExecuteTypeFull, batchBuildExecuteType(fullTask))
	fullTask.SyncedMark = `{"id":100}`
	fullTask.SyncedCount = 100
	assert.Equal(t, interfaces.BuildTaskExecuteTypeIncremental, batchBuildExecuteType(fullTask))
}

func TestBatchBuildWorkerHandleTask(t *testing.T) {
	t.Run("injects creator into downstream context", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		lim.EXPECT().CheckExist(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
		bbw := &batchBuildWorker{bts: bts, rs: rs, cs: cs, lim: lim}
		creator := interfaces.AccountInfo{ID: "u1", Type: "user"}

		task := &interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusPending, Creator: creator,
		}
		rs.EXPECT().InternalGetByID(gomock.Any(), "r1").Return(&interfaces.Resource{ID: "r1", CatalogID: "c1"}, nil)
		bts.EXPECT().InternalMarkFailed(gomock.Any(), "t1", "get catalog failed: forbidden").
			Return(true, nil)

		var gotAccount interfaces.AccountInfo
		var hasAccount bool
		cs.EXPECT().InternalGetByID(gomock.Any(), "c1", true).DoAndReturn(
			func(ctx context.Context, id string, withSensitiveFields bool) (*interfaces.Catalog, error) {
				gotAccount, hasAccount = workerAccountFromCtx(ctx)
				return nil, errors.New("forbidden")
			})

		require.NoError(t, bbw.Run(context.Background(), task))
		require.True(t, hasAccount)
		assert.Equal(t, creator, gotAccount)
	})

	t.Run("cancels task when resource was deleted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		bbw := &batchBuildWorker{bts: bts, rs: rs}

		task := &interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusPending,
		}
		rs.EXPECT().InternalGetByID(gomock.Any(), "r1").Return(nil, nil)
		bts.EXPECT().InternalMarkCancelled(gomock.Any(), "t1", "resource deleted").Return(true, nil)

		require.NoError(t, bbw.Run(context.Background(), task))
	})

	t.Run("cancels task when catalog was deleted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bbw := &batchBuildWorker{bts: bts, rs: rs, cs: cs, lim: lim}

		taskInfo := &interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusPending,
			ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
		}
		rs.EXPECT().InternalGetByID(gomock.Any(), "r1").
			Return(&interfaces.Resource{ID: "r1", CatalogID: "c1"}, nil)
		cs.EXPECT().InternalGetByID(gomock.Any(), "c1", true).
			Return(nil, &rest.HTTPError{HTTPCode: http.StatusNotFound})
		bts.EXPECT().InternalMarkCancelled(gomock.Any(), "t1", "catalog deleted").Return(true, nil)

		require.NoError(t, bbw.Run(context.Background(), taskInfo))
	})

	t.Run("does not switch local index when build fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		lim.EXPECT().CheckExist(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
		bbw := &batchBuildWorker{bts: bts, rs: rs, cs: cs, lim: lim}

		resource := &interfaces.Resource{
			ID:             "r1",
			CatalogID:      "c1",
			LocalIndexName: interfaces.BuildIndexName("r1", "old-task"),
		}
		task := &interfaces.BuildTask{
			ID:          "t1",
			ResourceID:  "r1",
			Mode:        interfaces.BuildTaskModeBatch,
			ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
			Status:      interfaces.BuildTaskStatusPending,
		}
		rs.EXPECT().InternalGetByID(gomock.Any(), "r1").Return(resource, nil)
		cs.EXPECT().InternalGetByID(gomock.Any(), "c1", true).Return(nil, errors.New("catalog down"))
		bts.EXPECT().InternalMarkFailed(gomock.Any(), "t1", "get catalog failed: catalog down").
			Return(true, nil)

		require.NoError(t, bbw.Run(context.Background(), task))
		assert.Equal(t, interfaces.BuildIndexName("r1", "old-task"), resource.LocalIndexName)
	})
}

func TestBatchBuildWorkerExecuteBuild(t *testing.T) {
	t.Run("does not dispatch embedding when index creation fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		enqueueCalled := false
		patches := gomonkey.ApplyFunc(sendEmbeddingTask,
			func(context.Context, chan<- string, string) error {
				enqueueCalled = true
				return nil
			})
		defer patches.Reset()
		bbw := &batchBuildWorker{lim: lim}
		resource := &interfaces.Resource{
			ID: "r1",
			SchemaDefinition: []*interfaces.Property{
				{
					Name: "content", Type: interfaces.DataType_String,
					Features: []interfaces.PropertyFeature{{FeatureType: interfaces.DataType_Vector}},
				},
			},
		}
		buildTask := &interfaces.BuildTask{
			ID: "t1",
			IndexConfig: &interfaces.BuildTaskIndexConfig{Features: map[string]interfaces.BuildTaskFieldIndexFeature{
				"content": {Vector: &interfaces.BuildTaskEmbeddingConfig{ModelID: "m1", Dimensions: 3}},
			}},
		}

		lim.EXPECT().CheckExist(gomock.Any(), interfaces.BuildIndexName("r1", "t1")).Return(false, nil)
		lim.EXPECT().CreateIndex(gomock.Any(), interfaces.BuildIndexName("r1", "t1"), gomock.Any()).
			Return(errors.New("opensearch unavailable"))

		err := bbw.executeBuild(context.Background(), &interfaces.Catalog{ID: "c1"}, resource, buildTask,
			interfaces.BuildTaskExecuteTypeIncremental)
		require.Error(t, err)
		assert.ErrorContains(t, err, "create local index failed: opensearch unavailable")
		assert.False(t, enqueueCalled)
	})
}

func TestAdvanceCursor(t *testing.T) {
	t.Run("advances across batches", func(t *testing.T) {
		keys := []string{"key_id"}

		cursor := advanceCursor(nil, keys, map[string]any{"key_id": "1000"})
		require.Len(t, cursor, 1)
		assert.Equal(t, "1000", cursor[0].Value)

		cursor = advanceCursor(cursor, keys, map[string]any{"key_id": "2000"})
		assert.Equal(t, "2000", cursor[0].Value)

		cursor = advanceCursor(cursor, keys, map[string]any{"key_id": "3000"})
		assert.Equal(t, "3000", cursor[0].Value)
	})

	t.Run("advances multiple keys", func(t *testing.T) {
		keys := []string{"id", "name"}
		cursor := advanceCursor(nil, keys, map[string]any{"id": 1, "name": "a"})
		cursor = advanceCursor(cursor, keys, map[string]any{"id": 2, "name": "b"})

		got := map[string]any{}
		for _, kv := range cursor {
			got[kv.Key] = kv.Value
		}
		assert.Equal(t, 2, got["id"])
		assert.Equal(t, "b", got["name"])
	})
}

func TestReconcileTaskFulltextFeatures(t *testing.T) {
	t.Run("errors when task field is missing from schema features", func(t *testing.T) {
		schema := []*interfaces.Property{
			{Name: "team_name", Type: interfaces.DataType_String},
			{Name: "team_code", Type: interfaces.DataType_String},
		}
		task := buildTaskWithFulltext("team_name", "ik_max_word")

		err := validateTaskFulltextFeatures(schema, task)

		require.Error(t, err)
		assert.Contains(t, err.Error(), `build task fulltext field "team_name"`)
	})

	t.Run("errors when schema has stale field", func(t *testing.T) {
		schema := []*interfaces.Property{
			{Name: "team_name", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
			}},
			{Name: "federation_name", Type: interfaces.DataType_String},
		}
		task := buildTaskWithFulltext("federation_name", "standard")

		err := validateTaskFulltextFeatures(schema, task)

		require.Error(t, err)
		assert.Contains(t, err.Error(), `resource schema fulltext field "team_name"`)
	})

	t.Run("errors when explicit analyzer differs", func(t *testing.T) {
		schema := []*interfaces.Property{
			{Name: "x", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
			}},
		}
		task := buildTaskWithFulltext("x", "ik_max_word")

		err := validateTaskFulltextFeatures(schema, task)

		require.Error(t, err)
		assert.Contains(t, err.Error(), `does not match build task analyzer`)
	})

	t.Run("applies task analyzer when schema omits analyzer", func(t *testing.T) {
		schema := []*interfaces.Property{
			{Name: "x", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
			}},
		}
		task := buildTaskWithFulltext("x", "ik_max_word")

		err := validateTaskFulltextFeatures(schema, task)

		require.NoError(t, err)
		assert.Equal(t, "ik_max_word", schema[0].Features[0].Config["analyzer"])
	})
}

func buildTaskWithFulltext(field string, analyzer string) *interfaces.BuildTask {
	return &interfaces.BuildTask{
		IndexConfig: &interfaces.BuildTaskIndexConfig{
			Features: map[string]interfaces.BuildTaskFieldIndexFeature{
				field: {Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: analyzer}},
			},
		},
	}
}

func TestValidateTaskEmbeddingFeatures(t *testing.T) {
	t.Run("errors when task field is missing from schema features", func(t *testing.T) {
		schema := []*interfaces.Property{{Name: "title", Type: interfaces.DataType_String}}

		err := validateTaskEmbeddingFeatures(schema, buildTaskWithVector("title"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), `build task embedding field "title"`)
	})

	t.Run("errors when schema has stale field", func(t *testing.T) {
		schema := []*interfaces.Property{{
			Name: "title",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "vector", FeatureType: interfaces.PropertyFeatureType_Vector},
			},
		}}

		err := validateTaskEmbeddingFeatures(schema, &interfaces.BuildTask{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), `resource schema embedding field "title"`)
	})

	t.Run("passes when schema and task match", func(t *testing.T) {
		schema := []*interfaces.Property{{
			Name: "title",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "vector", FeatureType: interfaces.PropertyFeatureType_Vector},
			},
		}}

		err := validateTaskEmbeddingFeatures(schema, buildTaskWithVector("title"))

		require.NoError(t, err)
	})
}

func TestValidateBuildTaskSchemaFeatures(t *testing.T) {
	tests := []struct {
		name     string
		category string
		schema   []*interfaces.Property
		wantErr  string
	}{
		{
			name:     "dataset rejects ref property",
			category: interfaces.ResourceCategoryDataset,
			schema: []*interfaces.Property{
				{Name: "content_keyword", Type: interfaces.DataType_String},
				{Name: "content", Type: interfaces.DataType_Text,
					Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "content_keyword"}}},
			},
			wantErr: "must not set ref_property",
		},
		// 从没被 PUT 过的存量资源，库里仍是自引用形状。抹平之后必须能建索引，否则
		// 「push 清掉 condition_operations -> 重建索引 -> 恢复」这条路仍然是断的。
		{
			name:     "accepts legacy self-referencing fulltext feature",
			category: interfaces.ResourceCategoryTable,
			schema: []*interfaces.Property{{
				Name: "title", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureName: "title_fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "title"}},
			}},
		},
		{
			// keyword 的 ref 类型要求是 string，text 字段上的自引用 keyword 特征
			// 在抹平前会撞上 ref 类型校验，抹平后按「特征作用于属性自身」通过
			name:     "accepts legacy self-referencing keyword feature on a text field",
			category: interfaces.ResourceCategoryTable,
			schema: []*interfaces.Property{{
				Name: "title", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureName: "title.keyword", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "title"}},
			}},
		},
		{
			// 入口严、存量宽：写入侧对 dataset 的 ref_property 仍然 400（#837 之前就是），
			// 但库里若已有这种行（迁移或直接写库留下的），构建不该因此建不起来
			name:     "accepts legacy self-referencing feature on a dataset",
			category: interfaces.ResourceCategoryDataset,
			schema: []*interfaces.Property{{
				Name: "content", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "content"}},
			}},
		},
		{
			name:     "rejects feature unsupported by property type",
			category: interfaces.ResourceCategoryTable,
			schema: []*interfaces.Property{{
				Name: "id", Type: interfaces.DataType_Integer,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword}},
			}},
			wantErr: "does not support feature type",
		},
		{
			name:     "rejects mismatched ref property type",
			category: interfaces.ResourceCategoryTable,
			schema: []*interfaces.Property{
				{Name: "embedding", Type: interfaces.DataType_Vector},
				{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "embedding"}}},
			},
			wantErr: "incompatible with feature type",
		},
		{
			name:     "accepts optional ref property on original resource",
			category: interfaces.ResourceCategoryTable,
			schema: []*interfaces.Property{{
				Name: "content", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBuildTaskSchemaFeatures(tt.category, tt.schema)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func buildTaskWithVector(field string) *interfaces.BuildTask {
	return &interfaces.BuildTask{
		IndexConfig: &interfaces.BuildTaskIndexConfig{
			Features: map[string]interfaces.BuildTaskFieldIndexFeature{
				field: {Vector: &interfaces.BuildTaskEmbeddingConfig{ModelID: "m1", Dimensions: 1024}},
			},
		},
	}
}

func TestBuildLocalIndexSchemaAppliesTaskIndexConfigWithoutMutatingResourceSchema(t *testing.T) {
	t.Run("single analyzer and vector field", func(t *testing.T) {
		res := &interfaces.Resource{ID: "r1", SchemaDefinition: []*interfaces.Property{
			{Name: "title", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
			}},
			{Name: "body", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "vector", FeatureType: interfaces.PropertyFeatureType_Vector},
			}},
		}}
		task := &interfaces.BuildTask{
			ID: "t1",
			IndexConfig: &interfaces.BuildTaskIndexConfig{
				Features: map[string]interfaces.BuildTaskFieldIndexFeature{
					"title": {Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}},
					"body":  {Vector: &interfaces.BuildTaskEmbeddingConfig{ModelID: "m1", Dimensions: 1024}},
				},
			},
		}

		schema, err := buildLocalIndexSchema(task, res)
		require.NoError(t, err)

		require.Len(t, schema[0].Features, 1)
		assert.Equal(t, interfaces.PropertyFeatureType_Fulltext, schema[0].Features[0].FeatureType)
		assert.Equal(t, "ik_max_word", schema[0].Features[0].Config["analyzer"])
		require.Len(t, schema, 3)
		assert.Equal(t, "body_vector", schema[2].Name)
		assert.Equal(t, interfaces.DataType_Vector, schema[2].Type)
		assert.Equal(t, 1024, schema[2].Features[0].Config["dimension"])
		assert.Nil(t, res.SchemaDefinition[0].Features[0].Config)
		assert.Len(t, res.SchemaDefinition[1].Features, 1)
	})

	t.Run("keeps different analyzers and vector dimensions per field", func(t *testing.T) {
		res := &interfaces.Resource{ID: "r1", SchemaDefinition: []*interfaces.Property{
			{Name: "title", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
				{FeatureName: "vector", FeatureType: interfaces.PropertyFeatureType_Vector},
			}},
			{Name: "body", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
				{FeatureName: "vector", FeatureType: interfaces.PropertyFeatureType_Vector},
			}},
		}}
		task := &interfaces.BuildTask{
			ID: "t1",
			IndexConfig: &interfaces.BuildTaskIndexConfig{
				Features: map[string]interfaces.BuildTaskFieldIndexFeature{
					"title": {
						Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"},
						Vector:   &interfaces.BuildTaskEmbeddingConfig{ModelID: "m1", Dimensions: 768},
					},
					"body": {
						Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "standard"},
						Vector:   &interfaces.BuildTaskEmbeddingConfig{ModelID: "m2", Dimensions: 1024},
					},
				},
			},
		}

		schema, err := buildLocalIndexSchema(task, res)
		require.NoError(t, err)

		assert.Equal(t, "ik_max_word", schema[0].Features[0].Config["analyzer"])
		assert.Equal(t, "standard", schema[1].Features[0].Config["analyzer"])
		vectorDimensions := map[string]any{}
		for _, prop := range schema {
			if prop.Type == interfaces.DataType_Vector {
				vectorDimensions[prop.Name] = prop.Features[0].Config["dimension"]
			}
		}
		assert.Equal(t, map[string]any{
			"title_vector": 768,
			"body_vector":  1024,
		}, vectorDimensions)
		assert.Nil(t, res.SchemaDefinition[0].Features[0].Config)
		assert.Nil(t, res.SchemaDefinition[1].Features[0].Config)
	})
}

func workerAccountFromCtx(ctx context.Context) (interfaces.AccountInfo, bool) {
	ai, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return ai, ok
}
