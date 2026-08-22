// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package isourcecoveragestore

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

// Store owns durable collection-coverage facts, separate from log payload storage.
type Store interface {
	Get(ctx context.Context, sourceID, deploymentID string) (observabilityvo.SourceCoverage, bool, error)
	UpsertDegraded(ctx context.Context, coverage observabilityvo.SourceCoverage) error
	MarkHealthyAfterCatchUp(ctx context.Context, sourceID, deploymentID string, version uint64) error
}
