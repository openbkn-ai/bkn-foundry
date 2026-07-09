// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestValidateConnectorTypeReq(t *testing.T) {
	ctx := context.Background()

	validReq := func() *interfaces.ConnectorTypeReq {
		return &interfaces.ConnectorTypeReq{
			Mode:     interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*interfaces.ConnectorTypeReq)
		wantErr bool
	}{
		{
			name: "accepts local connector type",
		},
		{
			name: "accepts remote connector type with endpoint",
			mutate: func(req *interfaces.ConnectorTypeReq) {
				req.Mode = interfaces.ConnectorModeRemote
				req.Endpoint = "https://connector.example.com"
			},
		},
		{
			name: "rejects empty mode",
			mutate: func(req *interfaces.ConnectorTypeReq) {
				req.Mode = ""
			},
			wantErr: true,
		},
		{
			name: "rejects unknown mode",
			mutate: func(req *interfaces.ConnectorTypeReq) {
				req.Mode = "sidecar"
			},
			wantErr: true,
		},
		{
			name: "rejects empty category",
			mutate: func(req *interfaces.ConnectorTypeReq) {
				req.Category = ""
			},
			wantErr: true,
		},
		{
			name: "rejects unknown category",
			mutate: func(req *interfaces.ConnectorTypeReq) {
				req.Category = "warehouse"
			},
			wantErr: true,
		},
		{
			name: "rejects remote connector type without endpoint",
			mutate: func(req *interfaces.ConnectorTypeReq) {
				req.Mode = interfaces.ConnectorModeRemote
				req.Endpoint = ""
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validReq()
			if tt.mutate != nil {
				tt.mutate(req)
			}

			err := ValidateConnectorTypeReq(ctx, req)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateConnectorTypeListQueryParams(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		params  interfaces.ConnectorTypesQueryParams
		wantErr bool
	}{
		{
			name: "accepts empty params",
		},
		{
			name: "accepts mode and category filters",
			params: interfaces.ConnectorTypesQueryParams{
				Mode:     interfaces.ConnectorModeRemote,
				Category: interfaces.ConnectorCategoryAPI,
			},
		},
		{
			name: "rejects unknown mode",
			params: interfaces.ConnectorTypesQueryParams{
				Mode: "sidecar",
			},
			wantErr: true,
		},
		{
			name: "rejects unknown category",
			params: interfaces.ConnectorTypesQueryParams{
				Category: "warehouse",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConnectorTypeListQueryParams(ctx, tt.params)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateOptionalConnectorMode(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, ValidateOptionalConnectorMode(ctx, ""))
	require.NoError(t, ValidateOptionalConnectorMode(ctx, interfaces.ConnectorModeLocal))
	require.NoError(t, ValidateOptionalConnectorMode(ctx, interfaces.ConnectorModeRemote))
	require.Error(t, ValidateOptionalConnectorMode(ctx, "invalid"))
}

func TestValidateOptionalConnectorCategory(t *testing.T) {
	ctx := context.Background()

	validCategories := []string{
		"",
		interfaces.ConnectorCategoryTable,
		interfaces.ConnectorCategoryIndex,
		interfaces.ConnectorCategoryTopic,
		interfaces.ConnectorCategoryFile,
		interfaces.ConnectorCategoryFileset,
		interfaces.ConnectorCategoryMetric,
		interfaces.ConnectorCategoryAPI,
	}

	for _, category := range validCategories {
		t.Run("accepts "+category, func(t *testing.T) {
			require.NoError(t, ValidateOptionalConnectorCategory(ctx, category))
		})
	}

	require.Error(t, ValidateOptionalConnectorCategory(ctx, "warehouse"))
}
