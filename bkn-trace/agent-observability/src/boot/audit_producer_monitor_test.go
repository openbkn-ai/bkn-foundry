// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"testing"
	"time"
)

type boundedAuditObserver struct {
	cancel   context.CancelFunc
	calls    int
	deadline time.Time
}

func (o *boundedAuditObserver) Observe(ctx context.Context) error {
	o.calls++
	o.deadline, _ = ctx.Deadline()
	o.cancel()
	return ctx.Err()
}
func TestAuditProducerMonitorBoundsWorkAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &boundedAuditObserver{cancel: cancel}
	start := time.Now()
	runAuditProducerMonitor(ctx, probe)
	if probe.calls != 1 || probe.deadline.Before(start) || probe.deadline.After(start.Add(3*time.Second+time.Second)) {
		t.Fatalf("unexpected bounded observation: %+v", probe)
	}
	runAuditProducerMonitor(ctx, probe)
	if probe.calls != 1 {
		t.Fatal("canceled monitor made another observation")
	}
}
