// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestSummaryOwnerWherePreservesLegacySubjectBoundary(t *testing.T) {
	where, args := summaryOwnerWhere("r", isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{
		AccountID: "subject-1", AccountType: "service",
	}})
	wantWhere := []string{
		"r.effective_subject_type=CONVERT(? USING ascii) COLLATE ascii_bin",
		"r.effective_subject_id=CONVERT(? USING ascii) COLLATE ascii_bin",
	}
	wantArgs := []any{"service", "subject-1"}
	if !reflect.DeepEqual(where, wantWhere) || !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("where=%v args=%v", where, args)
	}
}

func TestSummaryOwnerWherePinsASCIIIdentityParametersToASCIIBinaryCollation(t *testing.T) {
	where, _ := summaryOwnerWhere("c", isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{
		AccountID: "super_admin", AccountType: "service",
	}})
	for _, expected := range []string{
		"c.effective_subject_type=CONVERT(? USING ascii) COLLATE ascii_bin",
		"c.effective_subject_id=CONVERT(? USING ascii) COLLATE ascii_bin",
	} {
		if !contains(where, expected) {
			t.Fatalf("where=%v, missing %q", where, expected)
		}
	}
}

func TestSummaryOwnerWhereFailsClosedForNonASCIIAccountIdentity(t *testing.T) {
	where, args := summaryOwnerWhere("c", isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{
		AccountID: "业务用户", AccountType: "service",
	}})
	if !reflect.DeepEqual(where, []string{"1=0"}) || len(args) != 0 {
		t.Fatalf("non-ASCII account identity must not produce a lossy ASCII comparison: where=%v args=%v", where, args)
	}
}

func TestSummaryOwnerWhereFailsClosedForNonASCIIProfileIdentity(t *testing.T) {
	profile := evidencevo.AccessProfile{
		AccountActive:          true,
		EffectiveSubjectID:     "业务用户",
		ApplicationPrincipalID: "application-1",
	}
	where, args := summaryOwnerWhere("c", isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{
		AccessProfile: &profile,
	}})
	if !reflect.DeepEqual(where, []string{"1=0"}) || len(args) != 0 {
		t.Fatalf("non-ASCII profile identity must not widen the owner predicate: where=%v args=%v", where, args)
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestConversationSummaryReceiptExistsExcludesPendingAndUsesReceiptTime(t *testing.T) {
	from := time.Date(2026, 8, 19, 8, 30, 0, 0, time.UTC)
	to := time.Date(2026, 8, 19, 9, 30, 0, 0, time.UTC)
	clause, args := conversationSummaryReceiptExists(isessionstore.SummaryPageQuery{From: from, To: to})
	for _, required := range []string{
		"r.trace_id IS NOT NULL", "r.trace_id<>''", "r.request_id IS NOT NULL", "r.request_id<>''",
		"r.issued_at>=?", "r.issued_at<=?",
	} {
		if !strings.Contains(clause, required) {
			t.Fatalf("receipt predicate is missing %q: %s", required, clause)
		}
	}
	if strings.Contains(clause, "c.created_at") || !reflect.DeepEqual(args, []any{from, to}) {
		t.Fatalf("clause=%s args=%v", clause, args)
	}
}

func TestUsableSummaryReceiptRequiresProjectedRequestAndTraceIdentity(t *testing.T) {
	want := []string{
		"r.trace_id IS NOT NULL", "r.trace_id<>''", "r.request_id IS NOT NULL", "r.request_id<>''",
	}
	if got := usableSummaryReceiptPredicates("r"); !reflect.DeepEqual(got, want) {
		t.Fatalf("predicates=%v", got)
	}
}

func TestConversationSummaryExcludedAgentPredicateMatchesAllCanonicalAgentIdentities(t *testing.T) {
	clause, args := conversationSummaryExcludedAgentPredicate("c", []string{"analysis-agent", "", "analysis-agent", "claim-agent"})
	for _, expected := range []string{
		"c.agent_name IS NULL OR c.agent_name NOT IN (?,?)",
		"c.application_principal_id IS NULL OR c.application_principal_id NOT IN (CONVERT(? USING ascii) COLLATE ascii_bin,CONVERT(? USING ascii) COLLATE ascii_bin)",
		"c.effective_subject_id IS NULL OR c.effective_subject_id NOT IN (CONVERT(? USING ascii) COLLATE ascii_bin,CONVERT(? USING ascii) COLLATE ascii_bin)",
	} {
		if !strings.Contains(clause, expected) {
			t.Fatalf("predicate is missing %q: %s", expected, clause)
		}
	}
	if strings.Count(clause, "NOT IN (?,?)") != 1 {
		t.Fatalf("clause=%s", clause)
	}
	if !reflect.DeepEqual(args, []any{
		"analysis-agent", "claim-agent",
		"analysis-agent", "claim-agent",
		"analysis-agent", "claim-agent",
	}) {
		t.Fatalf("args=%v", args)
	}
}

func TestConversationSummaryExcludedAgentPredicatePinsASCIIIdentityParametersToASCIIBinaryCollation(t *testing.T) {
	clause, _ := conversationSummaryExcludedAgentPredicate("c", []string{"analysis-agent"})
	for _, expected := range []string{
		"c.application_principal_id IS NULL OR c.application_principal_id NOT IN (CONVERT(? USING ascii) COLLATE ascii_bin)",
		"c.effective_subject_id IS NULL OR c.effective_subject_id NOT IN (CONVERT(? USING ascii) COLLATE ascii_bin)",
	} {
		if !strings.Contains(clause, expected) {
			t.Fatalf("clause=%s, missing %q", clause, expected)
		}
	}
}

func TestConversationSummaryExcludedAgentPredicateDoesNotCompareNonASCIIValuesToASCIIColumns(t *testing.T) {
	clause, args := conversationSummaryExcludedAgentPredicate("c", []string{"业务溯源优化Agent", "business_provenance_optimizer"})
	for _, expected := range []string{
		"c.agent_name IS NULL OR c.agent_name NOT IN (?,?)",
		"c.application_principal_id IS NULL OR c.application_principal_id NOT IN (CONVERT(? USING ascii) COLLATE ascii_bin)",
		"c.effective_subject_id IS NULL OR c.effective_subject_id NOT IN (CONVERT(? USING ascii) COLLATE ascii_bin)",
	} {
		if !strings.Contains(clause, expected) {
			t.Fatalf("predicate is missing %q: %s", expected, clause)
		}
	}
	if !reflect.DeepEqual(args, []any{
		"业务溯源优化Agent", "business_provenance_optimizer",
		"business_provenance_optimizer",
		"business_provenance_optimizer",
	}) {
		t.Fatalf("args=%v", args)
	}
}

func TestConversationSummaryExcludedAgentPredicateOmitsASCIIColumnsForNonASCIIOnlyValues(t *testing.T) {
	clause, args := conversationSummaryExcludedAgentPredicate("c", []string{"业务溯源优化Agent"})
	if !strings.Contains(clause, "c.agent_name IS NULL OR c.agent_name NOT IN (?)") {
		t.Fatalf("agent name predicate is missing: %s", clause)
	}
	if strings.Contains(clause, "application_principal_id") || strings.Contains(clause, "effective_subject_id") {
		t.Fatalf("non-ASCII value must not be compared to ASCII columns: %s", clause)
	}
	if !reflect.DeepEqual(args, []any{"业务溯源优化Agent"}) {
		t.Fatalf("args=%v", args)
	}
}
