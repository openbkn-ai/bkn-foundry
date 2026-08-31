// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchevidencestore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

func TestScopeCandidateMustPushesDownBusinessRecordScope(t *testing.T) {
	profile := &evidencevo.AccessProfile{
		EffectiveSubjectID:         "builder-a",
		ApplicationPrincipalID:     "app-a",
		Roles:                      []string{"network_builder"},
		ManagedKnowledgeNetworkIDs: []string{"kn-a", "kn-b"},
		AccountActive:              true,
	}
	must := scopeCandidateMust(evidencevo.QueryScope{
		AccountID: "builder-a", AccountType: "user",
		View: evidencevo.AccessViewBusiness, AccessProfile: profile,
	})

	rendered := mustJSON(t, must)
	for _, expected := range []string{
		"effective_subject_id", "application_principal_id",
		"bkn.account.id", "bkn.account.type", "knowledge_network_ids", "kn-a", "kn-b",
		`"minimum_should_match":1`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("candidate query must contain %q: %s", expected, rendered)
		}
	}
}

func TestScopeCandidateMustDoesNotTreatTypeWideNetworkGrantAsBusinessContentAccess(t *testing.T) {
	profile := &evidencevo.AccessProfile{
		EffectiveSubjectID: "builder-a",
		Roles:              []string{"network_builder"},
		AccountActive:      true,
	}
	must := scopeCandidateMust(evidencevo.QueryScope{
		AccountID:     "builder-a",
		AccountType:   "user",
		View:          evidencevo.AccessViewBusiness,
		AccessProfile: profile,
	})

	rendered := mustJSON(t, must)
	if strings.Contains(rendered, "knowledge_network_ids") {
		t.Fatalf("type-wide management grant must not widen business content candidates: %s", rendered)
	}
	for _, expected := range []string{"effective_subject_id", "bkn.account.id", "bkn.account.type"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("own-record candidate condition %q is missing: %s", expected, rendered)
		}
	}
}

func TestScopeCandidateMustRequiresNetworkBuilderRoleForManagedNetworkCandidates(t *testing.T) {
	profile := &evidencevo.AccessProfile{
		EffectiveSubjectID:         "user-a",
		Roles:                      []string{"normal_user"},
		ManagedKnowledgeNetworkIDs: []string{"kn-a"},
		AccountActive:              true,
	}
	must := scopeCandidateMust(evidencevo.QueryScope{
		AccountID:     "user-a",
		AccountType:   "user",
		View:          evidencevo.AccessViewBusiness,
		AccessProfile: profile,
	})

	rendered := mustJSON(t, must)
	if strings.Contains(rendered, "knowledge_network_ids") {
		t.Fatalf("managed network candidates require the network_builder role: %s", rendered)
	}
}

func TestScopeCandidateMustAllowsExplicitGlobalTechnicalScan(t *testing.T) {
	profile := &evidencevo.AccessProfile{
		EffectiveSubjectID: "admin-a",
		Roles:              []string{"super_admin"},
		AccountActive:      true,
	}
	must := scopeCandidateMust(evidencevo.QueryScope{
		AccountID:     "admin-a",
		AccountType:   "user",
		View:          evidencevo.AccessViewTechnical,
		AccessProfile: profile,
	})

	rendered := mustJSON(t, must)
	if strings.Contains(rendered, "bkn.account.id") || strings.Contains(rendered, "effective_subject_id") {
		t.Fatalf("explicit technical view must not add business owner filters: %s", rendered)
	}
	if rendered != "null" {
		t.Fatalf("global technical candidates must not retain a removed deployment partition: %s", rendered)
	}
}

func TestScopeCandidateMustAllowsAdminGlobalScan(t *testing.T) {
	profile := &evidencevo.AccessProfile{
		EffectiveSubjectID: "admin-a",
		Roles:              []string{"admin"},
		AccountActive:      true,
	}
	must := scopeCandidateMust(evidencevo.QueryScope{
		AccountID:     "admin-a",
		AccountType:   "user",
		View:          evidencevo.AccessViewBusiness,
		AccessProfile: profile,
	})

	rendered := mustJSON(t, must)
	if rendered != "null" {
		t.Fatalf("admin candidates must not retain owner filters: %s", rendered)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
