// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// The identity path has already selected an authorized SQL page. Only fill
// absent summaries; never use this capability to replace projection search or
// to hide dependency failures. Root-filtered pages keep their existing checks.
func (s *Service) fillMissingTracePage(ctx context.Context, options evidencevo.SummaryQueryOptions, ids []string, summaries map[string]evidencevo.TraceSummary) error {
	if options.Service != "" || options.Tool != "" {
		return nil
	}
	reader, ok := s.sessionStore.(isessionstore.TraceSummaryFallbackStore)
	if !ok {
		return nil
	}
	missing := []string{}
	wanted := map[string]bool{}
	for _, id := range ids {
		if _, found := summaries[id]; !found && !wanted[id] {
			missing = append(missing, id)
			wanted[id] = true
		}
	}
	if len(missing) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := reader.LoadTraceSummaryFallback(ctx, missing, options.Scope)
	if err != nil {
		return fmt.Errorf("read missing Trace page summaries: %w", err)
	}
	// Reuse the same canonical Agent/root/first-failure enrichment as projected
	// summaries. It reads scoped metadata in batches, not operation payload lists.
	selected := make([]evidencevo.TraceSummary, 0, len(missing))
	for _, row := range rows {
		if wanted[row.TraceID] {
			row.SpanCount = 0
			row.SpanCountStatus = "unavailable"
			selected = append(selected, row)
		}
	}
	if err := s.applyCanonicalTraceMetadata(ctx, selected, options.Scope, false); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, row := range selected {
		summaries[row.TraceID] = row
	}
	return nil
}
