// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/licverify"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func TestConnectorFactoryRegisterLocalConnector(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	t.Cleanup(entitlement.ResetForTest)

	t.Run("registers a connector and its aliases in one operation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockConnector(ctrl)
		connector.EXPECT().GetType().Return("private-db")
		connector.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		cf := &connectorFactory{
			connectors:                map[string]interfaces.Connector{},
			connectorRequiredEditions: map[string]licverify.Edition{},
		}

		cf.registerLocalConnector(connector, licverify.EditionProfessional, "legacy-private-db")

		assert.Same(t, connector, cf.connectors["private-db"])
		assert.Same(t, connector, cf.connectors["legacy-private-db"])
		assert.Equal(t, licverify.EditionProfessional, cf.connectorRequiredEditions["private-db"])
		assert.Equal(t, licverify.EditionProfessional, cf.connectorRequiredEditions["legacy-private-db"])
	})

	t.Run("rejects registration after finalization", func(t *testing.T) {
		cf := &connectorFactory{localConnectorsFrozen: true}

		assert.Panics(t, func() {
			cf.registerLocalConnector(nil, licverify.EditionProfessional)
		})
	})
}
