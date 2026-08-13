package observabilityvo

import (
	"reflect"
	"sort"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

func TestCapabilitiesFollowTheExistingSixRoleLogMatrix(t *testing.T) {
	tests := []struct {
		role       string
		categories []string
		global     bool
		policyRead bool
	}{
		{role: "normal_user", categories: []string{}, global: false, policyRead: false},
		{role: "network_builder", categories: []string{CategoryRuntimeBusiness, CategoryRuntimeModel, CategoryRuntimeSystem}, global: true, policyRead: false},
		{role: "admin", categories: []string{CategoryRuntimeBusiness, CategoryRuntimeModel, CategoryRuntimeSystem}, global: true, policyRead: true},
		{role: "security", categories: []string{CategoryAccessUser, CategoryAuditSecurity}, global: true, policyRead: true},
		{role: "audit", categories: []string{CategoryAccessUser, CategoryAuditAdmin, CategoryAuditSecurity}, global: true, policyRead: true},
		{role: "super_admin", categories: append([]string(nil), AllCategories...), global: true, policyRead: true},
	}
	for _, test := range tests {
		t.Run(test.role, func(t *testing.T) {
			profile := evidencevo.AccessProfile{
				TenantID: "tenant-a", EffectiveSubjectID: "subject-a", Roles: []string{test.role},
				AccountActive: true, TenantActive: true,
			}
			capabilities := CapabilitiesFor(profile)
			actual, expected := append([]string(nil), capabilities.AllowedLogCategories...), append([]string(nil), test.categories...)
			sort.Strings(actual)
			sort.Strings(expected)
			if !reflect.DeepEqual(actual, expected) || capabilities.GlobalLogSearch != test.global || capabilities.LogPolicyRead != test.policyRead {
				t.Fatalf("role matrix drift: %+v", capabilities)
			}
		})
	}
}

func TestInactiveAccountHasNoObservabilityCapabilities(t *testing.T) {
	capabilities := CapabilitiesFor(evidencevo.AccessProfile{Roles: []string{"super_admin"}})
	if capabilities.GlobalLogSearch || capabilities.TechnicalTrace || len(capabilities.AllowedLogCategories) != 0 {
		t.Fatalf("inactive account retained observability access: %+v", capabilities)
	}
}

func TestOperationAuditContractUsesSixProductModules(t *testing.T) {
	want := []string{
		"domain_knowledge_network",
		"observability",
		"execution_factory",
		"data_resource_knowledge_network",
		"model_management",
		"system_management",
	}
	if !reflect.DeepEqual(AllBusinessModules, want) {
		t.Fatalf("business module contract drifted: got=%v want=%v", AllBusinessModules, want)
	}
	for _, legacy := range []string{"identity", "authorization", "api_key", "agent_conversation", "skill_management"} {
		if IsBusinessModule(legacy) {
			t.Fatalf("legacy object-level module %q must not remain a business module", legacy)
		}
	}
}

func TestOperationAuditContractUsesCanonicalOutcomes(t *testing.T) {
	want := []string{"success", "failure", "denied", "canceled", "unknown"}
	if !reflect.DeepEqual(AllAuditOutcomes, want) {
		t.Fatalf("audit outcome contract drifted: got=%v want=%v", AllAuditOutcomes, want)
	}
	for _, legacy := range []string{"accepted", "partial_success", "cancelled"} {
		if IsAuditOutcome(legacy) {
			t.Fatalf("legacy outcome %q must not remain accepted", legacy)
		}
	}
}
