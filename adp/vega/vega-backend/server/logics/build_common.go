// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root for details.

package logics

import "vega-backend/interfaces"

// BuildIndexName returns the managed OpenSearch index name for a build task.
func BuildIndexName(resourceID, buildTaskID string) string {
	return interfaces.BUILD_PREFIX + "-" + resourceID + "-" + buildTaskID
}
