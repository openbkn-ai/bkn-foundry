// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/extensions"
)

func TestValidateCatalogRequest(t *testing.T) {
	ctx := context.Background()

	validReq := func() *interfaces.CatalogRequest {
		return &interfaces.CatalogRequest{
			ID:          "catalog-1",
			Name:        "catalog",
			Tags:        []string{"prod"},
			Description: "catalog description",
			ConnectorCfg: interfaces.ConnectorConfig{
				"databases": []any{"db1", "db2"},
				"schemas":   []any{"public", "analytics"},
			},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*interfaces.CatalogRequest)
		wantErr bool
	}{
		{
			name: "accepts valid request",
		},
		{
			name: "rejects invalid id",
			mutate: func(req *interfaces.CatalogRequest) {
				req.ID = "catalog.with.dot"
			},
			wantErr: true,
		},
		{
			name: "rejects empty name",
			mutate: func(req *interfaces.CatalogRequest) {
				req.Name = ""
			},
			wantErr: true,
		},
		{
			name: "rejects too many tags",
			mutate: func(req *interfaces.CatalogRequest) {
				req.Tags = make([]string, interfaces.TAGS_MAX_NUMBER+1)
				for i := range req.Tags {
					req.Tags[i] = "tag"
				}
			},
			wantErr: true,
		},
		{
			name: "rejects overlong description",
			mutate: func(req *interfaces.CatalogRequest) {
				req.Description = strings.Repeat("a", interfaces.DESCRIPTION_MAX_LENGTH+1)
			},
			wantErr: true,
		},
		{
			name: "rejects duplicate database connector config",
			mutate: func(req *interfaces.CatalogRequest) {
				req.ConnectorCfg["databases"] = []any{"db1", "db1"}
			},
			wantErr: true,
		},
		{
			name: "rejects duplicate schema connector config",
			mutate: func(req *interfaces.CatalogRequest) {
				req.ConnectorCfg["schemas"] = []any{"public", "public"}
			},
			wantErr: true,
		},
		{
			name: "rejects reserved extension key",
			mutate: func(req *interfaces.CatalogRequest) {
				req.Extensions = &map[string]string{"vega_owner": "system"}
			},
			wantErr: true,
		},
		{
			name: "accepts valid extensions",
			mutate: func(req *interfaces.CatalogRequest) {
				req.Extensions = &map[string]string{"domain": "finance"}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validReq()
			if tt.mutate != nil {
				tt.mutate(req)
			}

			err := ValidateCatalogRequest(ctx, req)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateCatalogListQueryParams(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		params  interfaces.CatalogsQueryParams
		wantErr bool
	}{
		{
			name: "accepts empty params",
		},
		{
			name: "accepts physical type",
			params: interfaces.CatalogsQueryParams{
				Type: interfaces.CatalogTypePhysical,
			},
		},
		{
			name: "accepts logical type",
			params: interfaces.CatalogsQueryParams{
				Type: interfaces.CatalogTypeLogical,
			},
		},
		{
			name: "rejects unknown type",
			params: interfaces.CatalogsQueryParams{
				Type: "virtual",
			},
			wantErr: true,
		},
		{
			name: "accepts supported health status",
			params: interfaces.CatalogsQueryParams{
				HealthCheckStatus: interfaces.CatalogHealthStatusUnchecked,
			},
		},
		{
			name: "rejects unknown health status",
			params: interfaces.CatalogsQueryParams{
				HealthCheckStatus: "stale",
			},
			wantErr: true,
		},
		{
			name: "accepts paired extension filters",
			params: interfaces.CatalogsQueryParams{
				ExtensionKeys:   []string{"domain"},
				ExtensionValues: []string{"finance"},
			},
		},
		{
			name: "rejects mismatched extension filters",
			params: interfaces.CatalogsQueryParams{
				ExtensionKeys:   []string{"domain"},
				ExtensionValues: nil,
			},
			wantErr: true,
		},
		{
			name: "rejects too many extension filters",
			params: interfaces.CatalogsQueryParams{
				ExtensionKeys:   make([]string, extensions.MaxExtensionFilterPairs+1),
				ExtensionValues: make([]string, extensions.MaxExtensionFilterPairs+1),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCatalogListQueryParams(ctx, tt.params)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
