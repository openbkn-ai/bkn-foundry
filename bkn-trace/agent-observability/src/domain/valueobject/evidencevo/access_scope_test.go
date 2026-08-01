package evidencevo

import (
	"reflect"
	"testing"
)

func TestAccessProfileCanReadOwnAndDelegatedBusinessRecord(t *testing.T) {
	record := RecordScope{
		TenantID: "tenant-a", BusinessDomain: "domain-a",
		EffectiveSubjectID: "user-a", ApplicationPrincipalID: "app-a",
	}

	for _, profile := range []AccessProfile{
		{
			TenantID: "tenant-a", BusinessDomain: "domain-a", AccountActive: true, TenantActive: true,
			EffectiveSubjectID: "user-a", Roles: []string{"normal_user"},
		},
		{
			TenantID: "tenant-a", BusinessDomain: "domain-a", AccountActive: true, TenantActive: true,
			EffectiveSubjectID: "user-a", ActorID: "assistant-a", DelegationID: "delegation-a",
			Roles: []string{"normal_user"},
		},
	} {
		if !CanReadRecord(profile, record, AccessViewBusiness) {
			t.Fatalf("own or trusted delegated record must be readable: profile=%+v", profile)
		}
	}
}

func TestAccessProfileCanReadOwnApplicationBusinessRecord(t *testing.T) {
	profile := AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", AccountActive: true, TenantActive: true,
		ApplicationPrincipalID: "app-a",
	}
	record := RecordScope{
		TenantID: "tenant-a", BusinessDomain: "domain-a",
		EffectiveSubjectID: "service-a", ApplicationPrincipalID: "app-a",
	}
	if !CanReadRecord(profile, record, AccessViewBusiness) {
		t.Fatal("an application principal must read records produced by the same application")
	}
}

func TestNetworkBuilderMustManageEveryKnowledgeNetwork(t *testing.T) {
	record := RecordScope{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "other-user",
		KnowledgeNetworkIDs: []string{"kn-a", "kn-b"},
	}
	base := AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", AccountActive: true, TenantActive: true,
		EffectiveSubjectID: "builder-a", Roles: []string{"network_builder"},
	}

	all := base
	all.ManagedKnowledgeNetworkIDs = []string{"kn-a", "kn-b", "kn-c"}
	if !CanReadRecord(all, record, AccessViewBusiness) {
		t.Fatal("network_builder managing every associated network must read the record")
	}

	partial := base
	partial.ManagedKnowledgeNetworkIDs = []string{"kn-a"}
	if CanReadRecord(partial, record, AccessViewBusiness) {
		t.Fatal("network_builder managing only part of a multi-network record must be denied")
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
		TenantID: "tenant-a", BusinessDomain: "domain-a", AccountActive: true, TenantActive: true,
		EffectiveSubjectID: "builder-a", Roles: []string{"network_builder"},
	}
	record := RecordScope{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "other-user",
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

func TestPlatformRolesDoNotGrantBusinessContent(t *testing.T) {
	record := RecordScope{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "user-a",
		KnowledgeNetworkIDs: []string{"kn-a"},
	}
	for _, role := range []string{"admin", "security", "audit", "super_admin"} {
		profile := AccessProfile{
			TenantID: "tenant-a", BusinessDomain: "domain-a", AccountActive: true, TenantActive: true,
			EffectiveSubjectID: role + "-account", Roles: []string{role},
		}
		if CanReadRecord(profile, record, AccessViewBusiness) {
			t.Fatalf("platform role %q must not imply access to another subject's business content", role)
		}
	}
}

func TestRoleViewsRemainSeparated(t *testing.T) {
	record := RecordScope{TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: "user-a"}
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
			TenantID: "tenant-a", BusinessDomain: "domain-a", AccountActive: true, TenantActive: true,
			EffectiveSubjectID: test.role + "-account", Roles: []string{test.role},
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
	admin := &AccessProfile{Roles: []string{"admin"}}
	if NeedsCrossAccountCandidates(QueryScope{AccessProfile: admin, View: AccessViewBusiness}) {
		t.Fatal("admin business lookup must remain owner-scoped")
	}
	if !NeedsCrossAccountCandidates(QueryScope{AccessProfile: admin, View: AccessViewTechnical}) {
		t.Fatal("admin technical lookup needs platform technical candidates")
	}
}

func TestAccessProfileFailsClosedForInvalidIdentityBoundary(t *testing.T) {
	record := RecordScope{
		TenantID: "tenant-a", BusinessDomain: "domain-a",
		EffectiveSubjectID: "user-a", ApplicationPrincipalID: "app-a",
	}
	valid := AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", AccountActive: true, TenantActive: true,
		EffectiveSubjectID: "user-a", Roles: []string{"normal_user"},
	}
	tests := []AccessProfile{
		{},
		func() AccessProfile { value := valid; value.AccountActive = false; return value }(),
		func() AccessProfile { value := valid; value.TenantActive = false; return value }(),
		func() AccessProfile { value := valid; value.TenantID = "tenant-b"; return value }(),
		func() AccessProfile { value := valid; value.BusinessDomain = "domain-b"; return value }(),
	}
	for _, profile := range tests {
		if CanReadRecord(profile, record, AccessViewBusiness) {
			t.Fatalf("invalid identity boundary must fail closed: profile=%+v", profile)
		}
	}
}

func TestMatchesScopeUsesRecordAccessProfile(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace-a", RequestID: "request-a",
		TenantID: "tenant-a", BusinessDomain: "domain-a",
		AccountID: "other-user", AccountType: "user", EffectiveSubjectID: "other-user",
		KnowledgeNetworkIDs: []string{"kn-a", "kn-b"},
	}
	scope := QueryScope{
		View: AccessViewBusiness,
		AccessProfile: &AccessProfile{
			TenantID: "tenant-a", BusinessDomain: "domain-a", AccountActive: true, TenantActive: true,
			EffectiveSubjectID: "builder-a", Roles: []string{"network_builder"},
			ManagedKnowledgeNetworkIDs: []string{"kn-a", "kn-b"},
		},
	}
	if !MatchesScope(trace, scope) {
		t.Fatal("trace matching must use the shared record access decision")
	}
	scope.AccessProfile.ManagedKnowledgeNetworkIDs = []string{"kn-a"}
	if MatchesScope(trace, scope) {
		t.Fatal("trace matching must reject partial knowledge network management")
	}
}

func TestMatchesArtifactScopeDoesNotTreatPlatformRoleAsBusinessAccess(t *testing.T) {
	artifact := EvidenceArtifact{
		ArtifactID: "artifact-a", RequestID: "request-a",
		TenantID: "tenant-a", BusinessDomain: "domain-a",
		AccountID: "user-a", AccountType: "user", EffectiveSubjectID: "user-a",
		KnowledgeNetworkIDs: []string{"kn-a"},
	}
	scope := QueryScope{
		View: AccessViewBusiness,
		AccessProfile: &AccessProfile{
			TenantID: "tenant-a", BusinessDomain: "domain-a", AccountActive: true, TenantActive: true,
			EffectiveSubjectID: "admin-a", Roles: []string{"super_admin"},
		},
	}
	if MatchesArtifactScope(artifact, scope) {
		t.Fatal("platform role alone must not expose another subject's evidence artifact")
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
