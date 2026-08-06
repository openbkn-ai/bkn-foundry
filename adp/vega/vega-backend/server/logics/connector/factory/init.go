// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"vega-backend/interfaces"
	"vega-backend/logics/connector/local/fileset/anyshare"
	"vega-backend/logics/connector/local/index/opensearch"
	"vega-backend/logics/connector/local/table/mariadb"
	"vega-backend/logics/connector/local/table/postgresql"
	"vega-backend/logics/connector/local/table/sqlserver"
)

// initLocalConnectors 初始化本地 connector
func (cf *connectorFactory) initLocalConnectors() {
	cf.mu.Lock()
	defer cf.mu.Unlock()

	cf.connectors[interfaces.ConnectorTypeMySQL] = mariadb.NewMariaDBConnector()
	cf.connectors[interfaces.ConnectorTypeOpenSearch] = opensearch.NewOpenSearchConnector()
	cf.connectors[interfaces.ConnectorTypeMariaDB] = mariadb.NewMariaDBConnector()
	cf.connectors[interfaces.ConnectorTypePostgreSQL] = postgresql.NewPostgresqlConnector()
	cf.connectors[interfaces.ConnectorTypeSQLServer] = sqlserver.NewSQLServerConnector()
	cf.connectors[interfaces.ConnectorTypeAnyShare] = anyshare.NewAnyShareConnector()
}
