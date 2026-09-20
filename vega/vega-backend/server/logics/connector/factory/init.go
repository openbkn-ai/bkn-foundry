// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"fmt"

	extensionconnector "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/extension/connector"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/fileset/anyshare"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/index/opensearch"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/postgresql"
	"github.com/openbkn-ai/licverify"
)

// initLocalConnectors initializes the local connector
func (cf *connectorFactory) initLocalConnectors() {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	if cf.minimumEditions == nil {
		cf.minimumEditions = make(map[string]licverify.Edition)
	}

	cf.connectors[interfaces.ConnectorTypeMySQL] = mariadb.NewMariaDBConnector()
	cf.connectors[interfaces.ConnectorTypeOpenSearch] = opensearch.NewOpenSearchConnector()
	cf.connectors[interfaces.ConnectorTypeMariaDB] = mariadb.NewMariaDBConnector()
	cf.connectors[interfaces.ConnectorTypePostgreSQL] = postgresql.NewPostgresqlConnector()
	cf.connectors[interfaces.ConnectorTypeAnyShare] = anyshare.NewAnyShareConnector()

	for _, registration := range extensionconnector.LocalConnectors() {
		if _, exists := cf.connectors[registration.Type]; exists {
			panic(fmt.Sprintf("connector type %q is already provided by Vega core", registration.Type))
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
	}
}
