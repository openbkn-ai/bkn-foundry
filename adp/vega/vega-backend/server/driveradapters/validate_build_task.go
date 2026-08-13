// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import "vega-backend/interfaces"

func isValidBuildTaskOrderBy(o string) bool {
	_, ok := interfaces.BUILD_TASK_SORT[o]
	return ok
}

func isValidBuildTaskStatus(s string) bool {
	switch s {
	case interfaces.BuildTaskStatusPending,
		interfaces.BuildTaskStatusRunning,
		interfaces.BuildTaskStatusStopping,
		interfaces.BuildTaskStatusStopped,
		interfaces.BuildTaskStatusCompleted,
		interfaces.BuildTaskStatusFailed,
		interfaces.BuildTaskStatusCancelled:
		return true
	}
	return false
}

func isValidBuildTaskMode(m string) bool {
	switch m {
	case interfaces.BuildTaskModeStreaming,
		interfaces.BuildTaskModeBatch:
		return true
	}
	return false
}
