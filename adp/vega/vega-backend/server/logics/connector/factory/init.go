// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"github.com/openbkn-ai/bkn-comm-go/logger"

	extconn "github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/extension/connector"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics/connector/local/fileset/anyshare"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics/connector/local/index/opensearch"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics/connector/local/table/mariadb"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics/connector/local/table/postgresql"
)

// InitLocalConnectors 初始化本地 connector
func (cf *ConnectorFactory) InitLocalConnectors() {
	cf.connectors[interfaces.ConnectorTypeMySQL] = mariadb.NewMariaDBConnector()
	cf.connectors[interfaces.ConnectorTypeOpenSearch] = opensearch.NewOpenSearchConnector()
	//cf.connectors[interfaces.ConnectorTypeOracle] = oracle.NewOracleConnector()
	cf.connectors[interfaces.ConnectorTypeMariaDB] = mariadb.NewMariaDBConnector()
	cf.connectors[interfaces.ConnectorTypePostgreSQL] = postgresql.NewPostgresqlConnector()
	cf.connectors[interfaces.ConnectorTypeAnyShare] = anyshare.NewAnyShareConnector()

	cf.initExtensionConnectors()
}

// initExtensionConnectors installs the connectors the enterprise code line
// registered on the extension socket.
//
// Installation is unconditional — the licence is not consulted here. The
// implementation goes into the map whatever certificate is in force, so that
// one installed later takes effect on the next request rather than on the next
// restart; whether a type may actually be used is decided per call, in
// CreateConnectorInstance and in the type service.
//
// Nothing enables these from the database. A built-in connector gets its
// Enabled flag from its t_connector_type row via RegisterAllConnectors; a paid
// connector deliberately has no row (see the extension/connector package
// comment for what a row would do to a community boot), so the flag is set
// here. The question the flag answers for a built-in — has an operator switched
// this off — is answered for a paid connector by the licence instead.
func (cf *ConnectorFactory) initExtensionConnectors() {
	for _, e := range extconn.All() {
		c := e.New()
		c.SetEnabled(true)
		cf.connectors[e.Type] = c
		logger.Infof("registered extension connector %s (capability %s, min edition %s)",
			e.Type, e.Capability, e.MinEdition)
	}
}
