// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bknbackendaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestSearchProjectsBKNManagementFactAndForwardsTrustedScope(t *testing.T) {
	var request *http.Request
	client := New("http://bkn-backend:8080", &http.Client{Transport: roundTripFunc(func(candidate *http.Request) (*http.Response, error) {
		request = candidate
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Request: candidate,
			Body: io.NopCloser(strings.NewReader(`{"entries":[{"event_id":"evt-a","event_time":"2026-08-13T08:00:00Z","recorded_at":"2026-08-13T08:00:01Z","tenant_id":"tenant-a","knowledge_network_id":"kn-a","actor_id":"user-a","actor_name":"Alice","actor_type":"user","auth_method":"app_key","credential_id":"key-a","request_id":"req-a","source_channel":"api","method":"POST","action":"create","target_type":"object_type","target_id":"material","target_name":"物料","outcome":"success","change_summary":{"changed_fields":["name"]},"schema_version":"1.0"}],"has_more":false}`)),
		}, nil
	})})
	from := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	ctx := observabilityvo.WithSourceAuthorization(context.Background(), "Bearer token-a")
	page, err := client.Search(ctx, observabilityvo.LogQuery{
		TimeFrom: &from, TimeTo: &to, Action: "create", TargetType: "object_type", TargetID: "material", ActorID: "user-a", Limit: 20,
		AuthorizedTenantID: "tenant-a", AuthorizedCategories: []string{observabilityvo.CategoryAuditAdmin},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if request.Header.Get("Authorization") != "Bearer token-a" || request.Header.Get("x-tenant-id") != "tenant-a" ||
		request.URL.Query().Get("action") != "create" || request.URL.Query().Get("target_type") != "object_type" || request.URL.Query().Get("target_id") != "material" || request.URL.Query().Get("actor_id") != "user-a" {
		t.Fatalf("request = %s headers=%v", request.URL.String(), request.Header)
	}
	if len(page.Records) != 1 {
		t.Fatalf("records = %#v", page.Records)
	}
	record := page.Records[0]
	if record.LogID != "bkn-backend:evt-a" || record.SourceID != "bkn-backend" || record.Category != observabilityvo.CategoryAuditAdmin ||
		record.EventName != "resource_config.changed" || record.BusinessModule != "domain_knowledge_network" || record.Action != "create" ||
		record.TargetType != "object_type" || record.TargetID != "material" || record.TargetNameSnapshot != "物料" ||
		record.ActorNameSnapshot != "Alice" || record.ActorID != "user-a" || record.AuthMethod != "app_key" || record.RequestID != "req-a" ||
		len(record.KnowledgeNetworkIDs) != 1 || record.KnowledgeNetworkIDs[0] != "kn-a" {
		t.Fatalf("record = %#v", record)
	}
}

func TestMetadataDoesNotClaimAnUnconfiguredSourceIsHealthy(t *testing.T) {
	status := New("", nil).Metadata()
	if status.Status != "not_integrated" || status.Reason != "source_not_configured" {
		t.Fatalf("status = %#v", status)
	}
}

func TestMetadataDoesNotClaimPartialManagementCoverageIsHealthy(t *testing.T) {
	status := New("http://bkn-backend:8080", nil).Metadata()
	if status.Status != "degraded" || status.Reason != observabilityvo.SourceReasonPartialManagementAuditCoverage {
		t.Fatalf("status = %#v", status)
	}
}

func TestGetKeepsUnauthorizedDetailAsNotFound(t *testing.T) {
	client := New("http://bkn-backend:8080", &http.Client{Transport: roundTripFunc(func(candidate *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Request: candidate, Body: io.NopCloser(strings.NewReader(`{"error":"not found"}`))}, nil
	})})
	ctx := observabilityvo.WithSourceAuthorization(context.Background(), "Bearer token-a")
	ctx = observabilityvo.WithSourceAccessScope(ctx, observabilityvo.SourceAccessScope{TenantID: "tenant-a"})
	_, found, err := client.Get(ctx, "evt-secret")
	if err != nil || found {
		t.Fatalf("Get() found=%v err=%v", found, err)
	}
}
