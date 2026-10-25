// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"log"
	"time"
)

type recordIntegrityBatcher interface {
	PersistRecordIntegrityBatch(context.Context, string, int) (string, error)
}

// Generation is bounded and independent of projection configuration and MCP
// transactions. The batcher defers failed candidates across cursor passes.
func runRecordIntegritySupervisor(ctx context.Context, batcher recordIntegrityBatcher, interval time.Duration) {
	cursor := ""
	var errorLog recordIntegrityErrorLogThrottle
	for {
		if ctx.Err() != nil {
			return
		}
		batchContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		next, err := batcher.PersistRecordIntegrityBatch(batchContext, cursor, 10)
		cancel()
		if ctx.Err() != nil {
			return
		}
		cursor = next
		if errorLog.shouldLog(time.Now(), err) {
			log.Print("record integrity materialization batch incomplete; inspect persisted materialization status")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Successful or skipped passes do not reset the error log window. Individual
// failure details and exhausted attempts remain available in persisted status.
type recordIntegrityErrorLogThrottle struct {
	nextLogAt time.Time
}

func (throttle *recordIntegrityErrorLogThrottle) shouldLog(now time.Time, err error) bool {
	if err == nil || now.Before(throttle.nextLogAt) {
		return false
	}
	throttle.nextLogAt = now.Add(5 * time.Minute)
	return true
}
