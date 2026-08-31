// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchconversationaudit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type fakeSearchClient struct {
	index  string
	body   []byte
	result []byte
	calls  int
}

func (client *fakeSearchClient) Search(_ context.Context, index string, body []byte) ([]byte, error) {
	client.calls++
	client.index, client.body = index, append([]byte(nil), body...)
	return client.result, nil
}

func TestSearchProjectsConversationCreatedFromAuthoritativeProjection(t *testing.T) {
	createdAt := time.Date(2026, time.August, 12, 9, 30, 0, 0, time.UTC)
	conversation := map[string]any{
		"conversation_id":           "conv-a",
		"agent_name":                "供应链分析助手",
		"external_conversation_key": "cursor-thread-a",
		"creation_request_id":       "req-create-a",
		"business_context":          "managed",
		"actor_name_snapshot":       "供应链管理员",
		"creation_auth_method":      "api_key",
		"generation":                1,
		"status":                    "active",
		"one_shot":                  false,
		"row_version":               1,
		"created_at":                createdAt.Format(time.RFC3339Nano),
		"updated_at":                createdAt.Format(time.RFC3339Nano),
		"owner": map[string]any{
			"application_principal_id": "app-a",
			"effective_subject_type":   "user",
			"effective_subject_id":     "user-a",
		},
	}
	payload, _ := json.Marshal(map[string]any{
		"hits": map[string]any{
			"total": map[string]any{"value": 1, "relation": "eq"},
			"hits":  []any{map[string]any{"_id": "conversation:conv-a", "_source": conversation, "sort": []any{createdAt.Format(time.RFC3339Nano), "conv-a"}}},
		},
	})
	backend := &fakeSearchClient{result: payload}
	source := New(backend, "openbkn-core-projection")

	page, err := source.Search(context.Background(), observabilityvo.LogQuery{
		ConversationID: "conv-a", Limit: 20,
	})
	if err != nil {
		t.Fatalf("search conversation audit: %v", err)
	}
	if backend.index != "openbkn-core-projection" || len(page.Records) != 1 {
		t.Fatalf("unexpected source result: index=%q page=%+v", backend.index, page)
	}
	record := page.Records[0]
	if record.EventID != "conversation.created:conv-a" ||
		record.LogID != "bkn-trace-core:conversation.created:conv-a" ||
		record.SourceID != "bkn-trace-core" ||
		record.Category != observabilityvo.CategoryRuntimeBusiness ||
		record.EventName != "conversation.created" ||
		record.BusinessModule != "domain_knowledge_network" ||
		record.Action != "create" ||
		record.ConversationID != "conv-a" ||
		record.ActorID != "user-a" ||
		record.ApplicationID != "app-a" ||
		record.ActorNameSnapshot != "供应链管理员" ||
		record.TargetNameSnapshot != "供应链分析助手" ||
		record.AuthMethod != "api_key" ||
		record.RequestID != "req-create-a" ||
		record.SafeSummary != "Started an Agent business conversation" ||
		record.Attributes["business_context"] != "managed" ||
		record.Attributes["agent_name"] != "供应链分析助手" {
		t.Fatalf("conversation audit projection lost source facts: %+v", record)
	}
	var query map[string]any
	if err := json.Unmarshal(backend.body, &query); err != nil {
		t.Fatalf("decode search query: %v", err)
	}
	encoded, _ := json.Marshal(query)
	for _, expected := range []string{"external_conversation_key", "conversation_id.keyword"} {
		if !containsString(string(encoded), expected) {
			t.Fatalf("trusted projection filter %q missing from %s", expected, encoded)
		}
	}
}

func TestSearchReturnsExactEmptyForRequestRuntimeCorrelation(t *testing.T) {
	backend := &fakeSearchClient{}
	page, err := New(backend, "openbkn-core-projection").Search(context.Background(), observabilityvo.LogQuery{
		RequestID: "req-a",
	})
	if err != nil || backend.calls != 0 || len(page.Records) != 0 || page.Count != 0 || page.CountAccuracy != "exact" {
		t.Fatalf("page=%+v calls=%d err=%v", page, backend.calls, err)
	}
}

func containsString(value, expected string) bool {
	for index := 0; index+len(expected) <= len(value); index++ {
		if value[index:index+len(expected)] == expected {
			return true
		}
	}
	return false
}
