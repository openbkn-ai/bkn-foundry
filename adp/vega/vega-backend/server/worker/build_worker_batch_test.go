// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
	"vega-backend/logics"
)

func TestBuildBatchCursorFilter(t *testing.T) {
	filter := buildBatchCursorFilter(
		[]string{"customer_id", "id"},
		[]interfaces.KeyValue{{Key: "customer_id", Value: "customer-1"}, {Key: "id", Value: 100}},
	)

	require.Equal(t, "or", filter.Operation)
	require.Len(t, filter.SubConds, 2)
	assert.Equal(t, &interfaces.FilterCondCfg{
		Operation: "and",
		SubConds: []*interfaces.FilterCondCfg{
			{Name: "customer_id", Operation: "gt", ValueOptCfg: interfaces.ValueOptCfg{Value: "customer-1", ValueFrom: interfaces.ValueFrom_Const}},
		},
	}, filter.SubConds[0])
	assert.Equal(t, &interfaces.FilterCondCfg{
		Operation: "and",
		SubConds: []*interfaces.FilterCondCfg{
			{Name: "customer_id", Operation: "==", ValueOptCfg: interfaces.ValueOptCfg{Value: "customer-1", ValueFrom: interfaces.ValueFrom_Const}},
			{Name: "id", Operation: "gt", ValueOptCfg: interfaces.ValueOptCfg{Value: 100, ValueFrom: interfaces.ValueFrom_Const}},
		},
	}, filter.SubConds[1])
}

func TestBatchBuildWorkerHandleTask(t *testing.T) {
	t.Run("does not switch local index when build fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bbw := &batchBuildWorker{bts: bts, lim: lim}

		resource := workerTestResource()
		resource.LocalIndexName = buildIndexName("r1", "old-task")
		task := workerTestFullTask(t, resource)
		task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
		task.Status = interfaces.BuildTaskStatusPending
		lim.EXPECT().CheckIndexExist(gomock.Any(), buildIndexName("r1", "old-task")).
			Return(false, errors.New("opensearch unavailable"))
		bts.EXPECT().InternalMarkFailed(gomock.Any(), nil, "t1",
			"prepare local index failed: check local index exist failed: opensearch unavailable").
			Return(true, nil)

		require.NoError(t, bbw.Run(context.Background(), task, resource, &interfaces.Catalog{Enabled: true}))
		assert.Equal(t, buildIndexName("r1", "old-task"), resource.LocalIndexName)
	})

}

func TestBatchBuildWorkerExecuteBuild(t *testing.T) {
	t.Run("does not invoke model when index creation fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
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
			ID: "t1", ExecuteType: interfaces.BuildTaskExecuteTypeFull,
			IndexConfig: &interfaces.BuildTaskIndexConfig{Features: map[string]interfaces.BuildTaskFieldIndexFeature{
				"content": {Vector: &interfaces.SmallModel{ModelID: "m1", EmbeddingDim: 3}},
			}},
		}

		lim.EXPECT().CheckIndexExist(gomock.Any(), buildIndexName("r1", "t1")).Return(false, nil)
		lim.EXPECT().CreateIndex(gomock.Any(), buildIndexName("r1", "t1"), gomock.Any()).
			Return(errors.New("opensearch unavailable"))

		err := bbw.executeBuild(context.Background(), &interfaces.Catalog{ID: "c1"}, resource, buildTask)
		require.Error(t, err)
		assert.ErrorContains(t, err, "prepare local index failed: opensearch unavailable")
	})

	t.Run("empty full build publishes an established empty checkpoint", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		resource := workerTestResource()
		task := workerTestFullTask(t, resource)
		indexName := buildIndexName(resource.ID, task.ID)
		bbw := &batchBuildWorker{lim: lim, bts: bts, rs: rs, cf: cf}

		db, mockDB, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		lim.EXPECT().CheckIndexExist(gomock.Any(), indexName).Return(false, nil)
		lim.EXPECT().CreateIndex(gomock.Any(), indexName, gomock.Any()).Return(nil)
		var progressMarks []string
		bts.EXPECT().InternalSetProgress(gomock.Any(), nil, task.ID, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress) (bool, error) {
				if progress.SyncedMark != nil {
					progressMarks = append(progressMarks, *progress.SyncedMark)
				}
				return true, nil
			}).Times(2)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.QueryResult{Total: 0}, nil)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		mockDB.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(resource, nil)
		rs.EXPECT().InternalUpdateLocalIndexState(gomock.Any(), txMatcher, resource.ID,
			interfaces.ResourceLocalIndexStatusAvailable, indexName, `{"mode":"batch","cursor":[]}`).Return(true, nil)
		bts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, task.ID).Return(true, nil)
		mockDB.ExpectCommit()

		err = bbw.executeBuild(context.Background(), &interfaces.Catalog{ID: "c1", ConnectorType: "mysql"}, resource, task)

		require.NoError(t, err)
		assert.Equal(t, []string{"", `{"mode":"batch","cursor":[]}`}, progressMarks)
		assert.Equal(t, `{"mode":"batch","cursor":[]}`, resource.SyncMark)
		require.NoError(t, mockDB.ExpectationsWereMet())
	})

	t.Run("incremental writes current index and advances task and resource checkpoints atomically", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		resource := workerTestResource()
		resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{Name: "payload", Type: interfaces.DataType_Json})
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
		resource.LocalIndexName = "current-index"
		resource.SyncMark = `{"mode":"batch","cursor":[]}`
		task := workerTestFullTask(t, resource)
		task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
		task.Status = interfaces.BuildTaskStatusRunning
		task.SyncedMark = resource.SyncMark
		bbw := &batchBuildWorker{lim: lim, bts: bts, rs: rs, cf: cf}

		db, mockDB, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		lim.EXPECT().CheckIndexExist(gomock.Any(), "current-index").Return(true, nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
				assert.Nil(t, params.FilterCondCfg)
				return &interfaces.QueryResult{Total: 1, Entries: []map[string]any{{
					"id":      int64(1),
					"payload": []byte(`{"region":"cn"}`),
				}}}, nil
			})
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		indexed := false
		lim.EXPECT().IndexDocuments(gomock.Any(), "current-index", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, documents map[string]map[string]any) ([]string, error) {
				require.Len(t, documents, 1)
				for _, document := range documents {
					assert.Equal(t, map[string]any{"region": "cn"}, document["payload"])
				}
				indexed = true
				return nil, nil
			})
		newMark := `{"mode":"batch","cursor":[{"key":"id","value":1}]}`
		mockDB.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).DoAndReturn(
			func(context.Context, *sql.Tx, string) (*interfaces.Resource, error) {
				require.True(t, indexed, "checkpoint transaction must start after OpenSearch write")
				return resource, nil
			})
		bts.EXPECT().InternalSetProgress(gomock.Any(), txMatcher, task.ID, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, progress interfaces.BuildTaskProgress) (bool, error) {
				require.NotNil(t, progress.SyncedMark)
				assert.Equal(t, newMark, *progress.SyncedMark)
				return true, nil
			})
		rs.EXPECT().InternalUpdateLocalIndexState(gomock.Any(), txMatcher, resource.ID,
			interfaces.ResourceLocalIndexStatusAvailable, "current-index", newMark).Return(true, nil)
		mockDB.ExpectCommit()
		bts.EXPECT().InternalMarkCompleted(gomock.Any(), nil, task.ID).Return(true, nil)

		err = bbw.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)

		require.NoError(t, err)
		assert.Equal(t, newMark, task.SyncedMark)
		assert.Equal(t, newMark, resource.SyncMark)
		require.NoError(t, mockDB.ExpectationsWereMet())
	})

	t.Run("incremental with no new rows completes with initialized checkpoint", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		resource := workerTestResource()
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
		resource.LocalIndexName = "current-index"
		resource.SyncMark = `{"mode":"batch","cursor":[{"key":"id","value":10}]}`
		task := workerTestFullTask(t, resource)
		task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
		task.Status = interfaces.BuildTaskStatusRunning
		task.SyncedMark = resource.SyncMark
		bbw := &batchBuildWorker{lim: lim, bts: bts, cf: cf}

		lim.EXPECT().CheckIndexExist(gomock.Any(), "current-index").Return(true, nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteQuery(gomock.Any(), resource, gomock.Any()).Return(&interfaces.QueryResult{}, nil)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		bts.EXPECT().InternalGetStatusByID(gomock.Any(), task.ID).Return(interfaces.BuildTaskStatusRunning, nil)
		bts.EXPECT().InternalMarkCompleted(gomock.Any(), nil, task.ID).Return(true, nil)

		err := bbw.executeBuild(context.Background(), &interfaces.Catalog{ConnectorType: "mysql"}, resource, task)

		require.NoError(t, err)
		assert.Equal(t, resource.SyncMark, task.SyncedMark)
	})
}

func TestBatchBuildWorkerRejectsIncrementalCheckpointWhenIndexChanged(t *testing.T) {
	ctrl := gomock.NewController(t)
	bts := vmock.NewMockBuildTaskService(ctrl)
	rs := vmock.NewMockResourceService(ctrl)
	resource := workerTestResource()
	resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
	resource.LocalIndexName = "current-index"
	resource.SyncMark = `{"mode":"batch","cursor":[]}`
	current := *resource
	current.LocalIndexName = "replacement-index"
	task := workerTestFullTask(t, resource)
	task.ExecuteType = interfaces.BuildTaskExecuteTypeIncremental
	newMark := `{"mode":"batch","cursor":[{"key":"id","value":1}]}`
	progress := interfaces.BuildTaskProgress{SyncedMark: &newMark}
	bbw := &batchBuildWorker{bts: bts, rs: rs}

	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDB := logics.DB
	logics.DB = db
	defer func() { logics.DB = oldDB }()

	mockDB.ExpectBegin()
	txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
	rs.EXPECT().InternalGetByID(gomock.Any(), txMatcher, resource.ID).Return(&current, nil)
	mockDB.ExpectRollback()

	err = bbw.commitIncrementalProgress(context.Background(), resource, task,
		resource.LocalIndexName, resource.SyncMark, newMark, progress)

	require.ErrorContains(t, err, "resource local index changed during incremental build")
	assert.Equal(t, `{"mode":"batch","cursor":[]}`, resource.SyncMark)
	require.NoError(t, mockDB.ExpectationsWereMet())
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
				field: {Vector: &interfaces.SmallModel{ModelID: "m1", EmbeddingDim: 1024}},
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
					"body":  {Vector: &interfaces.SmallModel{ModelID: "m1", EmbeddingDim: 1024}},
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
						Vector:   &interfaces.SmallModel{ModelID: "m1", EmbeddingDim: 768},
					},
					"body": {
						Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "standard"},
						Vector:   &interfaces.SmallModel{ModelID: "m2", EmbeddingDim: 1024},
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

// 从没被 PUT 过的存量资源，库里仍是自引用形状。构建入口抹平之后必须能建索引，否则
// 「push 清掉 condition_operations -> 重建索引 -> 恢复」这条路仍然是断的。
func TestBuildLocalIndexSchemaNormalizesLegacySelfReference(t *testing.T) {
	// schema 里的 fulltext 特征必须同时在构建任务的 index config 里声明，与自引用无关
	fulltextTask := func(field string) *interfaces.BuildTask {
		return &interfaces.BuildTask{
			ID: "t1",
			IndexConfig: &interfaces.BuildTaskIndexConfig{
				Features: map[string]interfaces.BuildTaskFieldIndexFeature{
					field: {Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}},
				},
			},
		}
	}

	tests := []struct {
		name     string
		category string
		task     *interfaces.BuildTask
		props    []*interfaces.Property
	}{
		{
			name:     "fulltext feature referencing its own text field",
			category: interfaces.ResourceCategoryTable,
			task:     fulltextTask("title"),
			props: []*interfaces.Property{{
				Name: "title", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureName: "title_fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "title"}},
			}},
		},
		{
			// keyword 的 ref 类型要求是 string，text 字段上的自引用 keyword 特征
			// 在抹平前会撞上 ref 类型校验，抹平后按「特征作用于属性自身」通过
			name:     "keyword feature referencing its own text field",
			category: interfaces.ResourceCategoryTable,
			task:     &interfaces.BuildTask{ID: "t1"},
			props: []*interfaces.Property{{
				Name: "title", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureName: "title.keyword", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "title"}},
			}},
		},
		{
			// 入口严、存量宽：写入侧对 dataset 的 ref_property 仍然 400（#837 之前就是），
			// 但库里若已有这种行（迁移或直接写库留下的），构建不该因此建不起来
			name:     "any self-reference on a dataset",
			category: interfaces.ResourceCategoryDataset,
			task:     fulltextTask("content"),
			props: []*interfaces.Property{{
				Name: "content", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureName: "content_fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "content"}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &interfaces.Resource{Category: tt.category, SchemaDefinition: tt.props}

			schema, err := buildLocalIndexSchema(tt.task, res)

			require.NoError(t, err)
			require.Len(t, schema, 1)
			assert.Empty(t, schema[0].Features[0].RefProperty)
			// 只动深拷贝，资源行留给下一次更新自愈
			assert.Equal(t, tt.props[0].Name, res.SchemaDefinition[0].Features[0].RefProperty)
		})
	}
}
