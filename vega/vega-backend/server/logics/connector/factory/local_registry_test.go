// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/licverify"
)

func TestRegisterLocalConnector(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	ResetLocalConnectorRegistrationsForTest()
	t.Cleanup(resetLocalConnectorRegistrationsForTest)

	RegisterLocalConnector(LocalConnectorRegistration{
		MinEdition: licverify.EditionProfessional,
		Type:       "sqlserver",
		New:        func() interfaces.Connector { return nil },
	})

	connectors := LocalConnectorRegistrations()
	require.Len(t, connectors, 1)
	assert.Equal(t, "sqlserver", connectors[0].Type)
	assert.Equal(t, licverify.EditionProfessional, connectors[0].MinEdition)
	assert.Empty(t, entitlement.Assembled())
}

func TestRegisterCoreLocalConnectors(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	ResetLocalConnectorRegistrationsForTest()
	t.Cleanup(resetLocalConnectorRegistrationsForTest)

	RegisterCoreLocalConnectors()
	RegisterCoreLocalConnectors()

	registrations := LocalConnectorRegistrations()
	require.Len(t, registrations, 4)
	assert.Equal(t, interfaces.ConnectorTypeAnyShare, registrations[0].Type)
	assert.Equal(t, interfaces.ConnectorTypeMariaDB, registrations[1].Type)
	assert.Equal(t, []string{interfaces.ConnectorTypeMySQL}, registrations[1].Aliases)
	assert.Equal(t, interfaces.ConnectorTypeOpenSearch, registrations[2].Type)
	assert.Equal(t, interfaces.ConnectorTypePostgreSQL, registrations[3].Type)
	for _, registration := range registrations {
		assert.Equal(t, licverify.EditionCommunity, registration.MinEdition)
	}
}

func TestRegisterLocalConnectorRejectsInvalidAssembly(t *testing.T) {
	tests := []struct {
		name         string
		registration LocalConnectorRegistration
		message      string
	}{
		{
			name:         "empty type",
			registration: LocalConnectorRegistration{MinEdition: licverify.EditionProfessional, New: func() interfaces.Connector { return nil }},
			message:      "empty type",
		},
		{
			name:         "missing constructor",
			registration: LocalConnectorRegistration{MinEdition: licverify.EditionProfessional, Type: "sqlserver"},
			message:      "without a constructor",
		},
		{
			name:         "missing minimum edition",
			registration: LocalConnectorRegistration{Type: "sqlserver", New: func() interfaces.Connector { return nil }},
			message:      "unknown minimum edition",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
			ResetLocalConnectorRegistrationsForTest()
			t.Cleanup(resetLocalConnectorRegistrationsForTest)

			assert.Panics(t, func() {
				RegisterLocalConnector(test.registration)
			}, test.message)
		})
	}
}

func TestRegisterLocalConnectorRejectsDuplicateAndLateRegistration(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	ResetLocalConnectorRegistrationsForTest()
	t.Cleanup(resetLocalConnectorRegistrationsForTest)

	registration := LocalConnectorRegistration{
		MinEdition: licverify.EditionProfessional,
		Type:       "sqlserver",
		New:        func() interfaces.Connector { return nil },
	}
	RegisterLocalConnector(registration)
	assert.Panics(t, func() { RegisterLocalConnector(registration) })

	FreezeLocalConnectorRegistrations()
	assert.Panics(t, func() {
		RegisterLocalConnector(LocalConnectorRegistration{
			MinEdition: licverify.EditionProfessional,
			Type:       "hana",
			New:        func() interfaces.Connector { return nil },
		})
	})
}

func resetLocalConnectorRegistrationsForTest() {
	entitlement.ResetForTest()
	ResetLocalConnectorRegistrationsForTest()
}
