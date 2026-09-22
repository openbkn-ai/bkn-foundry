// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"

	"github.com/openbkn-ai/licverify"
)

//go:generate mockgen -source ../interfaces/connector_factory.go -destination ../interfaces/mock/mock_connector_factory.go

// ConnectorAvailability describes whether a connector can currently be used
// and the edition declared by its local implementation. RequiredEdition is
// empty for remote connectors and implementations not bundled in this binary.
type ConnectorAvailability struct {
	Available       bool
	RequiredEdition licverify.Edition
}

// ConnectorFactory owns the process-wide connector lifecycle. During startup,
// it assembles built-in and extension local connectors, then finalizes their
// persisted configuration before serving requests. At runtime, it validates
// connector types and creates, enables, and queries connector instances.
type ConnectorFactory interface {
	// RegisterCoreLocalConnectors adds the local connector implementations
	// compiled into the Foundry distribution.
	RegisterCoreLocalConnectors()

	// RegisterLocalConnector adds one local connector implementation during
	// startup assembly with its minimum required edition.
	RegisterLocalConnector(connector Connector, minEdition licverify.Edition)

	// Finalize closes connector assembly and applies persisted connector-type
	// configuration before the service starts accepting requests.
	Finalize()

	// ValidateConnectorTypeRegistration verifies that a connector type is
	// compatible with an implementation available in this process.
	ValidateConnectorTypeRegistration(ct *ConnectorType) error

	// RegisterConnector applies one persisted connector-type configuration to
	// its local implementation or creates its remote implementation.
	RegisterConnector(ctx context.Context, tp string, ct *ConnectorType) error

	// DeleteConnector removes a runtime remote connector implementation.
	// Compiled local connector implementations are retained.
	DeleteConnector(tp string)

	// SetConnectorEnabled updates the enabled state of a registered connector.
	SetConnectorEnabled(tp string, enabled bool)

	// CreateConnectorInstance creates an enabled connector instance for the
	// supplied type and configuration.
	CreateConnectorInstance(ctx context.Context, tp string, cfg ConnectorConfig) (Connector, error)

	// GetConnectorAvailability returns the connector's current availability and
	// declared edition requirement in one consistent snapshot.
	GetConnectorAvailability(tp string) ConnectorAvailability

	// GetConnectorFieldConfig returns the runtime field semantics for a
	// connector type.
	GetConnectorFieldConfig(ctx context.Context, ct *ConnectorType) (map[string]ConnectorFieldConfig, error)

	// GetSensitiveFields returns the sensitive fields declared by a connector
	// type that is available under the current entitlement.
	GetSensitiveFields(tp string) []string
}
