// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package connector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/licverify"
)

func TestRegisterLocal(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	ResetForTest()
	t.Cleanup(func() {
		entitlement.ResetForTest()
		ResetForTest()
	})

	RegisterLocal(LocalConnector{
		Capability: "connector_certified",
		MinEdition: licverify.EditionProfessional,
		Type:       "sqlserver",
		New:        func() interfaces.Connector { return nil },
	})

	connectors := LocalConnectors()
	require.Len(t, connectors, 1)
	assert.Equal(t, "sqlserver", connectors[0].Type)
	assert.Equal(t, "connector_certified", connectors[0].Capability)
	assert.Equal(t, licverify.EditionProfessional, connectors[0].MinEdition)
	assert.Equal(t, []entitlement.AssembledCap{{Name: "connector_certified", MinEdition: licverify.EditionProfessional}}, entitlement.Assembled())
}

func TestRegisterLocalRejectsInvalidAssembly(t *testing.T) {
	tests := []struct {
		name      string
		connector LocalConnector
		message   string
	}{
		{
			name:      "empty type",
			connector: LocalConnector{Capability: "connector_certified", MinEdition: licverify.EditionProfessional, New: func() interfaces.Connector { return nil }},
			message:   "empty type",
		},
		{
			name:      "missing constructor",
			connector: LocalConnector{Capability: "connector_certified", MinEdition: licverify.EditionProfessional, Type: "sqlserver"},
			message:   "without a constructor",
		},
		{
			name:      "missing capability",
			connector: LocalConnector{MinEdition: licverify.EditionProfessional, Type: "sqlserver", New: func() interfaces.Connector { return nil }},
			message:   "without a capability name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
			ResetForTest()
			t.Cleanup(func() {
				entitlement.ResetForTest()
				ResetForTest()
			})

			assert.Panics(t, func() {
				RegisterLocal(test.connector)
			}, test.message)
		})
	}
}

func TestRegisterLocalRejectsDuplicateAndLateRegistration(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	ResetForTest()
	t.Cleanup(func() {
		entitlement.ResetForTest()
		ResetForTest()
	})

	registration := LocalConnector{
		Capability: "connector_certified",
		MinEdition: licverify.EditionProfessional,
		Type:       "sqlserver",
		New:        func() interfaces.Connector { return nil },
	}
	RegisterLocal(registration)
	assert.Panics(t, func() { RegisterLocal(registration) })

	Freeze()
	assert.Panics(t, func() {
		RegisterLocal(LocalConnector{
			Capability: "connector_certified",
			MinEdition: licverify.EditionProfessional,
			Type:       "hana",
			New:        func() interfaces.Connector { return nil },
		})
	})
}
