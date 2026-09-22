// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package factory provides connector factory for creating data source connectors.
package factory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/remote"
)

var (
	cFactoryOnce sync.Once
	cFactory     interfaces.ConnectorFactory

	// ErrConnectorUnavailable indicates that the requested connector is not
	// implemented by the running binary. Database records can outlive connector
	// implementations during a binary downgrade, so this condition must not
	// prevent the service from starting.
	ErrConnectorUnavailable = errors.New("connector unavailable in this binary")

	// ErrConnectorEntitlementDenied indicates that the connector implementation
	// exists in this binary but requires an edition unavailable to this process.
	ErrConnectorEntitlementDenied = errors.New("connector entitlement denied")

	// ErrConnectorDisabled indicates that an otherwise available connector is
	// disabled by its persisted connector-type configuration.
	ErrConnectorDisabled = errors.New("connector disabled")
)

// ConnectorFactory creates and manages data source connectors
// Supports two modes: local and remote
// -local: A connector that runs built-in within the vega-backend process
// -remote: A connector that runs as an independent service and is invoked via HTTP
type connectorFactory struct {
	mu sync.RWMutex

	appSetting *common.AppSetting
	cta        interfaces.ConnectorTypeAccess // Database Access layer

	connectors                map[string]interfaces.Connector // Built-in connector builder
	connectorRequiredEditions map[string]licverify.Edition

	localConnectorsFrozen bool
}

// NewConnectorFactory creates the process-wide connector factory in assembly state.
func NewConnectorFactory(appSetting *common.AppSetting) interfaces.ConnectorFactory {
	cFactoryOnce.Do(func() {
		cFactory = &connectorFactory{
			appSetting:                appSetting,
			cta:                       logics.CTA,
			connectors:                make(map[string]interfaces.Connector),
			connectorRequiredEditions: make(map[string]licverify.Edition),
		}
	})
	return cFactory
}

// RegisterConnector is a registered connector builder
func (cf *connectorFactory) RegisterConnector(ctx context.Context, tp string, ct *interfaces.ConnectorType) error {
	cf.mu.Lock()
	defer cf.mu.Unlock()

	connector, exist := cf.connectors[tp]
	if exist {
		if err := cf.validateConnectorRegistration(tp, ct, connector); err != nil {
			return err
		}
		if ct.Mode == interfaces.ConnectorModeLocal {
			connector.SetEnabled(ct.Enabled)
		} else {
			connector := remote.NewRemoteConnector(ct)
			cf.connectors[tp] = connector
		}
	} else {
		if ct.Mode == interfaces.ConnectorModeLocal {
			return fmt.Errorf("local connector %s:%s not implemented: %w", tp, ct.Name, ErrConnectorUnavailable)
		} else {
			connector := remote.NewRemoteConnector(ct)
			cf.connectors[tp] = connector
		}
	}
	return nil
}

func (cf *connectorFactory) validateConnectorRegistration(tp string, ct *interfaces.ConnectorType, connector interfaces.Connector) error {
	if tp != ct.Type {
		return fmt.Errorf("connector registration key mismatch: key=%s, requested=%s", tp, ct.Type)
	}
	registeredMode := connector.GetMode()
	registeredCategory := connector.GetCategory()
	if registeredMode != ct.Mode {
		return fmt.Errorf("connector type %s mode mismatch: registered=%s, requested=%s",
			ct.Type, registeredMode, ct.Mode)
	}
	if registeredCategory != ct.Category {
		return fmt.Errorf("connector type %s category mismatch: registered=%s, requested=%s",
			ct.Type, registeredCategory, ct.Category)
	}
	return nil
}

// ValidateConnectorTypeRegistration validates a connector type registration
// against the implementations assembled in the running binary. Field
// definitions are intentionally not resolved here; they are read on demand by
// GetConnectorFieldConfig.
func (cf *connectorFactory) ValidateConnectorTypeRegistration(ct *interfaces.ConnectorType) error {
	if ct == nil {
		return fmt.Errorf("connector type registration is nil")
	}

	cf.mu.RLock()
	defer cf.mu.RUnlock()

	connector, exists := cf.connectors[ct.Type]
	if exists {
		if err := cf.validateConnectorRegistration(ct.Type, ct, connector); err != nil {
			return err
		}
	}
	if ct.Mode == interfaces.ConnectorModeRemote {
		return nil
	}

	if !exists {
		return fmt.Errorf("local connector %s:%s not implemented: %w", ct.Type, ct.Name, ErrConnectorUnavailable)
	}

	return nil
}

// GetConnectorFieldConfig returns runtime field semantics without applying
// registration validation or mutating connector registration state.
func (cf *connectorFactory) GetConnectorFieldConfig(ctx context.Context, ct *interfaces.ConnectorType) (map[string]interfaces.ConnectorFieldConfig, error) {
	if ct == nil {
		return nil, fmt.Errorf("connector type is nil")
	}
	if ct.Mode == interfaces.ConnectorModeRemote {
		return remote.GetFieldConfig(ctx, ct)
	}

	cf.mu.RLock()
	defer cf.mu.RUnlock()
	connector, exists := cf.connectors[ct.Type]
	if !exists {
		return nil, fmt.Errorf("local connector %s:%s not implemented: %w", ct.Type, ct.Name, ErrConnectorUnavailable)
	}
	if minimumEdition, hasMinimumEdition := cf.connectorRequiredEditions[ct.Type]; hasMinimumEdition && !entitlement.AtLeast(minimumEdition) {
		return nil, fmt.Errorf("local connector %s:%s requires edition %s: %w", ct.Type, ct.Name, minimumEdition, ErrConnectorEntitlementDenied)
	}
	if err := cf.validateConnectorRegistration(ct.Type, ct, connector); err != nil {
		return nil, err
	}
	return connector.GetFieldConfig(), nil
}

// DeleteConnector deletes the connector builder
func (cf *connectorFactory) DeleteConnector(tp string) {
	cf.mu.Lock()
	defer cf.mu.Unlock()

	connector, exist := cf.connectors[tp]
	if !exist {
		logger.Infof("Skip runtime connector deletion for %s: not registered in this process", tp)
		return
	}
	if connector.GetMode() == interfaces.ConnectorModeLocal {
		// Local connectors represent capabilities compiled into the binary.
		// Deleting their database registration must not remove the implementation.
		logger.Infof("Skip runtime connector deletion for %s: local connector implementations cannot be removed", tp)
		return
	}
	delete(cf.connectors, tp)
}

func (cf *connectorFactory) SetConnectorEnabled(tp string, enabled bool) {
	cf.mu.Lock()
	defer cf.mu.Unlock()

	connector, exist := cf.connectors[tp]
	if exist {
		connector.SetEnabled(enabled)
		return
	}
	logger.Infof("Skip runtime enabled update for connector %s: not registered in this process", tp)
}

// IsConnectorAvailable reports whether the running binary contains a registered
// implementation for the connector type.
func (cf *connectorFactory) IsConnectorAvailable(tp string) bool {
	cf.mu.RLock()
	defer cf.mu.RUnlock()

	_, exists := cf.connectors[tp]
	if !exists {
		return false
	}
	minimumEdition, hasMinimumEdition := cf.connectorRequiredEditions[tp]
	return !hasMinimumEdition || entitlement.AtLeast(minimumEdition)
}

// CreateConnector creates connector instances based on the type name
func (cf *connectorFactory) CreateConnectorInstance(ctx context.Context, tp string, cfg interfaces.ConnectorConfig) (interfaces.Connector, error) {
	cf.mu.Lock()
	defer cf.mu.Unlock()

	if connector, ok := cf.connectors[tp]; ok {
		if minimumEdition, hasMinimumEdition := cf.connectorRequiredEditions[tp]; hasMinimumEdition && !entitlement.AtLeast(minimumEdition) {
			return nil, fmt.Errorf("connector %s requires edition %s: %w", tp, minimumEdition, ErrConnectorEntitlementDenied)
		}
		if !connector.GetEnabled() {
			return nil, fmt.Errorf("connector %s is disabled: %w", tp, ErrConnectorDisabled)
		}

		cntor, err := connector.New(cfg)
		if err != nil {
			return nil, err
		}
		return cntor, nil
	}
	return nil, fmt.Errorf("connector %s not found: %w", tp, ErrConnectorUnavailable)
}

// GetSensitiveFields returns a list of sensitive fields based on the connector type
func (cf *connectorFactory) GetSensitiveFields(tp string) []string {
	cf.mu.RLock()
	defer cf.mu.RUnlock()

	if connector, ok := cf.connectors[tp]; ok {
		if minimumEdition, hasMinimumEdition := cf.connectorRequiredEditions[tp]; hasMinimumEdition && !entitlement.AtLeast(minimumEdition) {
			return nil
		}
		return connector.GetSensitiveFields()
	}
	return nil
}
