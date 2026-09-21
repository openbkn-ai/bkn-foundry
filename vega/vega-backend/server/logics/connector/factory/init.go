// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"fmt"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// initLocalConnectors initializes the local connector
func (cf *connectorFactory) initLocalConnectors() {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	if cf.minimumEditions == nil {
		cf.minimumEditions = make(map[string]licverify.Edition)
	}

	for _, registration := range LocalConnectorRegistrations() {
		if _, exists := cf.connectors[registration.Type]; exists {
			panic(fmt.Sprintf("connector type %q is already registered", registration.Type))
		}
		connector := registration.New()
		if connector == nil {
			panic(fmt.Sprintf("connector type %q constructor returned nil", registration.Type))
		}
		if connector.GetType() != registration.Type {
			panic(fmt.Sprintf("connector type %q constructor returned %q", registration.Type, connector.GetType()))
		}
		if connector.GetMode() != interfaces.ConnectorModeLocal {
			panic(fmt.Sprintf("connector type %q must use local mode", registration.Type))
		}
		cf.connectors[registration.Type] = connector
		cf.minimumEditions[registration.Type] = registration.MinEdition
		for _, alias := range registration.Aliases {
			if _, exists := cf.connectors[alias]; exists {
				panic(fmt.Sprintf("connector type %q is already registered", alias))
			}
			cf.connectors[alias] = connector
			cf.minimumEditions[alias] = registration.MinEdition
		}
	}
}
