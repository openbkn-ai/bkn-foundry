// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package logsvc

import (
	"context"
	"testing"

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
		{"publisher unavailable", "degraded", nil, true},
		{"recovered with historical gap", "healthy", &gap, true},
		{"observed delivery no known gap", "healthy", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := auditCollectionSource{fakeSource: fakeSource{id: "audit-ledger"}, collection: observabilityvo.AuditCollectionStatus{Status: tc.status, HistoricalGap: tc.gap}}
			svc := NewWithOptions([]Source{source}, Options{CursorKey: []byte("audit-state")})
			result, err := svc.List(context.Background(), activeProfile("admin", "super_admin"), observabilityvo.LogQuery{Limit: 10, Categories: []string{observabilityvo.CategoryAuditAdmin}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Records) != 0 || len(result.SourceStatus) != 1 || result.Partial != tc.partial {
				t.Fatalf("empty result must retain independent collection state: %+v", result)
			}
			status := result.SourceStatus[0]
			if status.Status != "healthy" || status.StatusScope != "query" || status.CollectionStatus.Status != tc.status {
				t.Fatalf("query success masked producer evidence: %+v", status)
			}
		})
	}
}
