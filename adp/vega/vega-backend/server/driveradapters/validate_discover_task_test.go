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

func Test_ValidateDiscoverTaskQueryParams(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		params  interfaces.DiscoverTaskQueryParams
		wantErr bool
	}{
		{
			name: "valid empty params",
		},
		{
			name: "valid status, strategy and trigger type",
			params: interfaces.DiscoverTaskQueryParams{
				Status:      interfaces.DiscoverTaskStatusCompleted,
				Strategy:    interfaces.DiscoverStrategyFullSync,
				TriggerType: interfaces.DiscoverTaskTriggerScheduled,
			},
		},
		{
			name: "invalid status",
			params: interfaces.DiscoverTaskQueryParams{
				Status: "unknown",
			},
			wantErr: true,
		},
		{
			name: "invalid strategy",
			params: interfaces.DiscoverTaskQueryParams{
				Strategy: "unknown",
			},
			wantErr: true,
		},
		{
			name: "invalid trigger type",
			params: interfaces.DiscoverTaskQueryParams{
				TriggerType: "unknown",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDiscoverTaskQueryParams(ctx, tt.params)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
