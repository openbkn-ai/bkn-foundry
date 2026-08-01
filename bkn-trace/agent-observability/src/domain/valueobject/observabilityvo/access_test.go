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
