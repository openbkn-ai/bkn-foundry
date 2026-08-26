// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root.

package logics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseBuildIndexName(t *testing.T) {
	resourceID, taskID, ok := ParseBuildIndexName("vega-build-resource-task")
	assert.True(t, ok)
	assert.Equal(t, "resource", resourceID)
	assert.Equal(t, "task", taskID)

	for _, name := range []string{"foreign-resource-task", "vega-build-resource-task-extra", "vega-build-resource-"} {
		_, _, ok := ParseBuildIndexName(name)
		assert.False(t, ok, name)
	}
}
