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

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/connector/remote"
)

var (
	cFactoryOnce sync.Once
	cFactory     interfaces.ConnectorFactory

	// ErrConnectorUnavailable indicates that the requested connector is not
	// implemented by the running binary. Database records can outlive connector
	// implementations during a binary downgrade, so this condition must not
	// prevent the service from starting.
	ErrConnectorUnavailable = errors.New("connector unavailable in this binary")
)

// ConnectorFactory creates and manages data source connectors
// Supports two modes: local and remote
// -local: A connector that runs built-in within the vega-backend process
// -remote: A connector that runs as an independent service and is invoked via HTTP
type connectorFactory struct {
	mu sync.RWMutex

	appSetting *common.AppSetting
	cta        interfaces.ConnectorTypeAccess // Database Access layer

	connectors map[string]interfaces.Connector // Built-in connector builder
}

// Init initializes the connector factory
func GetFactory(appSetting *common.AppSetting) interfaces.ConnectorFactory {
	cFactoryOnce.Do(func() {
		cf := &connectorFactory{
			appSetting: appSetting,
			cta:        logics.CTA,
			connectors: make(map[string]interfaces.Connector, 0),
		}

		cf.initLocalConnectors()
		cf.registerAllConnectors()
		cFactory = cf
	})
	return cFactory
}

// registerAllConnectors registers all connector builders
func (cf *connectorFactory) registerAllConnectors() {

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
	return exists
}

// CreateConnector creates connector instances based on the type name
func (cf *connectorFactory) CreateConnectorInstance(ctx context.Context, tp string, cfg interfaces.ConnectorConfig) (interfaces.Connector, error) {
	cf.mu.Lock()
	defer cf.mu.Unlock()

	if connector, ok := cf.connectors[tp]; ok {
		if !connector.GetEnabled() {
			return nil, fmt.Errorf("connector %s is disabled", tp)
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
		return connector.GetSensitiveFields()
	}
	return nil
}
