// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzresource

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

type stubProvider struct{}

func (stubProvider) List(context.Context, Query) (Page, error)    { return Page{}, nil }
func (stubProvider) Exists(context.Context, string) (bool, error) { return true, nil }

func withEdition(t *testing.T, edition licverify.Edition) {
	t.Helper()
	entitlement.ResetForTest()
	ResetForTest()
	t.Cleanup(func() {
		ResetForTest()
		entitlement.ResetForTest()
	})
	entitlement.SetGateForTest(entitlement.FixedGate(edition))
}

func TestLookupNeedsRegistrationAndTheLicensedEdition(t *testing.T) {
	withEdition(t, licverify.EditionCommunity)
	if _, ok := Lookup("knowledge_base"); ok {
		t.Fatal("an unregistered type was served")
	}

	Register("knowledge_base", "knowledge_base", licverify.EditionProfessional, stubProvider{})
	if _, ok := Lookup("knowledge_base"); ok {
		t.Fatal("a Professional type was served below Professional")
	}
	assembled := false
	for _, capability := range entitlement.Assembled() {
		if capability.Name == "knowledge_base" && capability.MinEdition == licverify.EditionProfessional {
			assembled = true
		}
	}
	if !assembled {
		t.Fatal("registration did not record the capability")
	}

	// The licence is read per call: upgrading needs no re-registration.
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))
	if _, ok := Lookup("knowledge_base"); !ok {
		t.Fatal("a licensed type was not served")
	}
	if _, ok := Lookup("other_type"); ok {
		t.Fatal("Lookup served a type nobody registered")
	}
}

func TestRegisterRejectsIncompleteRegistrations(t *testing.T) {
	withEdition(t, licverify.EditionProfessional)
	for name, register := range map[string]func(){
		"empty type":   func() { Register(" ", "knowledge_base", licverify.EditionProfessional, stubProvider{}) },
		"nil provider": func() { Register("knowledge_base", "knowledge_base", licverify.EditionProfessional, nil) },
		"no edition":   func() { Register("knowledge_base", "knowledge_base", "", stubProvider{}) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("Register did not panic")
				}
			}()
			register()
		})
	}
}
