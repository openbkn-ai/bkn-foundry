// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package remote provides HTTP-based remote connector implementations.
package remote

import (
	"context"

	"vega-backend/interfaces"
)

// ============================================
// RemoteConnector is a basic remote connector
// ============================================

// RemoteConnector implements the basic remote connector agent
type RemoteConnector struct {
	enabled  bool
	connType *interfaces.ConnectorType
	config   interfaces.ConnectorConfig
}

// NewRemoteConnector creates basic remote connectors
func NewRemoteConnector(ct *interfaces.ConnectorType) *RemoteConnector {
	return &RemoteConnector{
		enabled:  ct.Enabled,
		connType: ct,
	}
}

// GetType returns the connector type
func (r *RemoteConnector) GetType() string {
	return r.connType.Type
}

// GetName returns the connector name
func (r *RemoteConnector) GetName() string {
	return r.connType.Name
}

// GetMode returns the connector mode
func (r *RemoteConnector) GetMode() string {
	return r.connType.Mode
}

// GetCategory returns the connector category
func (r *RemoteConnector) GetCategory() string {
	return r.connType.Category
}

// GetEnabled returns whether the connector is enabled
func (r *RemoteConnector) GetEnabled() bool {
	return r.enabled
}

func (rc *RemoteConnector) SetEnabled(enabled bool) {
	rc.enabled = enabled
}

// GetSensitiveFields returns the sensitive fields for this remote connector.
// Falls back to default ["password"] if not configured.
func (rc *RemoteConnector) GetSensitiveFields() []string {
	return []string{"password"}
}

// GetFieldConfig returns the field configuration for this remote connector.
// Obtain the field configuration from ConnectorType
func (rc *RemoteConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return rc.connType.FieldConfig
}

// New creates a connector instance.
func (rc *RemoteConnector) New(cfg interfaces.ConnectorConfig) (interfaces.Connector, error) {
	return &RemoteConnector{
		enabled:  rc.enabled,
		connType: rc.connType,
		config:   cfg,
	}, nil
}

func (rc *RemoteConnector) Connect(ctx context.Context) error {
	return nil
}

func (rc *RemoteConnector) Close(ctx context.Context) error {
	return nil
}

func (rc *RemoteConnector) Ping(ctx context.Context) error {
	return nil
}

func (rc *RemoteConnector) TestConnection(ctx context.Context) error {
	return nil
}

// GetMetadata returns the metadata for the catalog (stub).
func (rc *RemoteConnector) GetMetadata(ctx context.Context) (map[string]any, error) {
	return nil, nil
}
