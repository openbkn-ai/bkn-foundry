// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"testing"

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
					Mode: PagingModeCursor,
					Size: 100,
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
			name: "cursor continuation",
			request: RawQueryContract{
				Paging: PagingRequest{Cursor: "opaque-token"},
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
			wantErr: "paging.size",
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

func TestRawQueryContractEffectiveInputDialect(t *testing.T) {
	assert.Equal(t, "postgres", RawQueryContract{QueryFormat: QueryFormatSQL}.EffectiveInputDialect())
	assert.Equal(t, "mysql", RawQueryContract{QueryFormat: QueryFormatSQL, InputDialect: "MySQL"}.EffectiveInputDialect())
}
