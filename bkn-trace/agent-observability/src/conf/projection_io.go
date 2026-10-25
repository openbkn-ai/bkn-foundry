// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Reserve scheduling/commit margin in addition to the separate I/O budgets.
const projectionLeaseSafetyMargin = time.Second

type projectionIOBudgets struct{ lease, task, ack, lock time.Duration }

func readProjectionIOBudgets() (projectionIOBudgets, error) {
	var result projectionIOBudgets
	for _, setting := range []struct {
		name     string
		fallback time.Duration
		target   *time.Duration
	}{
		{"BKN_TRACE_PROJECTION_LEASE_IO_TIMEOUT", 2 * time.Second, &result.lease},
		{"BKN_TRACE_ANSWER_TASK_IO_TIMEOUT", 10 * time.Second, &result.task},
		{"BKN_TRACE_ANSWER_ACK_IO_TIMEOUT", 2 * time.Second, &result.ack},
		{"BKN_TRACE_PROJECTION_LOCK_DURATION", 30 * time.Second, &result.lock},
	} {
		*setting.target = setting.fallback
		if raw := strings.TrimSpace(os.Getenv(setting.name)); raw != "" {
			parsed, err := time.ParseDuration(raw)
			if err != nil || parsed <= 0 {
				return result, fmt.Errorf("%s must be a positive duration", setting.name)
			}
			*setting.target = parsed
		}
	}
	// Subtraction avoids overflow for syntactically valid very large durations.
	remaining := result.lock - projectionLeaseSafetyMargin
	for _, budget := range []time.Duration{result.lease, result.task, result.ack} {
		if remaining <= budget {
			return result, fmt.Errorf("projection lease/task/ACK timeouts plus 1s safety margin must be less than BKN_TRACE_PROJECTION_LOCK_DURATION")
		}
		remaining -= budget
	}
	return result, nil
}
