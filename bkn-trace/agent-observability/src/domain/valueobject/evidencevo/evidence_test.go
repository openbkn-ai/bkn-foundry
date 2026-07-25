package evidencevo

import "testing"

func TestMatchesScopeRequiresEveryPersistedOwnershipDimension(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_owned", RequestID: "req_owned",
		TenantID: "tenant_a", BusinessDomain: "domain_a", AccountID: "account_a", AccountType: "user",
	}
	tests := []QueryScope{
		{TenantID: "tenant_a", AccountID: "account_a", AccountType: "user"},
		{TenantID: "tenant_a", BusinessDomain: "domain_b", AccountID: "account_a", AccountType: "user"},
		{TenantID: "tenant_b", BusinessDomain: "domain_a", AccountID: "account_a", AccountType: "user"},
		{TenantID: "tenant_a", BusinessDomain: "domain_a", AccountID: "account_b", AccountType: "user"},
		{TenantID: "tenant_a", BusinessDomain: "domain_a", AccountID: "account_a", AccountType: "app"},
	}
	for _, scope := range tests {
		if MatchesScope(trace, scope) {
			t.Fatalf("mismatched persisted ownership must fail closed: trace=%+v scope=%+v", trace, scope)
		}
	}
	if !MatchesScope(trace, QueryScope{TenantID: "tenant_a", BusinessDomain: "domain_a", AccountID: "account_a", AccountType: "user"}) {
		t.Fatal("exact ownership must match")
	}
}

func TestSameOwnershipComparesEveryPersistedDimension(t *testing.T) {
	existing := NormalizedTrace{
		TraceID: "trace_owned", RequestID: "req_owned",
		TenantID: "tenant_a", BusinessDomain: "domain_a", AccountID: "account_a", AccountType: "user",
	}
	tests := []NormalizedTrace{
		{TraceID: "trace_owned", RequestID: "req_owned", TenantID: "tenant_a", AccountID: "account_a", AccountType: "user"},
		{TraceID: "trace_owned", RequestID: "req_owned", TenantID: "tenant_a", BusinessDomain: "domain_b", AccountID: "account_a", AccountType: "user"},
		{TraceID: "trace_owned", RequestID: "req_owned", TenantID: "tenant_b", BusinessDomain: "domain_a", AccountID: "account_a", AccountType: "user"},
		{TraceID: "trace_owned", RequestID: "req_owned", TenantID: "tenant_a", BusinessDomain: "domain_a", AccountID: "account_b", AccountType: "user"},
		{TraceID: "trace_owned", RequestID: "req_owned", TenantID: "tenant_a", BusinessDomain: "domain_a", AccountID: "account_a", AccountType: "app"},
	}
	for _, incoming := range tests {
		if SameOwnership(existing, incoming) {
			t.Fatalf("ownership drift must be rejected: existing=%+v incoming=%+v", existing, incoming)
		}
	}
}

func TestMatchesScopeRejectsLegacyTraceWithoutOwnership(t *testing.T) {
	legacy := NormalizedTrace{TraceID: "trace_legacy", RequestID: "req_legacy", SchemaVersion: LegacyContractVersion}
	scope := QueryScope{TenantID: "tenant_a", BusinessDomain: "domain_a", AccountID: "account_a", AccountType: "user"}
	if MatchesScope(legacy, scope) {
		t.Fatal("legacy trace without persisted ownership must fail closed")
	}
	if SameOwnership(legacy, NormalizedTrace{TraceID: legacy.TraceID, RequestID: legacy.RequestID, TenantID: "tenant_a", AccountID: "account_a", AccountType: "user"}) {
		t.Fatal("legacy unowned trace must not be claimable by a new append")
	}
}
