// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencevo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQueryScopeNeverSerializesAuthorization(t *testing.T) {
	body, err := json.Marshal(QueryScope{AccountID: "account_a", Authorization: "Bearer secret-user-token"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-user-token") || strings.Contains(string(body), "Authorization") {
		t.Fatalf("query authorization leaked into JSON: %s", body)
	}
}

func TestMatchesScopeRequiresEveryPersistedOwnershipDimension(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_owned", RequestID: "req_owned",
		TenantID: "tenant_a", AccountID: "account_a", AccountType: "user",
	}
	tests := []QueryScope{
		{TenantID: "tenant_b", AccountID: "account_a", AccountType: "user"},
		{TenantID: "tenant_a", AccountID: "account_b", AccountType: "user"},
		{TenantID: "tenant_a", AccountID: "account_a", AccountType: "app"},
	}
	for _, scope := range tests {
		if MatchesScope(trace, scope) {
			t.Fatalf("mismatched persisted ownership must fail closed: trace=%+v scope=%+v", trace, scope)
		}
	}
	if !MatchesScope(trace, QueryScope{TenantID: "tenant_a", AccountID: "account_a", AccountType: "user"}) {
		t.Fatal("exact ownership must match")
	}
}

func TestSameOwnershipComparesEveryPersistedDimension(t *testing.T) {
	existing := NormalizedTrace{
		TraceID: "trace_owned", RequestID: "req_owned",
		TenantID: "tenant_a", AccountID: "account_a", AccountType: "user",
	}
	tests := []NormalizedTrace{
		{TraceID: "trace_owned", RequestID: "req_owned", TenantID: "tenant_b", AccountID: "account_a", AccountType: "user"},
		{TraceID: "trace_owned", RequestID: "req_owned", TenantID: "tenant_a", AccountID: "account_b", AccountType: "user"},
		{TraceID: "trace_owned", RequestID: "req_owned", TenantID: "tenant_a", AccountID: "account_a", AccountType: "app"},
	}
	for _, incoming := range tests {
		if SameOwnership(existing, incoming) {
			t.Fatalf("ownership drift must be rejected: existing=%+v incoming=%+v", existing, incoming)
		}
	}
}

func TestSameOwnershipAllowsConversationMigrationButRejectsConversationDrift(t *testing.T) {
	base := NormalizedTrace{
		TraceID: "trace_owned", RequestID: "req_owned",
		TenantID: "tenant_a", AccountID: "account_a", AccountType: "user",
	}
	withConversation := base
	withConversation.ConversationID = "conversation_a"
	if !SameOwnership(base, withConversation) || !SameOwnership(withConversation, base) {
		t.Fatal("an optional conversation id must be addable across contract migration")
	}
	drifted := withConversation
	drifted.ConversationID = "conversation_b"
	if SameOwnership(withConversation, drifted) {
		t.Fatal("two different non-empty conversation ids must conflict")
	}
}

func TestMatchesScopeRejectsLegacyTraceWithoutOwnership(t *testing.T) {
	legacy := NormalizedTrace{TraceID: "trace_legacy", RequestID: "req_legacy", SchemaVersion: LegacyContractVersion}
	scope := QueryScope{TenantID: "tenant_a", AccountID: "account_a", AccountType: "user"}
	if MatchesScope(legacy, scope) {
		t.Fatal("legacy trace without persisted ownership must fail closed")
	}
	if SameOwnership(legacy, NormalizedTrace{TraceID: legacy.TraceID, RequestID: legacy.RequestID, TenantID: "tenant_a", AccountID: "account_a", AccountType: "user"}) {
		t.Fatal("legacy unowned trace must not be claimable by a new append")
	}
}
