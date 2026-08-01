// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog_health_check_schedule

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCronExpr(t *testing.T) {
	tests := []struct {
		name     string
		cronExpr string
		wantErr  string
	}{
		{name: "accepts hourly", cronExpr: "0 * * * *"},
		{name: "accepts less frequent standard cron", cronExpr: "30 0,12 * * *"},
		{name: "accepts hourly descriptor", cronExpr: "@hourly"},
		{name: "accepts hourly fixed delay", cronExpr: "@every 1h"},
		{name: "rejects every minute", cronExpr: "* * * * *", wantErr: "minimum interval is 1 hour"},
		{name: "rejects multiple runs in one hour", cronExpr: "0,30 * * * *", wantErr: "minimum interval is 1 hour"},
		{name: "rejects short fixed delay", cronExpr: "@every 30m", wantErr: "minimum interval is 1 hour"},
		{name: "rejects invalid expression", cronExpr: "invalid", wantErr: "expected exactly 5 fields"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedule, err := ParseCronExpr(tt.cronExpr)
			if tt.wantErr == "" {
				require.NoError(t, err)
				require.NotNil(t, schedule)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
			require.Nil(t, schedule)
		})
	}
}
