// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencevo

import (
	"reflect"
	"testing"
)

func TestAccessProfileCanReadOwnAndDelegatedBusinessRecord(t *testing.T) {
	record := RecordScope{
		EffectiveSubjectID:     "user-a",
		ApplicationPrincipalID: "app-a",
	}

	for _, profile := range []AccessProfile{
		{
			AccountActive:      true,
			EffectiveSubjectID: "user-a",
			Roles:              []string{"normal_user"},
		},
		{
			AccountActive:      true,
			EffectiveSubjectID: "user-a",
			ActorID:            "assistant-a",
			DelegationID:       "delegation-a",
			Roles:              []string{"normal_user"},
		},
	} {
		if !CanReadRecord(profile, record, AccessViewBusiness) {
			t.Fatalf("own or trusted delegated record must be readable: profile=%+v", profile)
		}
	}
}

func TestAccessProfileCanReadTechnicalTraceForOwnOrManagedRecord(t *testing.T) {
	record := RecordScope{
		EffectiveSubjectID:  "owner-a",
		KnowledgeNetworkIDs: []string{"kn-a"},
	}
	base := AccessProfile{AccountActive: true}

	owner := base
	owner.EffectiveSubjectID = "owner-a"
	if !CanReadRecord(owner, record, AccessViewTechnical) {
		t.Fatal("record owner must be able to inspect the linked technical trace")
	}

	builder := base
	builder.EffectiveSubjectID = "builder-a"
	builder.Roles = []string{"network_builder"}
	builder.ManagedKnowledgeNetworkIDs = []string{"kn-a"}
	if !CanReadRecord(builder, record, AccessViewTechnical) {
		t.Fatal("network builder must be able to inspect traces for fully managed networks")
	}

	builder.ManagedKnowledgeNetworkIDs = []string{"kn-b"}
	if CanReadRecord(builder, record, AccessViewTechnical) {
		t.Fatal("network builder must not inspect traces outside the managed-network scope")
	}
}

func TestAccessProfileCanReadOwnApplicationBusinessRecord(t *testing.T) {
	profile := AccessProfile{
		AccountActive:          true,
		ApplicationPrincipalID: "app-a",
	}
	record := RecordScope{
		EffectiveSubjectID:     "service-a",
		ApplicationPrincipalID: "app-a",
	}
	if !CanReadRecord(profile, record, AccessViewBusiness) {
		t.Fatal("an application principal must read records produced by the same application")
	}
}

func TestNetworkBuilderCanReadCompleteRecordForManagedKnowledgeNetwork(t *testing.T) {
	record := RecordScope{
		EffectiveSubjectID:  "other-user",
		KnowledgeNetworkIDs: []string{"kn-a", "kn-b"},
	}
	base := AccessProfile{
		AccountActive:      true,
		EffectiveSubjectID: "builder-a",
		Roles:              []string{"network_builder"},
	}

	all := base
	all.ManagedKnowledgeNetworkIDs = []string{"kn-a", "kn-b", "kn-c"}
	if !CanReadRecord(all, record, AccessViewBusiness) {
		t.Fatal("network_builder managing every associated network must read the record")
	}

	partial := base
	partial.ManagedKnowledgeNetworkIDs = []string{"kn-a"}
	if !CanReadRecord(partial, record, AccessViewBusiness) {
		t.Fatal("a managed knowledge network match must authorize the complete Trace record")
	}

	if CanReadRecord(base, record, AccessViewBusiness) {
		t.Fatal("network_builder without a concrete managed network grant must be denied")
	}

	record.KnowledgeNetworkIDs = nil
	if CanReadRecord(all, record, AccessViewBusiness) {
		t.Fatal("cross-user record without a trusted knowledge network scope must fail closed")
	}
}

func TestNetworkBuilderTypeWideGrantDoesNotImplyBusinessContentAccess(t *testing.T) {
	profile := AccessProfile{
		AccountActive:      true,
		EffectiveSubjectID: "builder-a",
		Roles:              []string{"network_builder"},
	}
	record := RecordScope{
		EffectiveSubjectID:  "other-user",
		KnowledgeNetworkIDs: []string{"kn-a", "kn-b"},
	}
	if CanReadRecord(profile, record, AccessViewBusiness) {
		t.Fatal("type-wide management must not bypass concrete business content authorization")
	}

	record.KnowledgeNetworkIDs = nil
	if CanReadRecord(profile, record, AccessViewBusiness) {
		t.Fatal("type-wide management must not authorize a record without trusted knowledge network scope")
	}
	record.KnowledgeNetworkIDs = []string{""}
	if CanReadRecord(profile, record, AccessViewBusiness) {
		t.Fatal("type-wide management must reject an empty knowledge network identifier")
	}
}

func TestAdminReadsAllTraceRecordsWithoutGrantingAuditRolesBusinessContent(t *testing.T) {
	record := RecordScope{
		EffectiveSubjectID:  "user-a",
		KnowledgeNetworkIDs: []string{"kn-a"},
	}
	for _, test := range []struct {
		role    string
		allowed bool
	}{
		{role: "admin", allowed: true},
		{role: "super_admin", allowed: true},
		{role: "security", allowed: false},
		{role: "audit", allowed: false},
	} {
		profile := AccessProfile{
			AccountActive:      true,
			EffectiveSubjectID: test.role + "-account",
			Roles:              []string{test.role},
		}
		if got := CanReadRecord(profile, record, AccessViewBusiness); got != test.allowed {
			t.Fatalf("role %q business Trace access = %v, want %v", test.role, got, test.allowed)
		}
	}
}

func TestRoleViewsRemainSeparated(t *testing.T) {
	record := RecordScope{EffectiveSubjectID: "user-a"}
	tests := []struct {
		role    string
		view    AccessView
		allowed bool
	}{
		{role: "admin", view: AccessViewTechnical, allowed: true},
		{role: "super_admin", view: AccessViewTechnical, allowed: true},
		{role: "security", view: AccessViewSecurity, allowed: true},
		{role: "audit", view: AccessViewAudit, allowed: true},
		{role: "admin", view: AccessViewSecurity, allowed: false},
		{role: "security", view: AccessViewTechnical, allowed: false},
		{role: "audit", view: AccessViewTechnical, allowed: false},
	}
	for _, test := range tests {
		profile := AccessProfile{
			AccountActive:      true,
			EffectiveSubjectID: test.role + "-account",
			Roles:              []string{test.role},
		}
		if got := CanReadRecord(profile, record, test.view); got != test.allowed {
			t.Fatalf("role=%s view=%s: got %v want %v", test.role, test.view, got, test.allowed)
		}
	}
}

func TestCrossAccountCandidatesRequireAnExplicitAuthorizedView(t *testing.T) {
	builder := &AccessProfile{
		Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-a"},
	}
	if !NeedsCrossAccountCandidates(QueryScope{AccessProfile: builder, View: AccessViewBusiness}) {
		t.Fatal("managed-network business lookup needs cross-account candidates before record filtering")
	}
	typeWideBuilder := &AccessProfile{
		Roles: []string{"network_builder"},
	}
	if NeedsCrossAccountCandidates(QueryScope{AccessProfile: typeWideBuilder, View: AccessViewBusiness}) {
		t.Fatal("type-wide network management must not widen business provenance candidates")
	}
	admin := &AccessProfile{Roles: []string{"admin"}, AccountActive: true}
	if !NeedsCrossAccountCandidates(QueryScope{AccessProfile: admin, View: AccessViewBusiness}) {
		t.Fatal("admin business lookup must include all Trace candidates before record filtering")
	}
	if !NeedsCrossAccountCandidates(QueryScope{AccessProfile: admin, View: AccessViewTechnical}) {
		t.Fatal("admin technical lookup needs platform technical candidates")
	}
	superAdmin := &AccessProfile{Roles: []string{"super_admin"}, AccountActive: true}
	if !NeedsCrossAccountCandidates(QueryScope{AccessProfile: superAdmin, View: AccessViewBusiness}) ||
		!NeedsCrossAccountCandidates(QueryScope{AccessProfile: superAdmin, View: AccessViewTechnical}) {
		t.Fatal("super_admin must retain explicit global Trace access")
	}
}

func TestAccessProfileFailsClosedForInvalidIdentityBoundary(t *testing.T) {
	record := RecordScope{
		EffectiveSubjectID:     "user-a",
		ApplicationPrincipalID: "app-a",
	}
	valid := AccessProfile{
		AccountActive:      true,
		EffectiveSubjectID: "user-a",
		Roles:              []string{"normal_user"},
	}
	tests := []AccessProfile{
		{},
		func() AccessProfile { value := valid; value.AccountActive = false; return value }(),
		func() AccessProfile { value := valid; value.EffectiveSubjectID = "other-user"; return value }(),
	}
	for _, profile := range tests {
		if CanReadRecord(profile, record, AccessViewBusiness) {
			t.Fatalf("invalid identity boundary must fail closed: profile=%+v", profile)
		}
	}
}

func TestMatchesScopeUsesRecordAccessProfile(t *testing.T) {
	trace := NormalizedTrace{
		TraceID:             "trace-a",
		RequestID:           "request-a",
		AccountID:           "other-user",
		AccountType:         "user",
		EffectiveSubjectID:  "other-user",
		KnowledgeNetworkIDs: []string{"kn-a", "kn-b"},
	}
	scope := QueryScope{
		View: AccessViewBusiness,
		AccessProfile: &AccessProfile{
			AccountActive:              true,
			EffectiveSubjectID:         "builder-a",
			Roles:                      []string{"network_builder"},
			ManagedKnowledgeNetworkIDs: []string{"kn-a", "kn-b"},
		},
	}
	if !MatchesScope(trace, scope) {
		t.Fatal("trace matching must use the shared record access decision")
	}
	scope.AccessProfile.ManagedKnowledgeNetworkIDs = []string{"kn-a"}
	if !MatchesScope(trace, scope) {
		t.Fatal("one managed knowledge network match must authorize the complete Trace record")
	}
}

func TestMatchesArtifactScopeAllowsAdminToReadBusinessTrace(t *testing.T) {
	artifact := EvidenceArtifact{
		ArtifactID:          "artifact-a",
		RequestID:           "request-a",
		AccountID:           "user-a",
		AccountType:         "user",
		EffectiveSubjectID:  "user-a",
		KnowledgeNetworkIDs: []string{"kn-a"},
	}
	scope := QueryScope{
		View: AccessViewBusiness,
		AccessProfile: &AccessProfile{
			AccountActive:      true,
			EffectiveSubjectID: "admin-a",
			Roles:              []string{"super_admin"},
		},
	}
	if !MatchesArtifactScope(artifact, scope) {
		t.Fatal("admin must read the complete evidence artifact for an authorized Trace")
	}
}

func TestWithEventsDerivesStableKnowledgeNetworkScope(t *testing.T) {
	trace := WithEvents(NormalizedTrace{}, []EvidenceEvent{
		{Payload: map[string]any{
			"kn_id": "kn-direct",
			"business_refs": []any{
				"kn:kn-explicit",
				"object:kn-a:forecast",
				map[string]any{"ref_id": "property:kn-b:forecast:qty"},
			},
		}},
		{Payload: map[string]any{
			"message":       "diagnostic text mentions object:kn-phantom:forecast but is not a business ref",
			"model_outputs": []any{"property:kn-phantom:forecast:qty"},
			"business_refs": []any{"relation:kn-a:forecast_product", "logic:kn-c:forecast:sum"},
			"source_refs":   []any{"object:kn-source:forecast"},
			"resource_refs": []any{map[string]any{"ref_id": "resource:data-view-forecast"}},
			"field_refs":    []any{"property:kn-field:forecast:qty"},
			"output": map[string]any{
				"business_refs": []any{
					"object:kn-nested:forecast",
					map[string]any{
						"ref_id":  "object:kn-parent:forecast",
						"related": []any{map[string]any{"ref_id": "property:kn-deep:forecast:qty"}},
					},
				},
				"metadata": map[string]any{"kn_id": "kn-structured"},
			},
		}},
	})
	want := []string{
		"kn-a", "kn-b", "kn-c", "kn-deep", "kn-direct", "kn-explicit", "kn-field",
		"kn-nested", "kn-parent", "kn-source", "kn-structured",
	}
	if !reflect.DeepEqual(trace.KnowledgeNetworkIDs, want) {
		t.Fatalf("unexpected knowledge network scope: got %v want %v", trace.KnowledgeNetworkIDs, want)
	}
}

func TestNormalizeArtifactDerivesKnowledgeNetworkScopeFromBusinessRefs(t *testing.T) {
	artifact, _ := NormalizeArtifact(EvidenceArtifact{
		SourceRef:           "object:kn-source:forecast",
		BusinessRefs:        []string{"object:kn-a:forecast", "kn:kn-b"},
		KnowledgeNetworkIDs: []string{"forged-network"},
	})
	want := []string{"kn-a", "kn-b", "kn-source"}
	if !reflect.DeepEqual(artifact.KnowledgeNetworkIDs, want) {
		t.Fatalf("artifact scope must be derived from canonical refs, got %v want %v", artifact.KnowledgeNetworkIDs, want)
	}
}
