// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/logsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
	"net/http"
	"net/http/httptest"

	"encoding/json"
	"testing"
)

func TestAuditLedgerHealthDoesNotCertifyUnobservedProducer(t *testing.T) {
	raw, err := json.Marshal(NewAuditLedgerSource(nil).Metadata())
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err = json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if status["status_scope"] != "query" {
		t.Fatalf("query availability must be scoped; got %s", raw)
	}
	collection, ok := status["collection_status"].(map[string]any)
	if !ok || collection["status"] != "unknown" {
		t.Fatalf("unobserved publisher must remain unknown; got %s", raw)
	}
	if collection["dropped_records"] != nil || collection["historical_gap"] != nil {
		t.Fatalf("missing observation is not zero drops or proof of completeness: %s", raw)
	}
}

type auditPublisherWireSource struct{ *auditLedgerSource }

func (s auditPublisherWireSource) Search(context.Context, observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	return observabilityvo.SourcePage{CountAccuracy: "partial"}, nil
}

func (s auditPublisherWireSource) SearchNumbered(context.Context, observabilityvo.LogQuery, evidencevo.AccessProfile) (observabilityvo.SourcePage, error) {
	return observabilityvo.SourcePage{CountAccuracy: "partial"}, nil
}

type recordedPublisherState struct {
	state observabilityvo.AuditCollectionStatus
}

func (p recordedPublisherState) Status() observabilityvo.AuditCollectionStatus { return p.state }

func TestLogHTTPResponsePreservesPublisherStatusAlongsideEmptyQuery(t *testing.T) {
	gap := true
	provider := recordedPublisherState{state: observabilityvo.AuditCollectionStatus{Scope: "bkn-safe-publisher-instance", Status: "degraded", Reason: "publisher_unavailable", HistoricalGap: &gap}}
	source := auditPublisherWireSource{NewAuditLedgerSource(nil, provider)}
	profile := evidencevo.AccessProfile{AccountActive: true, ActorID: "auditor", EffectiveSubjectID: "auditor", Roles: []string{"audit"}}
	handler := newTestLogHandler(profile, nil)
	handler.service = logsvc.New([]logsvc.Source{source})
	request := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/logs?log_category=audit.admin", nil)
	request.Header.Set("x-account-id", "auditor")
	request.Header.Set("x-account-type", "user")
	response := httptest.NewRecorder()
	handler.ListLogs(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("query unavailable: %d %s", response.Code, response.Body.String())
	}
	var wire rdto.LogListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Partial {
		t.Fatalf("publisher diagnostics must not alter query completeness: %s", response.Body.String())
	}
	if len(wire.SourceStatus) != 1 {
		t.Fatalf("missing source diagnostics: %s", response.Body.String())
	}
	status := wire.SourceStatus[0]
	if status.Status != "healthy" || status.StatusScope != "query" || status.CollectionStatus == nil || status.CollectionStatus.Status != "degraded" || status.CollectionStatus.HistoricalGap == nil || !*status.CollectionStatus.HistoricalGap {
		t.Fatalf("HTTP query success concealed producer fault: %s", response.Body.String())
	}
}
