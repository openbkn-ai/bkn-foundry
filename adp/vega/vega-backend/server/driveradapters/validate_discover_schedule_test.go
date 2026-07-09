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

func Test_ValidateDiscoverScheduleRequest(t *testing.T) {
	validReq := func() *interfaces.DiscoverScheduleRequest {
		return &interfaces.DiscoverScheduleRequest{
			Name:      "schedule-1",
			CatalogID: "catalog-1",
			CronExpr:  "*/5 * * * *",
			StartTime: 1000,
			EndTime:   2000,
			Strategy:  interfaces.DiscoverStrategyFullSync,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*interfaces.DiscoverScheduleRequest)
		wantErr bool
	}{
		{
			name: "valid request",
		},
		{
			name: "missing name",
			mutate: func(req *interfaces.DiscoverScheduleRequest) {
				req.Name = ""
			},
			wantErr: true,
		},
		{
			name: "missing catalog ID",
			mutate: func(req *interfaces.DiscoverScheduleRequest) {
				req.CatalogID = ""
			},
			wantErr: true,
		},
		{
			name: "missing cron expression",
			mutate: func(req *interfaces.DiscoverScheduleRequest) {
				req.CronExpr = ""
			},
			wantErr: true,
		},
		{
			name: "invalid cron expression",
			mutate: func(req *interfaces.DiscoverScheduleRequest) {
				req.CronExpr = "invalid"
			},
			wantErr: true,
		},
		{
			name: "invalid strategy",
			mutate: func(req *interfaces.DiscoverScheduleRequest) {
				req.Strategy = "unknown"
			},
			wantErr: true,
		},
		{
			name: "invalid time range",
			mutate: func(req *interfaces.DiscoverScheduleRequest) {
				req.StartTime = 2000
				req.EndTime = 1000
			},
			wantErr: true,
		},
		{
			name: "valid time range without end time",
			mutate: func(req *interfaces.DiscoverScheduleRequest) {
				req.StartTime = 0
				req.EndTime = 0
			},
		},
		{
			name: "negative start time",
			mutate: func(req *interfaces.DiscoverScheduleRequest) {
				req.StartTime = -1
			},
			wantErr: true,
		},
		{
			name: "negative end time",
			mutate: func(req *interfaces.DiscoverScheduleRequest) {
				req.EndTime = -1
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

			err := ValidateDiscoverScheduleRequest(context.Background(), req)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
