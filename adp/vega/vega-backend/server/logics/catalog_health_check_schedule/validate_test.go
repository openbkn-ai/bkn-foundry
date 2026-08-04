// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog_health_check_schedule

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
)

func TestValidateRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *interfaces.CatalogHealthCheckScheduleRequest
		wantErr string
	}{
		{name: "accepts inherit", req: &interfaces.CatalogHealthCheckScheduleRequest{Mode: interfaces.CatalogHealthCheckScheduleModeInherit}},
		{name: "accepts enabled cron", req: &interfaces.CatalogHealthCheckScheduleRequest{Mode: interfaces.CatalogHealthCheckScheduleModeEnabled, CronExpr: "0 * * * *"}},
		{name: "accepts disabled with retained cron", req: &interfaces.CatalogHealthCheckScheduleRequest{Mode: interfaces.CatalogHealthCheckScheduleModeDisabled, CronExpr: "*/5 * * * *"}},
		{name: "rejects nil request", wantErr: "required"},
		{name: "rejects inherit cron", req: &interfaces.CatalogHealthCheckScheduleRequest{Mode: interfaces.CatalogHealthCheckScheduleModeInherit, CronExpr: "*/5 * * * *"}, wantErr: "must be empty"},
		{name: "rejects missing enabled cron", req: &interfaces.CatalogHealthCheckScheduleRequest{Mode: interfaces.CatalogHealthCheckScheduleModeEnabled}, wantErr: "required"},
		{name: "rejects enabled cron more frequent than hourly", req: &interfaces.CatalogHealthCheckScheduleRequest{Mode: interfaces.CatalogHealthCheckScheduleModeEnabled, CronExpr: "*/30 * * * *"}, wantErr: "minimum interval is 1 hour"},
		{name: "rejects invalid mode", req: &interfaces.CatalogHealthCheckScheduleRequest{Mode: "unknown"}, wantErr: "invalid mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequest(tt.req)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
