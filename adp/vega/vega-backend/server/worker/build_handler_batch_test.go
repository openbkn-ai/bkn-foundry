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
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusRunning, Creator: creator,
		}, nil)
		resAccess.EXPECT().GetByID(gomock.Any(), "r1").Return(&interfaces.Resource{ID: "r1", CatalogID: "c1"}, nil)
		taskAccess.EXPECT().UpdateStatus(gomock.Any(), "t1", gomock.Any()).Return(nil).AnyTimes()

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

func TestReconcileFulltextFeatures(t *testing.T) {
	t.Run("adds selected field and is idempotent", func(t *testing.T) {
		res := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
			{Name: "team_name", Type: interfaces.DataType_String},
			{Name: "team_code", Type: interfaces.DataType_String},
		}}

		changed := reconcileFulltextFeatures(res, "team_name", "ik_max_word")

		require.True(t, changed)
		tn := res.SchemaDefinition[0]
		require.Len(t, tn.Features, 1)
		assert.Equal(t, interfaces.PropertyFeatureType_Fulltext, tn.Features[0].FeatureType)
		assert.Equal(t, "ik_max_word", tn.Features[0].Config["analyzer"])
		assert.Empty(t, res.SchemaDefinition[1].Features)
		assert.False(t, reconcileFulltextFeatures(res, "team_name", "ik_max_word"))
	})

	t.Run("removes stale fields", func(t *testing.T) {
		res := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
			{Name: "team_name", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
			}},
			{Name: "federation_name", Type: interfaces.DataType_String},
		}}

		changed := reconcileFulltextFeatures(res, "federation_name", "standard")

		require.True(t, changed)
		assert.False(t, hasFulltextFeature(res.SchemaDefinition[0]))
		assert.True(t, hasFulltextFeature(res.SchemaDefinition[1]))
		require.True(t, reconcileFulltextFeatures(res, "", ""))
		assert.False(t, hasFulltextFeature(res.SchemaDefinition[1]))
	})

	t.Run("updates analyzer", func(t *testing.T) {
		res := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
			{Name: "x", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
			}},
		}}

		require.True(t, reconcileFulltextFeatures(res, "x", "ik_max_word"))
		assert.Equal(t, "ik_max_word", res.SchemaDefinition[0].Features[0].Config["analyzer"])
	})

	t.Run("leaves config nil when analyzer is empty", func(t *testing.T) {
		res := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
			{Name: "x", Type: interfaces.DataType_String},
		}}

		reconcileFulltextFeatures(res, "x", "")

		assert.Nil(t, res.SchemaDefinition[0].Features[0].Config)
	})
}

func TestBuildResourceForTaskDoesNotMutateResourceSchema(t *testing.T) {
	res := &interfaces.Resource{ID: "r1", SchemaDefinition: []*interfaces.Property{
		{Name: "title", Type: interfaces.DataType_String},
		{Name: "body", Type: interfaces.DataType_String},
	}}
	task := &interfaces.BuildTask{
		ID:               "t1",
		FulltextFields:   "title",
		FulltextAnalyzer: "ik_max_word",
	}

	buildRes, err := buildResourceForTask(res, task)
	require.NoError(t, err)

	require.Len(t, buildRes.SchemaDefinition[0].Features, 1)
	assert.Equal(t, interfaces.PropertyFeatureType_Fulltext, buildRes.SchemaDefinition[0].Features[0].FeatureType)
	assert.Equal(t, "ik_max_word", buildRes.SchemaDefinition[0].Features[0].Config["analyzer"])
	assert.Empty(t, res.SchemaDefinition[0].Features)
	assert.Empty(t, res.SchemaDefinition[1].Features)
}

func TestFieldNameSet(t *testing.T) {
	t.Run("trims and skips empty entries", func(t *testing.T) {
		got := fieldNameSet(" a, b ,, c ")

		assert.Equal(t, map[string]bool{"a": true, "b": true, "c": true}, got)
	})
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
