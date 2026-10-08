// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"go.uber.org/mock/gomock"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/require"
)

func TestDiscoverStatusAfterEnrichPreservesExactCount(t *testing.T) {
	for _, count := range []any{int64(9007199254740993), json.Number("9007199254740993")} {
		resource := &interfaces.Resource{SourceMetadata: map[string]any{
			"properties": map[string]any{"row_count": count, "row_count_time": int64(100)},
		}}
		beforeHash := sourceSnapshotHash(resource)

		status := discoverStatusAfterEnrich(resource, beforeHash)

		require.Equal(t, interfaces.DiscoverStatusUnchanged, status)
		require.Equal(t, beforeHash, sourceSnapshotHash(resource))
		properties := resource.SourceMetadata["properties"].(map[string]any)
		require.Equal(t, json.Number("9007199254740993"), properties["row_count"])
		require.Equal(t, json.Number("100"), properties["row_count_time"])
	}
}

func TestSourceSnapshotHashIgnoresIndependentStatistics(t *testing.T) {
	count, countTime := int64(42), int64(100)
	resource := &interfaces.Resource{RowCount: &count, RowCountTime: &countTime,
		SourceMetadata: map[string]any{"properties": map[string]any{"estimated_row_count": int64(40)}},
	}
	before := sourceSnapshotHash(resource)
	count = 43
	countTime = 200
	require.Equal(t, before, sourceSnapshotHash(resource))
	resource.SourceMetadata["properties"].(map[string]any)["estimated_row_count"] = int64(50)
	require.NotEqual(t, before, sourceSnapshotHash(resource))
}

func TestCountOnlyActions(t *testing.T) {
	for _, tt := range []struct {
		name    string
		actions *interfaces.DiscoverActions
		want    bool
	}{
		{"nil", nil, false},
		{"none", &interfaces.DiscoverActions{}, false},
		{"count", &interfaces.DiscoverActions{Count: true}, true},
		{"full sync and count", &interfaces.DiscoverActions{Create: true, Refresh: true, MarkStale: true, Count: true}, false},
		{"create and count", &interfaces.DiscoverActions{Create: true, Count: true}, false},
		{"refresh and count", &interfaces.DiscoverActions{Refresh: true, Count: true}, false},
		{"cleanup and count", &interfaces.DiscoverActions{MarkStale: true, Count: true}, false},
	} {
		t.Run(tt.name, func(t *testing.T) { require.Equal(t, tt.want, countOnlyActions(tt.actions)) })
	}
}

func expectDiscoverResourceTransaction(t *testing.T, commit bool) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	previous := logics.DB
	logics.DB = db
	mock.ExpectBegin()
	if commit {
		mock.ExpectCommit()
	} else {
		mock.ExpectRollback()
	}
	t.Cleanup(func() {
		logics.DB = previous
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return mock
}

func TestSaveDiscoveredResourceTransaction(t *testing.T) {
	for _, scenario := range []string{"metadata only", "success", "begin failure", "metadata failure", "count failure", "commit failure"} {
		t.Run(scenario, func(t *testing.T) {
			rs := vmock.NewMockResourceService(gomock.NewController(t))
			worker := &DiscoverTaskWorker{rs: rs}
			resource := &interfaces.Resource{ID: "r1", UpdateTime: 43}
			if scenario == "metadata only" {
				rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, resource, int64(42)).Return(nil)
				require.NoError(t, worker.saveDiscoveredResource(context.Background(), resource, 42, nil, 1234))
				return
			}
			db, mock, dbErr := sqlmock.New()
			require.NoError(t, dbErr)
			previous := logics.DB
			logics.DB = db
			t.Cleanup(func() {
				logics.DB = previous
				require.NoError(t, mock.ExpectationsWereMet())
				_ = db.Close()
			})
			if scenario == "begin failure" {
				mock.ExpectBegin().WillReturnError(errors.New("begin failed"))
				count := int64(0)
				require.ErrorContains(t, worker.saveDiscoveredResource(context.Background(), resource, 42, &count, 1234), "begin failed")
				return
			}
			mock.ExpectBegin()
			switch scenario {
			case "success":
				mock.ExpectCommit()
			case "commit failure":
				mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
			default:
				mock.ExpectRollback()
			}
			var metadataErr, countErr error
			if scenario == "metadata failure" {
				metadataErr = errors.New("metadata failed")
			}
			if scenario == "count failure" {
				countErr = errors.New("count failed")
			}
			var transaction *sql.Tx
			metadata := rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), gomock.Not(gomock.Nil()), resource, int64(42)).DoAndReturn(func(_ context.Context, tx *sql.Tx, _ *interfaces.Resource, _ int64) error {
				transaction = tx
				return metadataErr
			})
			if metadataErr == nil {
				rs.EXPECT().InternalUpdateRowCount(gomock.Any(), gomock.Not(gomock.Nil()), resource, int64(0), int64(1234)).DoAndReturn(func(_ context.Context, tx *sql.Tx, _ *interfaces.Resource, _, _ int64) error {
					require.Same(t, transaction, tx)
					return countErr
				}).After(metadata)
			}
			count := int64(0)
			err := worker.saveDiscoveredResource(context.Background(), resource, 42, &count, 1234)
			if scenario == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
