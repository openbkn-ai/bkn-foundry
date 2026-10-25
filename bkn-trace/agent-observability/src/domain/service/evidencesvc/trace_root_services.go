// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// MaxTraceRootServiceOptions bounds discovery result memory.
const MaxTraceRootServiceOptions = 100

var (
	ErrTraceRootServicesUnsupported = errors.New("trace root service filters are unsupported")
	ErrTraceRootServicesUnavailable = errors.New("trace root service discovery is unavailable")
)

// ListTraceRootServices discovers bounded canonical names using the same hot
// SQL membership or authorized receipt scan as execution summaries.
func (s *Service) ListTraceRootServices(ctx context.Context, o evidencevo.SummaryQueryOptions, search string, limit int) (evidencevo.TraceRootServiceOptions, error) {
	empty := evidencevo.TraceRootServiceOptions{Entries: []string{}}
	if o.Cursor != "" || o.Page != 0 || o.Limit != 0 || o.ConversationID != "" || o.InteractionID != "" || o.AgentOrApp != "" || o.Keyword != "" || o.KnowledgeNetwork != "" || o.RecordIntegrity != "" || o.EvidenceCompleteness != "" || o.ExcludeAgentOrApp != "" || len(o.ExcludeAgentOrApps) != 0 || (o.Scope.View != "" && o.Scope.View != evidencevo.AccessViewBusiness && o.Scope.View != evidencevo.AccessViewTechnical) {
		return empty, ErrTraceRootServicesUnsupported
	}
	if !trustedQueryScope(o.Scope) || o.Scope.AccessProfile != nil && (!o.Scope.AccessProfile.AccountActive || !summaryScopeMatchesProfile(o.Scope)) {
		return empty, nil
	}
	if limit <= 0 || limit > MaxTraceRootServiceOptions {
		limit = MaxTraceRootServiceOptions
	}
	o.Service = ""
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	search = strings.ToLower(strings.TrimSpace(search))
	// Match ordinary list membership: its simple path selects hot SQL receipts,
	// whereas the scanner also retains historical receipt projections after purge.
	if store, ok := s.sessionStore.(isessionstore.RootServiceOptionStore); ok && canUseTraceSummaryIdentityPage(o) {
		query := summaryIdentityQuery(o)
		query.Tool, query.Service, query.Limit = o.Tool, "", limit
		entries, hasMore, err := store.ListTraceRootServiceOptions(ctx, query, search)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = errors.Join(iprojectionsource.ErrSummaryScanBudgetExceeded, err)
			}
			return empty, err
		}
		if entries == nil {
			entries = []string{}
		}
		return evidencevo.TraceRootServiceOptions{Entries: entries, HasMore: hasMore}, nil
	}
	source, ok := s.projectionSource.(iprojectionsource.TraceSummaryScanSourcePort)
	if !ok {
		return empty, ErrTraceRootServicesUnavailable
	}
	status := o.Status
	if status == "" && strings.TrimSpace(o.ErrorKeyword) != "" {
		status = "error"
	}
	names := make(map[string]struct{}, limit)
	hasMore := false
	err := s.scanMatchedTraceSummaries(ctx, source, o, iprojectionsource.Query{Scope: o.Scope, From: o.From, To: o.To, TraceID: o.TraceID, Status: status, Tool: o.Tool, SummaryOnly: true, OmitSummaryTerminalContent: true}, func(trace evidencevo.TraceSummary) error {
		name := trace.RootService
		if strings.TrimSpace(name) == "" || !strings.Contains(strings.ToLower(name), search) {
			return nil
		}
		if _, exists := names[name]; exists {
			return nil
		}
		if len(names) == limit {
			hasMore = true
			return nil
		}
		names[name] = struct{}{}
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.Join(iprojectionsource.ErrSummaryScanBudgetExceeded, err)
		}
		return empty, err
	}
	result := evidencevo.TraceRootServiceOptions{Entries: make([]string, 0, len(names)), HasMore: hasMore}
	for name := range names {
		result.Entries = append(result.Entries, name)
	}
	sort.Strings(result.Entries)
	return result, nil
}
