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

func TestUpdateResourceIndexName(t *testing.T) {
	t.Run("updates empty old index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		resource := &interfaces.Resource{ID: "r1"}

		rs.EXPECT().InternalUpdateLocalIndexName(gomock.Any(), nil, "r1", "new-index").DoAndReturn(func(_ context.Context, _ *sql.Tx, id, indexName string) error {
			assert.Equal(t, "r1", id)
			assert.Equal(t, "new-index", indexName)
			return nil
		})

		require.NoError(t, updateResourceIndexName(context.Background(), resource, rs, "new-index"))
	})

	t.Run("skips unchanged index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: "same-index"}

		require.NoError(t, updateResourceIndexName(context.Background(), resource, rs, "same-index"))
	})

	t.Run("keeps old index after update failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: "old-index"}

		rs.EXPECT().InternalUpdateLocalIndexName(gomock.Any(), nil, "r1", "new-index").DoAndReturn(func(_ context.Context, _ *sql.Tx, id, indexName string) error {
			assert.Equal(t, "r1", id)
			assert.Equal(t, "new-index", indexName)
			return errors.New("update failed")
		})

		err := updateResourceIndexName(context.Background(), resource, rs, "new-index")

		require.Error(t, err)
		assert.Equal(t, "old-index", resource.LocalIndexName)
	})
}

func TestCompleteBuildTaskWithoutEmbedding(t *testing.T) {
	t.Run("completes task and resource update atomically", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ts := vmock.NewMockBuildTaskService(ctrl)
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: "old-index"}

		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		mock.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalUpdateLocalIndexName(gomock.Any(), txMatcher, "r1", "new-index").
			DoAndReturn(func(_ context.Context, _ *sql.Tx, id, indexName string) error {
				assert.Equal(t, "r1", id)
				assert.Equal(t, "new-index", indexName)
				return nil
			})
		ts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, "t1").Return(true, nil)
		mock.ExpectCommit()

		err = completeBuildTaskWithoutEmbedding(context.Background(), resource, rs, ts, "t1", "new-index")

		require.NoError(t, err)
		assert.Equal(t, "new-index", resource.LocalIndexName)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("finishes concurrent stop request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ts := vmock.NewMockBuildTaskService(ctrl)
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: "old-index"}

		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		mock.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalUpdateLocalIndexName(gomock.Any(), txMatcher, "r1", "new-index").Return(nil)
		ts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, "t1").Return(false, nil)
		mock.ExpectRollback()
		ts.EXPECT().InternalMarkStopped(gomock.Any(), "t1").Return(true, nil)

		err = completeBuildTaskWithoutEmbedding(context.Background(), resource, rs, ts, "t1", "new-index")

		require.NoError(t, err)
		assert.Equal(t, "old-index", resource.LocalIndexName)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("preserves semantic metadata when build completes from stale snapshot", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ts := vmock.NewMockBuildTaskService(ctrl)
		catalogResource := &interfaces.Resource{
			ID:               "r1",
			Name:             "supply_chain.material_entity",
			SourceIdentifier: "supply_chain.material_entity",
			Description:      "source material description",
			SourceMetadata: map[string]any{
				"original_description": "source material description",
			},
			SchemaDefinition: []*interfaces.Property{{
				Name:                "material_id",
				DisplayName:         "material_id",
				Description:         "source material identifier",
				OriginalDescription: "source material identifier",
			}},
		}
		staleBuildResource := &interfaces.Resource{
			ID:               "r1",
			Name:             "supply_chain.material_entity",
			SourceIdentifier: "supply_chain.material_entity",
			Description:      "source material description",
			SchemaDefinition: []*interfaces.Property{{
				Name:                "material_id",
				DisplayName:         "material_id",
				Description:         "source material identifier",
				OriginalDescription: "source material identifier",
			}},
		}
		semanticWorker := &SemanticUnderstandingTaskWorker{rs: rs}
		semanticTask := &interfaces.SemanticUnderstandingTask{
			ResourceID:          "r1",
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeFillEmpty,
			ConfidenceThreshold: 0.75,
		}

		rs.EXPECT().GetByID(gomock.Any(), "r1").Return(catalogResource, nil)
		rs.EXPECT().UpdateResource(gomock.Any(), catalogResource).Return(nil)
		applied, err := semanticWorker.applyResourceResult(context.Background(), semanticTask, `{"confidence":1,"resource":{"display_name":"物料","description":"物料主数据"},"fields":[{"name":"material_id","display_name":"物料ID","description":"物料唯一标识"}]}`, nil)
		require.NoError(t, err)
		assert.True(t, applied.Applied)

		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		oldDB := logics.DB
		logics.DB = db
		defer func() { logics.DB = oldDB }()

		mock.ExpectBegin()
		txMatcher := gomock.AssignableToTypeOf(&sql.Tx{})
		rs.EXPECT().InternalUpdateLocalIndexName(gomock.Any(), txMatcher, "r1", "new-index").DoAndReturn(
			func(_ context.Context, _ *sql.Tx, id, indexName string) error {
				assert.Equal(t, "r1", id)
				assert.Equal(t, "物料", catalogResource.Name)
				assert.Equal(t, "物料主数据", catalogResource.Description)
				assert.Equal(t, "物料ID", catalogResource.SchemaDefinition[0].DisplayName)
				assert.Equal(t, "物料唯一标识", catalogResource.SchemaDefinition[0].Description)
				catalogResource.LocalIndexName = indexName
				return nil
			},
		)
		ts.EXPECT().InternalMarkCompleted(gomock.Any(), txMatcher, "build-task-1").Return(true, nil)
		mock.ExpectCommit()

		require.NoError(t, completeBuildTaskWithoutEmbedding(context.Background(), staleBuildResource, rs, ts, "build-task-1", "new-index"))
		assert.Equal(t, "物料", catalogResource.Name)
		assert.Equal(t, "物料主数据", catalogResource.Description)
		assert.Equal(t, "物料ID", catalogResource.SchemaDefinition[0].DisplayName)
		assert.Equal(t, "物料唯一标识", catalogResource.SchemaDefinition[0].Description)
		assert.Equal(t, "new-index", catalogResource.LocalIndexName)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
