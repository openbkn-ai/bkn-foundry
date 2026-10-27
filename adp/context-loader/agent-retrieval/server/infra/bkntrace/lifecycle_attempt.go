// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"context"
	"time"
)

// LifecycleAttemptContext leaves a small part of the existing phase budget for
// acknowledgement recovery. A write using the entire phase deadline otherwise
// makes every subsequent receipt read or idempotent replay run on an expired
// context. This never extends the parent deadline or authorizes business replay.
func LifecycleAttemptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	remaining := time.Until(deadline)
	reserve := min(100*time.Millisecond, max(time.Duration(0), remaining/4))
	return context.WithDeadline(ctx, deadline.Add(-reserve))
}
