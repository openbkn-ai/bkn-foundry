// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package itracestats

import "context"

// Source provides bounded, batch trace statistics for summary projections.
type Source interface {
	CountSpansByTraceIDs(ctx context.Context, traceIDs []string) (map[string]int, error)
}
