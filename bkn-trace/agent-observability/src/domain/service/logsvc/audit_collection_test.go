// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package logsvc

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type auditCollectionSource struct {
	fakeSource
	collection observabilityvo.AuditCollectionStatus
}

func (s auditCollectionSource) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{SourceID: s.ID(), Status: "healthy", StatusScope: "query", CollectionStatus: &s.collection, Reliability: "best_effort", Categories: []string{observabilityvo.CategoryAuditAdmin}}
}

func TestAuditCollectionStateIsIndependentOfQueryAndEmptyResults(t *testing.T) {
	gap := true
	for _, tc := range []struct {
		name, status string
		gap          *bool
		partial      bool
	}{
		{"unmonitored idle", "unknown", nil, false},
		{"publisher unavailable", "degraded", nil, false},
		{"recovered with historical gap", "healthy", &gap, false},
		{"observed delivery no known gap", "healthy", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := auditCollectionSource{fakeSource: fakeSource{id: "audit-ledger"}, collection: observabilityvo.AuditCollectionStatus{Status: tc.status, HistoricalGap: tc.gap}}
			svc := NewWithOptions([]Source{source}, Options{CursorKey: []byte("audit-state")})
			result, err := svc.List(context.Background(), activeProfile("admin", "super_admin"), observabilityvo.LogQuery{Limit: 10, Categories: []string{observabilityvo.CategoryAuditAdmin}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Records) != 0 || len(result.SourceStatus) != 1 || result.Partial != tc.partial || !result.CountExact {
				t.Fatalf("empty result must retain independent collection state: %+v", result)
			}
			status := result.SourceStatus[0]
			if status.Status != "healthy" || status.StatusScope != "query" || status.CollectionStatus.Status != tc.status {
				t.Fatalf("query success masked producer evidence: %+v", status)
			}
		})
	}
}

type numberedAuditCollectionSource struct{ auditCollectionSource }

func (s numberedAuditCollectionSource) SearchNumbered(context.Context, observabilityvo.LogQuery, evidencevo.AccessProfile) (observabilityvo.SourcePage, error) {
	// Seven stored matching records, all on page one. Page two is empty, exact.
	return observabilityvo.SourcePage{Count: 7, CountAccuracy: "exact"}, nil
}
func TestHistoricalPublisherGapDoesNotPoisonNumberedQueryCounts(t *testing.T) {
	gap := true
	since := time.Now().UTC()
	for _, offset := range []time.Duration{-24 * time.Hour, -time.Minute} {
		from, to := since.Add(offset), since.Add(offset+time.Minute)
		source := numberedAuditCollectionSource{auditCollectionSource{fakeSource: fakeSource{id: "audit-ledger"}, collection: observabilityvo.AuditCollectionStatus{Status: "healthy", HistoricalGap: &gap, HistoricalGapSince: &since}}}
		service := NewWithOptions([]Source{source}, Options{CursorKey: []byte("publisher-gap-numbered")})
		result, err := service.List(context.Background(), activeProfile("admin", "super_admin"), observabilityvo.LogQuery{Limit: 10, Page: 2, TimeFrom: &from, TimeTo: &to, Categories: []string{observabilityvo.CategoryAuditAdmin}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Partial || !result.CountExact || result.Count != 7 || len(result.Records) != 0 {
			t.Fatalf("historical producer loss changed stored-record pagination/count: %+v", result)
		}
		if len(result.SourceStatus) != 1 || result.SourceStatus[0].CollectionStatus.HistoricalGap == nil || !*result.SourceStatus[0].CollectionStatus.HistoricalGap {
			t.Fatalf("independent historical warning was erased: %+v", result.SourceStatus)
		}
	}
}
