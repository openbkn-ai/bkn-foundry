// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"os"
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

const (
	envActionExecutionMaxObjects     = "ACTION_EXECUTION_MAX_OBJECTS"
	defaultActionExecutionMaxObjects = 10000
)

// ActionExecutionMaxObjects is the most objects one action execution may
// target. Execution rejects a larger match set, and preview lists at most this
// many actions, so both describe the same set an execution would cover.
var ActionExecutionMaxObjects = defaultActionExecutionMaxObjects

func init() {
	if val := os.Getenv(envActionExecutionMaxObjects); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			ActionExecutionMaxObjects = n
			logger.Infof("Action execution max objects limit set to %d", ActionExecutionMaxObjects)
		}
	}
}
