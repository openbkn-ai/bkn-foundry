// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

func TestAccessProfileResponseUsesTheR62RoleMatrix(t *testing.T) {
	tests := []struct {
		name              string
		roles             []string
		managedNetworks   []string
		globalSearch      bool
		categories        []string
		sensitiveFields   bool
		export            bool
		policyRead        bool
		managedProvenance bool
	}{
		{name: "normal user", categories: []string{}},
		{
			name: "network builder", roles: []string{"network_builder"}, managedNetworks: []string{"kn-a"},
			globalSearch: true, categories: []string{"runtime.system", "runtime.business", "runtime.model"},
			export: true, managedProvenance: true,
		},
		{
			name: "admin", roles: []string{"admin"}, globalSearch: true,
			categories:      []string{"runtime.system", "runtime.business", "runtime.model"},
			sensitiveFields: true, export: true, policyRead: true,
		},
		{
			name: "security", roles: []string{"security"}, globalSearch: true,
			categories:      []string{"access.user", "audit.security"},
			sensitiveFields: true, export: true, policyRead: true,
		},
		{
			name: "audit", roles: []string{"audit"}, globalSearch: true,
			categories:      []string{"access.user", "audit.admin", "audit.security"},
			sensitiveFields: true, export: true, policyRead: true,
		},
		{
			name: "super admin", roles: []string{"super_admin"}, globalSearch: true,
			categories: []string{
				"access.user", "audit.admin", "audit.security",
				"runtime.system", "runtime.business", "runtime.model",
			},
			sensitiveFields: true, export: true, policyRead: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := accessProfileResponse(evidencevo.AccessProfile{
				TenantID: "tenant-a", ActorID: "user-a", EffectiveSubjectID: "user-a",
				Roles: test.roles, ManagedKnowledgeNetworkIDs: test.managedNetworks,
				AccountActive: true, TenantActive: true, Fingerprint: "sha256:profile-a",
			})

			if !response.BusinessProvenanceOwn || !response.TechnicalTrace {
				t.Fatalf("active users must be able to inspect their own provenance and trace: %+v", response)
			}
			if response.BusinessProvenanceManagedNetworks != test.managedProvenance ||
				response.GlobalLogSearch != test.globalSearch ||
				!reflect.DeepEqual(response.AllowedLogCategories, test.categories) ||
				response.LogSensitiveFields != test.sensitiveFields || response.LogExport != test.export ||
				response.LogPolicyRead != test.policyRead {
				t.Fatalf("unexpected R6.2 profile: %+v", response)
			}
		})
	}
}

func TestAccessProfileResponseFailsClosedForInactiveIdentity(t *testing.T) {
	response := accessProfileResponse(evidencevo.AccessProfile{
		Roles: []string{"super_admin"}, AccountActive: false, TenantActive: true,
	})
	if response.BusinessProvenanceOwn || response.TechnicalTrace || response.GlobalLogSearch ||
		len(response.AllowedLogCategories) != 0 || response.LogSensitiveFields || response.LogExport ||
		response.LogPolicyRead {
		t.Fatalf("inactive identity received capabilities: %+v", response)
	}
}
