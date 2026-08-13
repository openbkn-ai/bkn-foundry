// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog_health_check_schedule

import (
	"fmt"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func validateRequest(req *interfaces.CatalogHealthCheckScheduleRequest) error {
	if req == nil {
		return fmt.Errorf("health check schedule request is required")
	}
	switch req.Mode {
	case interfaces.CatalogHealthCheckScheduleModeInherit:
		if req.CronExpr != "" {
			return fmt.Errorf("cron_expr must be empty when mode is inherit")
		}
	case interfaces.CatalogHealthCheckScheduleModeEnabled:
		if req.CronExpr == "" {
			return fmt.Errorf("cron_expr is required when mode is enabled")
		}
		if _, err := common.ParseHourlyCronExpr(req.CronExpr); err != nil {
			return fmt.Errorf("invalid cron_expr: %w", err)
		}
	case interfaces.CatalogHealthCheckScheduleModeDisabled:
	default:
		return fmt.Errorf("invalid mode: %s", req.Mode)
	}
	return nil
}
