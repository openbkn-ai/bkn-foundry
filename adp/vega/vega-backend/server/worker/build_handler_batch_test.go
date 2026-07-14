// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestBatchBuildHandlerHandleTask(t *testing.T) {
	t.Run("injects creator into downstream context", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		taskAccess := vmock.NewMockBuildTaskAccess(ctrl)
		resAccess := vmock.NewMockResourceAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		lim.EXPECT().CheckExist(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
		bh := &batchBuildHandler{taskAccess: taskAccess, resAccess: resAccess, cs: cs, lim: lim}
		creator := interfaces.AccountInfo{ID: "u1", Type: "user"}

		taskAccess.EXPECT().GetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusInit, Creator: creator,
		}, nil)
		taskAccess.EXPECT().UpdateStatus(gomock.Any(), nil, "t1",
			interfaces.NewBuildTaskUpdate().
				WithStatus(interfaces.BuildTaskStatusRunning).
				WithErrorMsg(""),
			interfaces.BuildTaskStatusInit).
			Return(true, nil)
		resAccess.EXPECT().GetByID(gomock.Any(), "r1").Return(&interfaces.Resource{ID: "r1", CatalogID: "c1"}, nil)
		taskAccess.EXPECT().UpdateStatus(gomock.Any(), nil, "t1", gomock.Any()).Return(true, nil).AnyTimes()

		var gotAccount interfaces.AccountInfo
		var hasAccount bool
		cs.EXPECT().GetByID(gomock.Any(), "c1", true).DoAndReturn(
			func(ctx context.Context, id string, withSensitiveFields bool) (*interfaces.Catalog, error) {
				gotAccount, hasAccount = workerAccountFromCtx(ctx)
				return nil, errors.New("forbidden")
			})

		task := asynq.NewTask("build:batch", workerBuildTaskPayload(t, interfaces.BatchBuildTaskMessage{TaskID: "t1"}))
		require.NoError(t, bh.HandleTask(context.Background(), task))
		require.True(t, hasAccount)
		assert.Equal(t, creator, gotAccount)
	})

	t.Run("skips duplicate message when task is already claimed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		taskAccess := vmock.NewMockBuildTaskAccess(ctrl)
		bh := &batchBuildHandler{taskAccess: taskAccess}

		taskAccess.EXPECT().GetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusInit,
		}, nil)
		taskAccess.EXPECT().UpdateStatus(gomock.Any(), nil, "t1",
			interfaces.NewBuildTaskUpdate().
				WithStatus(interfaces.BuildTaskStatusRunning).
				WithErrorMsg(""),
			interfaces.BuildTaskStatusInit).
			Return(false, nil)

		task := asynq.NewTask("build:batch", workerBuildTaskPayload(t, interfaces.BatchBuildTaskMessage{TaskID: "t1"}))
		require.NoError(t, bh.HandleTask(context.Background(), task))
	})

	t.Run("does not switch local index when build fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		taskAccess := vmock.NewMockBuildTaskAccess(ctrl)
		resAccess := vmock.NewMockResourceAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		lim.EXPECT().CheckExist(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
		bh := &batchBuildHandler{taskAccess: taskAccess, resAccess: resAccess, cs: cs, lim: lim}

		resource := &interfaces.Resource{
			ID:             "r1",
			CatalogID:      "c1",
			LocalIndexName: interfaces.BuildIndexName("r1", "old-task"),
		}
		taskAccess.EXPECT().GetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusInit,
		}, nil)
		taskAccess.EXPECT().UpdateStatus(gomock.Any(), nil, "t1",
			interfaces.NewBuildTaskUpdate().
				WithStatus(interfaces.BuildTaskStatusRunning).
				WithErrorMsg(""),
			interfaces.BuildTaskStatusInit).
			Return(true, nil)
		resAccess.EXPECT().GetByID(gomock.Any(), "r1").Return(resource, nil)
		cs.EXPECT().GetByID(gomock.Any(), "c1", true).Return(nil, errors.New("catalog down"))
		taskAccess.EXPECT().UpdateStatus(gomock.Any(), nil, "t1",
			interfaces.NewBuildTaskUpdate().
				WithStatus(interfaces.BuildTaskStatusFailed).
				WithErrorMsg("get catalog failed: catalog down")).
			Return(true, nil)

		task := asynq.NewTask("build:batch", workerBuildTaskPayload(t, interfaces.BatchBuildTaskMessage{TaskID: "t1"}))
		require.NoError(t, bh.HandleTask(context.Background(), task))
		assert.Equal(t, interfaces.BuildIndexName("r1", "old-task"), resource.LocalIndexName)
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
}

func workerAccountFromCtx(ctx context.Context) (interfaces.AccountInfo, bool) {
	ai, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return ai, ok
}

func workerBuildTaskPayload(t *testing.T, msg any) []byte {
	t.Helper()

	payload, err := sonic.Marshal(msg)
	require.NoError(t, err)
	return payload
}
