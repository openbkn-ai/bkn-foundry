// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
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
			log.Printf("record integrity materialization batch incomplete; failure_phases=%s; infrastructure failures may not have persisted retry status", recordIntegrityFailurePhases(err))
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

// Only fixed phase names reach logs. Never include raw dependency errors or the
// batch cursor: query, claim and save failures may have no durable status row.
func recordIntegrityFailurePhases(err error) string {
	if err == nil {
		return ""
	}
	var phases []string
	for _, phase := range []struct {
		err  error
		name string
	}{
		{evidencesvc.ErrRecordIntegrityCandidateQueryFailed, "query"},
		{evidencesvc.ErrRecordIntegrityClaimFailed, "claim"},
		{evidencesvc.ErrRecordIntegrityResultPersistenceFailed, "persist"},
		{evidencesvc.ErrRecordIntegrityInspectionDeferred, "inspection"},
	} {
		if errors.Is(err, phase.err) {
			phases = append(phases, phase.name)
		}
	}
	if len(phases) == 0 {
		return "unknown"
	}
	return strings.Join(phases, ",")
}

// Successful or skipped passes do not reset the error log window. Individual
// failure details are available in persisted status only when its write succeeded.
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
