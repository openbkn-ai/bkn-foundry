// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"vega-backend/interfaces"
)

func TestIsValidDiscoverTaskStatus(t *testing.T) {
	assert.True(t, isValidDiscoverTaskStatus(interfaces.DiscoverTaskStatusPending))
	assert.True(t, isValidDiscoverTaskStatus(interfaces.DiscoverTaskStatusRunning))
	assert.True(t, isValidDiscoverTaskStatus(interfaces.DiscoverTaskStatusCompleted))
	assert.True(t, isValidDiscoverTaskStatus(interfaces.DiscoverTaskStatusFailed))
	assert.True(t, isValidDiscoverTaskStatus(interfaces.DiscoverTaskStatusCancelled))
	assert.False(t, isValidDiscoverTaskStatus("unknown"))
}

func TestIsValidDiscoverTaskTriggerType(t *testing.T) {
	assert.True(t, isValidDiscoverTaskTriggerType(interfaces.DiscoverTaskTriggerManual))
	assert.True(t, isValidDiscoverTaskTriggerType(interfaces.DiscoverTaskTriggerScheduled))
	assert.False(t, isValidDiscoverTaskTriggerType("unknown"))
}
