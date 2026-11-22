// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/sync_checkpoint"
)

func TestBuildTaskWorkerPrepareIncrementalBaseline(t *testing.T) {
	t.Run("cancellation during the index lookup leaves the task unchanged", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		f := newIncrementalBaselineFixture(t, interfaces.DataType_Date, "date")
		task := &interfaces.BuildTask{ID: "incremental-task"}
		f.expectRead("modified", "id").
			DoAndReturn(func(context.Context, string, *interfaces.Resource, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				cancel()
				return nil, errors.New("index search failed")
			})

		prepared, err := f.worker.prepareIncrementalBaseline(ctx, task, f.resource, &interfaces.Catalog{})

		require.ErrorIs(t, err, context.Canceled)
		require.False(t, prepared)
	})
}

func TestBuildTaskWorkerRecoverIncrementalBaseline(t *testing.T) {
	t.Run("reads the first row using all effective cursor fields in descending order", func(t *testing.T) {
		f := newIncrementalBaselineFixture(t, interfaces.DataType_Timestamp, "timestamptz")
		f.resource.IndexConfig.PrimaryKeyFields = []string{"tenant", "id"}
		f.resource.IndexConfig.IncrementalFields = []string{"modified", "id"}
		f.resource.SchemaDefinition = append(f.resource.SchemaDefinition,
			&interfaces.Property{Name: "tenant", OriginalName: "tenant_id", OriginalType: "bigint", Type: interfaces.DataType_Integer})
		selected := map[string]any{
			"modified": "2026-01-02T23:04:05.123456789+08:00",
			"id":       json.Number("18446744073709551614"),
			"tenant":   json.Number("-9223372036854775808"),
		}
		f.expectRead("modified", "id", "tenant").Return(baselineResult(4000, selected), nil)

		mark, err := f.worker.recoverIncrementalBaseline(t.Context(), "incremental-task", f.resource)
		require.NoError(t, err)
		checkpoint, err := sync_checkpoint.DecodeBatch(mark)
		require.NoError(t, err)
		require.NoError(t, sync_checkpoint.ValidateCursor(checkpoint, []string{"modified", "id", "tenant"}, f.resource.SchemaDefinition))
		require.Equal(t, []interfaces.KeyValue{
			{Key: "modified", Value: selected["modified"]},
			{Key: "id", Value: uint64(18446744073709551614)},
			{Key: "tenant", Value: int64(-9223372036854775808)},
		}, checkpoint.Cursor)
	})

	t.Run("establishes an empty baseline for an empty published index", func(t *testing.T) {
		f := newIncrementalBaselineFixture(t, interfaces.DataType_Date, "date")
		f.expectRead("modified", "id").Return(baselineResult(0), nil)

		mark, err := f.worker.recoverIncrementalBaseline(t.Context(), "incremental-task", f.resource)
		require.NoError(t, err)
		require.JSONEq(t, `{"mode":"batch","cursor":[]}`, mark)
	})

	t.Run("preserves valid indexed temporal values", func(t *testing.T) {
		t.Setenv("TZ", "UTC")
		for _, tc := range []struct{ name, fieldType, nativeType, indexed string }{
			{"date", interfaces.DataType_Date, "date", "2026-11-20"},
			{"datetime", interfaces.DataType_Datetime, "datetime", "2026-11-20 16:00:00.123456"},
			{"datetime with offset", interfaces.DataType_Datetime, "datetimeoffset", "2026-11-20T16:00:00.123400+00:00"},
			{"timestamp with offset", interfaces.DataType_Timestamp, "timestamp(6)", "2026-11-20T16:00:00.123456+08:00"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newIncrementalBaselineFixture(t, tc.fieldType, tc.nativeType)
				f.expectRead("modified", "id").Return(baselineResult(1, map[string]any{
					"modified": tc.indexed, "id": json.Number("7"),
				}), nil)

				mark, err := f.worker.recoverIncrementalBaseline(t.Context(), "incremental-task", f.resource)

				require.NoError(t, err)
				checkpoint, err := sync_checkpoint.DecodeBatch(mark)
				require.NoError(t, err)
				require.Equal(t, interfaces.KeyValue{Key: "modified", Value: tc.indexed}, checkpoint.Cursor[0])
			})
		}
	})

	t.Run("rejects invalid timestamp formats", func(t *testing.T) {
		for _, indexed := range []string{
			"2026-01-02T15:04:05",
			"2026-01-02T15:04:05.1234567891Z",
			"2026-01-02T15:04:05+24:00",
		} {
			t.Run(indexed, func(t *testing.T) {
				f := newIncrementalBaselineFixture(t, interfaces.DataType_Timestamp, "timestamp")
				f.expectRead("modified", "id").Return(baselineResult(1, map[string]any{
					"modified": indexed, "id": json.Number("7"),
				}), nil)

				mark, err := f.worker.recoverIncrementalBaseline(t.Context(), "incremental-task", f.resource)

				require.ErrorIs(t, err, errBaselineInvalidCursor)
				require.Empty(t, mark)
			})
		}
	})

	t.Run("rejects a selection that cannot form a valid cursor", func(t *testing.T) {
		tests := []struct {
			name    string
			result  *interfaces.ResourceDataQueryResult
			readErr error
			want    string
		}{
			{name: "nonempty index with no row", result: baselineResult(2), want: "index search row count"},
			{name: "missing result", want: "invalid result"},
			{name: "missing key", result: baselineResult(1, map[string]any{"id": json.Number("7")}), want: "modified"},
			{name: "null key", result: baselineResult(1, map[string]any{"modified": nil, "id": json.Number("7")}), want: "modified"},
			{name: "numeric string", result: baselineResult(1, map[string]any{"modified": "2026-01-02", "id": "7"}), want: "id"},
			{name: "ambiguous date", result: baselineResult(1, map[string]any{"modified": json.Number("7"), "id": json.Number("7")}), want: "modified"},
			{name: "incomplete query", readErr: errors.New("incomplete index search result"), want: "incomplete index search result"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := newIncrementalBaselineFixture(t, interfaces.DataType_Date, "date")
				f.expectRead("modified", "id").Return(tt.result, tt.readErr)

				mark, err := f.worker.recoverIncrementalBaseline(t.Context(), "incremental-task", f.resource)
				require.ErrorContains(t, err, tt.want)
				require.Empty(t, mark)
			})
		}
	})
}

type incrementalBaselineFixture struct {
	worker   *BuildTaskWorker
	resource *interfaces.Resource
	lim      *vmock.MockLocalIndexManager
}

func newIncrementalBaselineFixture(t *testing.T, fieldType, originalType string) *incrementalBaselineFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	resource := &interfaces.Resource{
		ID: "resource", CatalogID: "catalog",
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index",
		IndexConfig: &interfaces.ResourceIndexConfig{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"modified"}},
		SchemaDefinition: []*interfaces.Property{
			{Name: "modified", OriginalName: "last_modified", OriginalType: originalType, Type: fieldType},
			{Name: "id", OriginalName: "id", OriginalType: "bigint unsigned", Type: interfaces.DataType_UnsignedInteger},
		},
	}
	lim := vmock.NewMockLocalIndexManager(ctrl)
	bts := vmock.NewMockBuildTaskService(ctrl)
	bts.EXPECT().InternalGetStatusByID(gomock.Any(), "incremental-task").Return(interfaces.BuildTaskStatusRunning, nil)
	return &incrementalBaselineFixture{
		lim: lim, resource: resource,
		worker: &BuildTaskWorker{bts: bts, bbw: &batchBuildWorker{lim: lim}},
	}
}

func (f *incrementalBaselineFixture) expectRead(fields ...string) *gomock.Call {
	params := &interfaces.ResourceDataQueryParams{
		Paging:       interfaces.PagingRequest{Limit: 1},
		OutputFields: fields,
		NeedTotal:    true,
	}
	for _, field := range fields {
		params.Sort = append(params.Sort, &interfaces.SortField{Field: field, Direction: "desc"})
	}
	return f.lim.EXPECT().ListDocuments(gomock.Any(), f.resource.LocalIndexName, f.resource, params)
}

func baselineResult(total int64, rows ...map[string]any) *interfaces.ResourceDataQueryResult {
	return &interfaces.ResourceDataQueryResult{Entries: rows, TotalCount: total}
}
