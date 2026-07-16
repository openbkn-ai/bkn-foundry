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

		rs.EXPECT().InternalUpdate(gomock.Any(), nil, resource).DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource) error {
			assert.Equal(t, "new-index", got.LocalIndexName)
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

		rs.EXPECT().InternalUpdate(gomock.Any(), nil, resource).DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource) error {
			assert.Equal(t, "new-index", got.LocalIndexName)
			return errors.New("update failed")
		})

		err := updateResourceIndexName(context.Background(), resource, rs, "new-index")

		require.Error(t, err)
		assert.Equal(t, "old-index", resource.LocalIndexName)
	})
}

func TestCompleteBuildTaskWithoutEmbedding(t *testing.T) {
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
	rs.EXPECT().InternalUpdate(gomock.Any(), txMatcher, resource).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource) error {
			assert.Equal(t, "new-index", got.LocalIndexName)
			return nil
		})
	ts.EXPECT().InternalUpdateStatus(gomock.Any(), txMatcher, "t1", gomock.AssignableToTypeOf(interfaces.BuildTaskUpdate{})).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ string, update interfaces.BuildTaskUpdate, _ ...string) (bool, error) {
			require.NotNil(t, update.Status)
			assert.Equal(t, interfaces.BuildTaskStatusCompleted, *update.Status)
			return true, nil
		})
	mock.ExpectCommit()

	err = completeBuildTaskWithoutEmbedding(context.Background(), resource, rs, ts, "t1", "new-index")

	require.NoError(t, err)
	assert.Equal(t, "new-index", resource.LocalIndexName)
	require.NoError(t, mock.ExpectationsWereMet())
}
