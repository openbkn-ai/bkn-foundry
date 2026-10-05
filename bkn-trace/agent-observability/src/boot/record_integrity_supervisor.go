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
// transactions. A failed candidate remains unmaterialized for the next pass.
func runRecordIntegritySupervisor(ctx context.Context, batcher recordIntegrityBatcher, interval time.Duration) {
	cursor := ""
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
		if err != nil {
			log.Printf("record integrity materialization batch incomplete; candidates remain pending: %v", err)
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
