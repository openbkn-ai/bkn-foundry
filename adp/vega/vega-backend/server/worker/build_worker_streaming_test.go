// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestStreamingBuildWorkerRun(t *testing.T) {
	t.Run("injects creator into downstream context", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		sh := &streamingBuildWorker{bts: bts, rs: rs, cs: cs, lim: lim}
		creator := interfaces.AccountInfo{ID: "u1", Type: "user"}

		task := &interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusPending, Creator: creator,
		}
		rs.EXPECT().InternalGetByID(gomock.Any(), "r1").Return(&interfaces.Resource{ID: "r1", CatalogID: "c1"}, nil)

		var gotAccount interfaces.AccountInfo
		var hasAccount bool
		cs.EXPECT().InternalGetByID(gomock.Any(), "c1", true).DoAndReturn(
			func(ctx context.Context, id string, withSensitiveFields bool) (*interfaces.Catalog, error) {
				gotAccount, hasAccount = workerAccountFromCtx(ctx)
				return nil, errors.New("forbidden")
			})

		err := sh.Run(context.Background(), task)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "get catalog failed")
		require.True(t, hasAccount)
		assert.Equal(t, creator, gotAccount)
	})

	t.Run("cancels task when resource was deleted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		worker := &streamingBuildWorker{bts: bts, rs: rs}

		task := &interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusPending,
		}
		rs.EXPECT().InternalGetByID(gomock.Any(), "r1").Return(nil, nil)
		bts.EXPECT().InternalMarkCancelled(gomock.Any(), "t1", "resource deleted").Return(true, nil)

		require.NoError(t, worker.Run(context.Background(), task))
	})

	t.Run("marks task failed for invalid streaming connector configuration", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		sh := &streamingBuildWorker{bts: bts, rs: rs, cs: cs}

		task := &interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusPending,
		}
		rs.EXPECT().InternalGetByID(gomock.Any(), "r1").Return(&interfaces.Resource{ID: "r1", CatalogID: "c1"}, nil)
		cs.EXPECT().InternalGetByID(gomock.Any(), "c1", true).Return(&interfaces.Catalog{
			ID:            "c1",
			Enabled:       true,
			ConnectorType: interfaces.ConnectorTypePostgreSQL,
			ConnectorCfg:  interfaces.ConnectorConfig{},
		}, nil)
		bts.EXPECT().InternalMarkFailed(gomock.Any(), "t1", "PostgreSQL streaming build requires connector_config.database").
			Return(true, nil)

		require.NoError(t, sh.Run(context.Background(), task))
	})
}

func TestStreamingDatabase(t *testing.T) {
	t.Run("returns PostgreSQL connection database", func(t *testing.T) {
		database, err := streamingDatabase(&interfaces.Catalog{
			ConnectorType: interfaces.ConnectorTypePostgreSQL,
			ConnectorCfg:  interfaces.ConnectorConfig{"database": "app"},
		})

		require.NoError(t, err)
		assert.Equal(t, "app", database)
	})

	t.Run("rejects PostgreSQL without connection database", func(t *testing.T) {
		_, err := streamingDatabase(&interfaces.Catalog{
			ConnectorType: interfaces.ConnectorTypePostgreSQL,
			ConnectorCfg:  interfaces.ConnectorConfig{},
		})

		require.ErrorContains(t, err, "connector_config.database")
	})

	t.Run("returns MySQL database include list from databases", func(t *testing.T) {
		database, err := streamingDatabase(&interfaces.Catalog{
			ConnectorType: interfaces.ConnectorTypeMySQL,
			ConnectorCfg:  interfaces.ConnectorConfig{"databases": []any{"app", "analytics"}},
		})

		require.NoError(t, err)
		assert.Equal(t, "app,analytics", database)
	})

	t.Run("rejects MySQL instance-level catalog", func(t *testing.T) {
		_, err := streamingDatabase(&interfaces.Catalog{
			ConnectorType: interfaces.ConnectorTypeMySQL,
			ConnectorCfg:  interfaces.ConnectorConfig{"databases": []any{}},
		})

		require.ErrorContains(t, err, "non-empty connector_config.databases")
	})
}

func TestGetKafkaKeyValuesUsesConfiguredDocumentIDFields(t *testing.T) {
	values, err := getKafkaKeyValues([]string{"id", "payload"}, map[string]any{
		"id":      1,
		"payload": map[string]any{"region": "cn"},
	})

	require.NoError(t, err)
	assert.Equal(t, []interfaces.KeyValue{
		{Key: "id", Value: 1},
		{Key: "payload", Value: map[string]any{"region": "cn"}},
	}, values)
}

func TestHandleUpdateOperationWritesReplacementBeforeDeletingOldDocument(t *testing.T) {
	ctrl := gomock.NewController(t)
	lim := vmock.NewMockLocalIndexManager(ctrl)
	worker := &streamingBuildWorker{lim: lim}
	buildTask := &interfaces.BuildTask{IndexConfig: &interfaces.BuildTaskIndexConfig{BuildKeyFields: []string{"id"}}}
	oldID, err := generateDocumentID([]interfaces.KeyValue{{Key: "id", Value: 1}})
	require.NoError(t, err)
	newID, err := generateDocumentID([]interfaces.KeyValue{{Key: "id", Value: 2}})
	require.NoError(t, err)

	gomock.InOrder(
		lim.EXPECT().IndexDocuments(gomock.Any(), "index-1", map[string]map[string]any{newID: {"id": 2, "title": "updated"}}).Return([]string{newID}, nil),
		lim.EXPECT().DeleteDocument(gomock.Any(), "index-1", oldID).Return(nil),
	)

	require.NoError(t, worker.handleUpdateOperation(
		context.Background(),
		map[string]any{"id": 1},
		map[string]any{"id": 2, "title": "updated"},
		"index-1",
		buildTask,
		&embeddingPipeline{},
	))
}

func TestHandleUpdateOperationKeepsOldDocumentWhenReplacementWriteFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	lim := vmock.NewMockLocalIndexManager(ctrl)
	worker := &streamingBuildWorker{lim: lim}
	buildTask := &interfaces.BuildTask{IndexConfig: &interfaces.BuildTaskIndexConfig{BuildKeyFields: []string{"id"}}}
	newID, err := generateDocumentID([]interfaces.KeyValue{{Key: "id", Value: 2}})
	require.NoError(t, err)

	lim.EXPECT().IndexDocuments(gomock.Any(), "index-1", map[string]map[string]any{newID: {"id": 2}}).Return(nil, errors.New("write failed"))

	err = worker.handleUpdateOperation(
		context.Background(),
		map[string]any{"id": 1},
		map[string]any{"id": 2},
		"index-1",
		buildTask,
		&embeddingPipeline{},
	)
	require.ErrorContains(t, err, "write failed")
}

func TestBuildConnectorConfigUsesCaptureDatabase(t *testing.T) {
	worker := &streamingBuildWorker{appSetting: &common.AppSetting{}}

	t.Run("uses database include list for MySQL", func(t *testing.T) {
		config := worker.buildConnectorConfig("build-c1", &interfaces.Catalog{
			ID:            "c1",
			ConnectorType: interfaces.ConnectorTypeMySQL,
			ConnectorCfg:  interfaces.ConnectorConfig{},
		}, "app,analytics", "")

		assert.Equal(t, "app,analytics", config["config"].(map[string]any)["database.include.list"])
	})

	t.Run("uses connection database for PostgreSQL", func(t *testing.T) {
		config := worker.buildConnectorConfig("build-c1", &interfaces.Catalog{
			ID:            "c1",
			ConnectorType: interfaces.ConnectorTypePostgreSQL,
			ConnectorCfg:  interfaces.ConnectorConfig{},
		}, "app", "")

		assert.Equal(t, "app", config["config"].(map[string]any)["database.dbname"])
	})
}
