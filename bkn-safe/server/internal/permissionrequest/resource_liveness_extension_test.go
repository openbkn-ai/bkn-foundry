// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permissionrequest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/authzresource"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

type livenessProvider struct{ existing map[string]bool }

func (livenessProvider) List(context.Context, authzresource.Query) (authzresource.Page, error) {
	return authzresource.Page{}, nil
}

func (p livenessProvider) Exists(_ context.Context, id string) (bool, error) {
	if id == "down" {
		return false, errors.New("kb-api unavailable")
	}
	return p.existing[id], nil
}

func TestResourceLivenessAsksTheExtensionForTypesCoreDoesNotServe(t *testing.T) {
	entitlement.ResetForTest()
	authzresource.ResetForTest()
	t.Cleanup(func() {
		authzresource.ResetForTest()
		entitlement.ResetForTest()
	})
	resolver := &httpResourceLivenessResolver{endpoints: map[string]string{}}

	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))
	if _, err := resolver.Exists(t.Context(), "knowledge_base", "7"); !errors.Is(err, ErrUnsupportedResourceType) {
		t.Fatalf("unregistered type error = %v, want unsupported", err)
	}

	authzresource.Register("knowledge_base", "knowledge_base", licverify.EditionProfessional, livenessProvider{existing: map[string]bool{"7": true}})
	if exists, err := resolver.Exists(t.Context(), "knowledge_base", "7"); err != nil || !exists {
		t.Fatalf("Exists(7) = (%v, %v)", exists, err)
	}
	if exists, err := resolver.Exists(t.Context(), "knowledge_base", "8"); err != nil || exists {
		t.Fatalf("Exists(8) = (%v, %v), want gone", exists, err)
	}
	// An outage is an error (the service maps it to unavailable), never "deleted".
	if _, err := resolver.Exists(t.Context(), "knowledge_base", "down"); err == nil {
		t.Fatal("an upstream failure was reported as an answer")
	}

	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	if _, err := resolver.Exists(t.Context(), "knowledge_base", "7"); !errors.Is(err, ErrUnsupportedResourceType) {
		t.Fatalf("unlicensed type error = %v, want unsupported", err)
	}
}

type deadlineProvider struct{ deadline *time.Duration }

func (deadlineProvider) List(context.Context, authzresource.Query) (authzresource.Page, error) {
	return authzresource.Page{}, nil
}

func (p deadlineProvider) Exists(ctx context.Context, _ string) (bool, error) {
	if deadline, ok := ctx.Deadline(); ok {
		*p.deadline = time.Until(deadline)
	}
	return true, nil
}

func TestResourceLivenessBoundsExtensionCalls(t *testing.T) {
	entitlement.ResetForTest()
	authzresource.ResetForTest()
	t.Cleanup(func() {
		authzresource.ResetForTest()
		entitlement.ResetForTest()
	})
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))
	var remaining time.Duration
	authzresource.Register("knowledge_base", "knowledge_base", licverify.EditionProfessional, deadlineProvider{deadline: &remaining})

	// The caller's context has no deadline; core must add one.
	resolver := &httpResourceLivenessResolver{endpoints: map[string]string{}}
	if _, err := resolver.Exists(context.Background(), "knowledge_base", "7"); err != nil {
		t.Fatal(err)
	}
	if remaining <= 0 || remaining > authzresource.CallTimeout {
		t.Fatalf("provider deadline = %v, want within %v", remaining, authzresource.CallTimeout)
	}
}
