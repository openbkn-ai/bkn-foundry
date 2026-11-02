package sso

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestDepartmentClaimMissingDoesNotClearMembership(t *testing.T) {
	provider := &model.SSOProvider{DepartmentClaim: "departments"}
	profile := &profile{claims: map[string]json.RawMessage{}}
	if _, present, err := departmentNames(provider, profile); err != nil || present {
		t.Fatalf("missing claim should preserve membership: present=%v err=%v", present, err)
	}
	profile.claims["departments"] = json.RawMessage(`["research","platform"]`)
	names, present, err := departmentNames(provider, profile)
	if err != nil || !present || len(names) != 2 || names[0] != "research" {
		t.Fatalf("department claim not mapped: names=%v present=%v err=%v", names, present, err)
	}
}
