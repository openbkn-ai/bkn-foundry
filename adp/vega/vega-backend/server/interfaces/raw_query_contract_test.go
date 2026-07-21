// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"testing"

	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRawQueryContractValidate(t *testing.T) {
	tests := []struct {
		name    string
		request RawQueryContract
		wantErr string
	}{
		{
			name: "sql defaults to postgres",
			request: RawQueryContract{
				Query:       "SELECT * FROM {{orders}}",
				QueryFormat: QueryFormatSQL,
			},
		},
		{
			name: "sql cursor request",
			request: RawQueryContract{
				Query:        "SELECT * FROM {{orders}}",
				QueryFormat:  QueryFormatSQL,
				InputDialect: "trino",
				Paging: PagingRequest{
					Mode:  PagingModeCursor,
					Limit: 100,
				},
			},
		},
		{
			name: "cursor first page accepts offset",
			request: RawQueryContract{
				Query:       "SELECT * FROM {{orders}}",
				QueryFormat: QueryFormatSQL,
				Paging: PagingRequest{
					Mode:   PagingModeCursor,
					Limit:  MinPageLimit,
					Offset: 20,
				},
			},
		},
		{
			name: "opensearch DSL",
			request: RawQueryContract{
				Query:        map[string]any{"resource_id": "resource-1"},
				QueryFormat:  QueryFormatDSL,
				InputDialect: "opensearch",
			},
		},
		{
			name: "opensearch DSL cursor",
			request: RawQueryContract{
				Query:        map[string]any{"resource_id": "resource-1", "sort": []any{"timestamp"}},
				QueryFormat:  QueryFormatDSL,
				InputDialect: "opensearch",
				Paging:       PagingRequest{Mode: PagingModeCursor, Limit: MinPageLimit},
			},
		},
		{
			name: "cursor continuation",
			request: RawQueryContract{
				Paging: PagingRequest{Cursor: "opaque-token"},
			},
		},
		{
			name: "single first page accepts offset",
			request: RawQueryContract{
				Query:       "SELECT * FROM {{orders}}",
				QueryFormat: QueryFormatSQL,
				Paging:      PagingRequest{Mode: PagingModeSingle, Offset: 20},
			},
		},
		{
			name: "rejects missing query format",
			request: RawQueryContract{
				Query: "SELECT 1",
			},
			wantErr: "query_format",
		},
		{
			name: "rejects unsupported format dialect pair",
			request: RawQueryContract{
				Query:        "SELECT 1",
				QueryFormat:  QueryFormatSQL,
				InputDialect: "opensearch",
			},
			wantErr: "unsupported SQL input_dialect",
		},
		{
			name: "rejects DSL without dialect",
			request: RawQueryContract{
				Query:       map[string]any{},
				QueryFormat: QueryFormatDSL,
			},
			wantErr: "DSL input_dialect",
		},
		{
			name: "rejects cursor without page size",
			request: RawQueryContract{
				Query:       "SELECT 1",
				QueryFormat: QueryFormatSQL,
				Paging:      PagingRequest{Mode: PagingModeCursor},
			},
			wantErr: "paging.limit is required",
		},
		{
			name: "rejects fields on continuation",
			request: RawQueryContract{
				Query: "SELECT 1",
				Paging: PagingRequest{
					Cursor: "opaque-token",
				},
			},
			wantErr: "only paging.cursor",
		},
		{
			name: "rejects offset on continuation",
			request: RawQueryContract{
				Paging: PagingRequest{Cursor: "opaque-token", Offset: 1},
			},
			wantErr: "only paging.cursor",
		},
		{
			name: "rejects excessive cursor keep alive",
			request: RawQueryContract{
				Query:       "SELECT 1",
				QueryFormat: QueryFormatSQL,
				Paging: PagingRequest{
					Mode:         PagingModeCursor,
					Limit:        MinPageLimit,
					KeepAliveSec: MaxCursorKeepAliveSec + 1,
				},
			},
			wantErr: "paging.keep_alive_sec",
		},
		{
			name: "rejects negative single limit",
			request: RawQueryContract{
				Query:       "SELECT 1",
				QueryFormat: QueryFormatSQL,
				Paging:      PagingRequest{Mode: PagingModeSingle, Limit: -1},
			},
			wantErr: "paging.limit",
		},
		{
			name: "rejects oversized single limit",
			request: RawQueryContract{
				Query:       "SELECT 1",
				QueryFormat: QueryFormatSQL,
				Paging:      PagingRequest{Mode: PagingModeSingle, Limit: MaxPageLimit + 1},
			},
			wantErr: "paging.limit",
		},
		{
			name: "ignores keep alive for single paging",
			request: RawQueryContract{
				Query:       "SELECT 1",
				QueryFormat: QueryFormatSQL,
				Paging:      PagingRequest{Mode: PagingModeSingle, KeepAliveSec: 60},
			},
		},
		{
			name: "accepts client search after for DSL cursor because execution drops it",
			request: RawQueryContract{
				Query:        map[string]any{"resource_id": "resource-1", "sort": []any{"timestamp"}, "search_after": []any{"cursor"}},
				QueryFormat:  QueryFormatDSL,
				InputDialect: "opensearch",
				Paging:       PagingRequest{Mode: PagingModeCursor, Limit: MinPageLimit},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestRawQueryResponseDoesNotExposeLegacyPagingState(t *testing.T) {
	response := RawQueryResponse{
		SearchAfter: []any{"internal"},
		NeedTotal:   true,
	}
	encoded, err := sonic.Marshal(response)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "has_more")
	assert.NotContains(t, string(encoded), "query_id")
	assert.NotContains(t, string(encoded), "search_after")
	assert.NotContains(t, string(encoded), "offset")
	assert.NotContains(t, string(encoded), "need_total")
}

func TestPagingRequestIgnoresRemovedSizeField(t *testing.T) {
	var request PagingRequest
	err := sonic.Unmarshal([]byte(`{"size": 10}`), &request)
	require.NoError(t, err)
	assert.Zero(t, request.Limit)

	err = sonic.Unmarshal([]byte(`{"limit": 10}`), &request)
	require.NoError(t, err)
	assert.Equal(t, 10, request.Limit)
}

func TestRawQueryRequestRejectsContinuationTimeout(t *testing.T) {
	err := RawQueryRequest{
		QueryTimeoutSec: 60,
		Paging:          PagingRequest{Cursor: "opaque-token"},
	}.ValidateContract()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query_timeout_sec")
}

func TestRawQueryContractEffectiveInputDialect(t *testing.T) {
	assert.Equal(t, "postgres", RawQueryContract{QueryFormat: QueryFormatSQL}.EffectiveInputDialect())
	assert.Equal(t, "mysql", RawQueryContract{QueryFormat: QueryFormatSQL, InputDialect: "MySQL"}.EffectiveInputDialect())
}
