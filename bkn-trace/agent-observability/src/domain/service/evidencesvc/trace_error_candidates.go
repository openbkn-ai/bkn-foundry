// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func (s *Service) traceErrorCandidateFilter(o evidencevo.SummaryQueryOptions) iprojectionsource.TraceSummaryCandidateFilter {
	keyword := strings.ToLower(strings.TrimSpace(o.ErrorKeyword))
	if keyword == "" || s.sessionStore == nil || o.Scope.AccessProfile == nil || !evidencevo.HasGlobalTraceAccess(*o.Scope.AccessProfile) {
		return nil
	}
	return func(ctx context.Context, candidates []iprojectionsource.TraceSummaryCandidate) ([]string, error) {
		if len(candidates) == 0 {
			return nil, nil
		}
		scopes := make([]isessionstore.TraceMetadataScope, 0, len(candidates))
		for _, c := range candidates {
			scopes = append(scopes, isessionstore.TraceMetadataScope{TraceID: c.TraceID, ConversationID: c.ConversationID, InteractionID: c.InteractionID})
		}
		possible := map[string]bool{}
		err := s.sessionStore.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			metadata, ok := tx.(isessionstore.TraceFailureMetadataTransaction)
			if !ok {
				// Older stores retain the complete scanner; never infer no match
				// merely because the narrow failure capability is unavailable.
				for _, c := range candidates {
					possible[c.TraceID] = true
				}
				return nil
			}
			for _, fact := range metadata.ListFirstTraceFailureMetadata(scopes) {
				if fact.Status == sessionvo.AttemptFailed && strings.Contains(strings.ToLower(operationFailureSummary(fact)), keyword) {
					possible[fact.TraceID] = true
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(possible))
		for _, c := range candidates {
			if possible[c.TraceID] {
				ids = append(ids, c.TraceID)
				delete(possible, c.TraceID)
			}
		}
		return ids, nil
	}
}
