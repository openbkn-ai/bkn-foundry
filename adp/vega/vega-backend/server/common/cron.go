// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"errors"
	"math/bits"
	"time"

	"github.com/robfig/cron/v3"
)

const cronMinuteMask = uint64(1<<60) - 1

var errCronTooFrequent = errors.New("cron_expr minimum interval is 1 hour")

// ParseHourlyCronExpr parses a Cron expression and requires at least one hour
// between adjacent executions.
func ParseHourlyCronExpr(cronExpr string) (cron.Schedule, error) {
	schedule, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return nil, err
	}

	switch typedSchedule := schedule.(type) {
	case *cron.SpecSchedule:
		// Standard Cron combines every selected minute with every selected hour.
		// Multiple selected minutes therefore always create sub-hour executions.
		if bits.OnesCount64(typedSchedule.Minute&cronMinuteMask) != 1 {
			return nil, errCronTooFrequent
		}
	case cron.ConstantDelaySchedule:
		if typedSchedule.Delay < time.Hour {
			return nil, errCronTooFrequent
		}
	default:
		firstRun := schedule.Next(time.Unix(0, 0).UTC())
		secondRun := schedule.Next(firstRun)
		if firstRun.IsZero() || secondRun.IsZero() || secondRun.Sub(firstRun) < time.Hour {
			return nil, errCronTooFrequent
		}
	}

	return schedule, nil
}
