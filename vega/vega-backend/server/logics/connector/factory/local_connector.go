// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"context"
	"errors"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/fileset/anyshare"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/index/opensearch"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/postgresql"
	"github.com/openbkn-ai/licverify"
)

// RegisterLocalConnector registers one local connector directly with this
// factory. Availability is evaluated from the current license at use time,
// not during assembly.
func (cf *connectorFactory) RegisterLocalConnector(connector interfaces.Connector, minEdition licverify.Edition) {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	connectorType := cf.registerLocalConnectorLocked(connector, minEdition)
	entitlement.MarkAssembled(connectorType, minEdition)
}

// RegisterCoreLocalConnectors registers Foundry's built-in local connectors.
func (cf *connectorFactory) RegisterCoreLocalConnectors() {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	cf.registerLocalConnectorLocked(mariadb.NewMySQLConnector(), licverify.EditionCommunity)
	cf.registerLocalConnectorLocked(mariadb.NewMariaDBConnector(), licverify.EditionCommunity)
	cf.registerLocalConnectorLocked(opensearch.NewOpenSearchConnector(), licverify.EditionCommunity)
	cf.registerLocalConnectorLocked(postgresql.NewPostgresqlConnector(), licverify.EditionCommunity)
	cf.registerLocalConnectorLocked(anyshare.NewAnyShareConnector(), licverify.EditionCommunity)
}

func (cf *connectorFactory) registerLocalConnectorLocked(connector interfaces.Connector, minEdition licverify.Edition) string {
	if cf.localConnectorsFrozen {
		panic("vega connector factory: local connector registered after Finalize")
	}
	if connector == nil {
		panic("vega connector factory: local connector registered without a builder")
	}
	connectorType := connector.GetType()
	entitlement.MustBeAssembling("vega connector factory:" + connectorType)
	if connectorType == "" {
		panic("vega connector factory: local connector registered with an empty type")
	}
	if connector.GetMode() != interfaces.ConnectorModeLocal {
		panic(fmt.Sprintf("vega connector factory: %q must use local mode", connectorType))
	}
	if minEdition == "" || !minEdition.Known() {
		panic(fmt.Sprintf("vega connector factory: %q registered with an unknown minimum edition %q", connectorType, minEdition))
	}
	if _, exists := cf.connectors[connectorType]; exists {
		panic(fmt.Sprintf("vega connector factory: %q registered twice", connectorType))
	}

	cf.connectors[connectorType] = connector
	cf.connectorRequiredEditions[connectorType] = minEdition
	return connectorType
}

// applyPersistedConnectorTypes applies database connector-type configuration
// to assembled local builders and creates configured remote connectors.
func (cf *connectorFactory) applyPersistedConnectorTypes() {
	ctx := context.Background()
	cts, _, err := cf.cta.List(ctx, interfaces.ConnectorTypesQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{
			Limit: -1,
		},
	})
	if err != nil {
		panic(fmt.Errorf("failed to get all connector types: %w", err))
	}

	for _, ct := range cts {
		err = cf.RegisterConnector(ctx, ct.Type, ct)
		if errors.Is(err, ErrConnectorUnavailable) {
			logger.Warnf("Skipping connector type %s:%s because it is unavailable in this binary", ct.Type, ct.Name)
			continue
		}
		if err != nil {
			panic(fmt.Errorf("failed to register connector type %s:%s: %w", ct.Type, ct.Name, err))
		}
	}
}

// Finalize closes local connector assembly and applies persisted connector-type
// configuration before workers and HTTP handlers start.
func (cf *connectorFactory) Finalize() {
	cf.mu.Lock()
	if cf.localConnectorsFrozen {
		cf.mu.Unlock()
		panic("vega connector factory: Finalize called twice")
	}
	cf.localConnectorsFrozen = true
	cf.mu.Unlock()

	entitlement.Freeze()
	cf.applyPersistedConnectorTypes()
}
