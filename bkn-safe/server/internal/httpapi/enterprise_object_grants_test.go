// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permobject"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

type inventoryAuthorizer struct{}

func (inventoryAuthorizer) Decide(context.Context, permobject.Request) (permobject.LocalOpinion, error) {
	return permobject.LocalOpinion{}, nil
}

type inventoryManager struct {
	entries  []permobject.InventoryEntry
	revoked  string
	operator string
	reason   string
}

func (m *inventoryManager) Inventory(context.Context, time.Time) ([]permobject.InventoryEntry, error) {
	return append([]permobject.InventoryEntry(nil), m.entries...), nil
}

func (m *inventoryManager) Revoke(_ context.Context, grantID, operatorID, reason string, _ time.Time) error {
	m.revoked, m.operator, m.reason = grantID, operatorID, reason
	return nil
}

func TestEnterpriseObjectGrantInventoryAndExactRevoke(t *testing.T) {
	permobject.ResetForTest()
	entitlement.ResetForTest()
	t.Cleanup(func() {
		permobject.ResetForTest()
		entitlement.ResetForTest()
	})
	community, _ := newCommunityServer(t)
	communityResponse := adminReq(t, community, http.MethodGet,
		"/api/safe/v1/admin/enterprise-object-grants", nil)

	manager := &inventoryManager{entries: []permobject.InventoryEntry{
		{GrantID: "ee-grant-1", RuleID: "rule-1", AccessorID: "user-1", SubjectType: "user",
			ResourceType: "catalog", ResourceID: "c-1", Operation: "view_detail", Effect: "allow",
			Classification: "dormant_experimental", ActivationState: "dormant",
			RuntimeEligible: false, InactiveReason: "administrator confirmation required"},
	}}
	permobject.Register(licverify.EditionEnterprise, inventoryAuthorizer{})
	permobject.RegisterManager(licverify.EditionEnterprise, manager)
	r, _, _, _ := newAdminServer(t)

	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))
	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/enterprise-object-grants", nil); w.Code != http.StatusNotFound ||
		communityResponse.Code != http.StatusNotFound || !bytes.Equal(w.Body.Bytes(), communityResponse.Body.Bytes()) {
		t.Fatalf("Professional inventory = %d %q; want Community-identical 404 %q",
			w.Code, w.Body.String(), communityResponse.Body.String())
	}

	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/enterprise-object-grants", nil); w.Code != http.StatusOK {
		t.Fatalf("Enterprise inventory = %d %s", w.Code, w.Body.String())
	}
	w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/enterprise-object-grants", map[string]any{
		"grant_id": "ee-grant-1", "reason": "access review",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("Enterprise revoke = %d %s", w.Code, w.Body.String())
	}
	if manager.revoked != "ee-grant-1" || manager.operator != adminSub || manager.reason != "access review" {
		t.Fatalf("exact revoke call = (%q, %q, %q)", manager.revoked, manager.operator, manager.reason)
	}
}
