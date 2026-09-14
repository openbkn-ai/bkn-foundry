// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"log/slog"
	"time"
)

// RunPolicyRefresh reloads the live model from the store every interval until
// ctx ends. Writes keep the live model current on their own; this is the
// safety net for rows changed outside bkn-safe while it runs (a manual repair,
// for instance), which the pre-#1511 reload-on-every-write used to pick up at
// the next write. A reload that finds a difference is logged, since in normal
// operation it should find none. interval <= 0 disables it.
func (en *Enforcer) RunPolicyRefresh(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			start := time.Now()
			drifted, err := en.ReloadPolicy(ctx)
			switch {
			case err != nil:
				if ctx.Err() == nil {
					slog.Error("periodic authorization policy reload failed", "err", err)
				}
			case drifted:
				slog.Warn("authorization policy differed from the store; live model reloaded",
					"took", time.Since(start))
			default:
				slog.Debug("authorization policy reloaded", "took", time.Since(start))
			}
		}
	}
}
