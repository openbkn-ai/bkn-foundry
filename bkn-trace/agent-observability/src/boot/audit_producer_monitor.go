// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditproducersvc"
	"log"
	"time"
)

type auditProducerObserver interface{ Observe(context.Context) error }

// Poll outside query requests. Each observation and its persistence share one
// deadline; query handlers consume only the monitor's immutable cached snapshot.
func runAuditProducerMonitor(ctx context.Context, observer auditProducerObserver) {
	var nextErrorLog time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		observation, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := observer.Observe(observation)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil && !time.Now().Before(nextErrorLog) {
			log.Printf("audit producer diagnostics incomplete; observation_failed=%t; historical_gap_persistence_failed=%t", errors.Is(err, auditproducersvc.ErrObservationUnavailable), errors.Is(err, auditproducersvc.ErrGapPersistenceUnavailable))
			nextErrorLog = time.Now().Add(5 * time.Minute)
		}
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
