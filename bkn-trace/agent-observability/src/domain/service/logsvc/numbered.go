// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package logsvc

import (
	"context"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

// numberedSource must apply all filters and authorization before counting and
// slicing. Sources without this capability remain available through cursors;
// numbered jumps must never replay earlier pages.
type numberedSource interface {
	SearchNumbered(context.Context, observabilityvo.LogQuery, evidencevo.AccessProfile) (observabilityvo.SourcePage, error)
}

func (service *Service) listNumbered(ctx context.Context, profile evidencevo.AccessProfile, capabilities observabilityvo.AccessCapabilities, original, query observabilityvo.LogQuery, paged numberedSource, source Source, watermark time.Time, result observabilityvo.ListResult) (observabilityvo.ListResult, error) {
	status := service.sourceStatus(ctx, source)
	if status.Status == "not_integrated" {
		return observabilityvo.ListResult{}, ErrSourcesUnavailable
	}
	sourceContext, cancel := context.WithTimeout(ctx, service.sourceTimeout)
	defer cancel()
	started := time.Now()
	page, err := paged.SearchNumbered(sourceContext, query, profile)
	if err != nil {
		return observabilityvo.ListResult{}, errors.Join(ErrSourcesUnavailable, err)
	}
	if err := sourceContext.Err(); err != nil {
		return observabilityvo.ListResult{}, errors.Join(ErrSourcesUnavailable, err)
	}
	latency := time.Since(started).Milliseconds()
	status.LatencyMS = &latency
	if status.Status != observabilityvo.SourceCoverageDegraded {
		status.Status = "healthy"
	}
	status.CountAccuracy = page.CountAccuracy
	result.SourceStatus = append(result.SourceStatus, status)
	result.Partial = status.Status == observabilityvo.SourceCoverageDegraded
	result.Count = page.Count
	result.CountExact = !result.Partial && normalizedAccuracy(page.CountAccuracy) == "exact"
	for _, record := range page.Records {
		if (!service.operationAuditOnly || (isOperationAuditRecord(record) && validOperationAuditProjection(record))) && contains(query.AuthorizedCategories, record.Category) && matchesQuery(record, query) && canReadLog(profile, capabilities, record, original.IsAssociatedDrilldown()) {
			result.Records = append(result.Records, record)
		} else {
			// A source-contract violation must never disclose an unauthorized record or
			// advertise a raw count as the visible total.
			result.Partial, result.CountExact = true, false
			result.Count = int64(len(result.Records))
		}
	}
	if result.Partial && len(result.Records) != len(page.Records) {
		result.Count = int64(len(result.Records))
	}
	if page.NextCursor != "" && page.LastPosition != nil {
		result.NextCursor = encodeCursor(service.cursorKey, cursorPayload{
			Version: cursorVersion, SortVersion: cursorSortVersion, FilterHash: logFilterHash(original),
			EffectiveSubject: profile.EffectiveSubjectID, ApplicationID: profile.ApplicationPrincipalID,
			ScopeFingerprint: profile.Fingerprint, VisibleSources: []string{source.ID()},
			Positions:      map[string]observabilityvo.SourcePosition{source.ID(): *page.LastPosition},
			QueryWatermark: watermark, ExpiresAt: time.Now().Add(cursorTTL),
		})
	}
	return result, nil
}
