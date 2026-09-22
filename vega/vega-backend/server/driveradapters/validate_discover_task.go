// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"

func isValidDiscoverTaskStatus(s string) bool {
	switch s {
	case interfaces.DiscoverTaskStatusPending,
		interfaces.DiscoverTaskStatusRunning,
		interfaces.DiscoverTaskStatusCompleted,
		interfaces.DiscoverTaskStatusFailed,
		interfaces.DiscoverTaskStatusCancelled:
		return true
	}
	return false
}

func isValidDiscoverTaskTriggerType(s string) bool {
	switch s {
	case interfaces.DiscoverTaskTriggerManual,
		interfaces.DiscoverTaskTriggerScheduled:
		return true
	}
	return false
}
