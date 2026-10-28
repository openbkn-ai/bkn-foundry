// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"os"
	"time"
)

// SafeDeleteTimeout is the per-resource policy teardown budget. Reads and other
// Safe operations retain their existing timeout. Operators can tune this after
// measuring their deployment; an unset or invalid duration uses 30 seconds.
func SafeDeleteTimeout() time.Duration {
	if timeout, err := time.ParseDuration(os.Getenv("BKN_SAFE_DELETE_TIMEOUT")); err == nil && timeout > 0 {
		return timeout
	}
	return 30 * time.Second
}
