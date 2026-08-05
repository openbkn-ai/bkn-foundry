// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

//go:generate mockgen -source ../interfaces/connector_factory.go -destination ../interfaces/mock/mock_connector_factory.go

// ConnectorFactory defines connector registration lifecycle operations used by
// connector type management.
type ConnectorFactory interface {
	ResolveConnectorTypeRegistration(ctx context.Context, ct *ConnectorType) (*ConnectorType, error)
	RegisterConnector(ctx context.Context, tp string, ct *ConnectorType) error
	DeleteConnector(ctx context.Context, tp string) error
	SetConnectorEnabled(ctx context.Context, tp string, enabled bool) error
	CreateConnectorInstance(ctx context.Context, tp string, cfg ConnectorConfig) (Connector, error)

	GetSensitiveFields(tp string) []string
}
