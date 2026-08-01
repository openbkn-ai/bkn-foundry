// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog_health_check_schedule

import (
	"errors"
	"math/bits"
	"time"

	"github.com/robfig/cron/v3"
)

const catalogHealthCheckMinimumInterval = time.Hour

const cronMinuteMask = uint64(1<<60) - 1

var errCatalogHealthCheckCronTooFrequent = errors.New("cron_expr minimum interval is 1 hour")

// ParseCronExpr parses a Catalog health-check Cron expression and enforces the minimum interval.
func ParseCronExpr(cronExpr string) (cron.Schedule, error) {
	schedule, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return nil, err
	}

	switch typedSchedule := schedule.(type) {
	case *cron.SpecSchedule:
		// Standard Cron combines every selected minute with every selected hour.
		// Multiple selected minutes therefore always create sub-hour executions.
		if bits.OnesCount64(typedSchedule.Minute&cronMinuteMask) != 1 {
			return nil, errCatalogHealthCheckCronTooFrequent
		}
	case cron.ConstantDelaySchedule:
		if typedSchedule.Delay < catalogHealthCheckMinimumInterval {
			return nil, errCatalogHealthCheckCronTooFrequent
		}
	default:
		firstRun := schedule.Next(time.Unix(0, 0).UTC())
		secondRun := schedule.Next(firstRun)
		if firstRun.IsZero() || secondRun.IsZero() || secondRun.Sub(firstRun) < catalogHealthCheckMinimumInterval {
			return nil, errCatalogHealthCheckCronTooFrequent
		}
	}

	return schedule, nil
}
