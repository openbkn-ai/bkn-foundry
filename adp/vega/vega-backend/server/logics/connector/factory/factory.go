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
	"reflect"
	"sync"

	"github.com/openbkn-ai/bkn-comm-go/logger"

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

// ConnectorFactory 创建和管理数据源连接器
// 支持 local 和 remote 两种模式:
// - local: 内置在 vega-backend 进程内运行的连接器
// - remote: 作为独立服务运行，通过 HTTP 调用的连接器
type connectorFactory struct {
	mu sync.RWMutex

	appSetting *common.AppSetting
	cta        interfaces.ConnectorTypeAccess // 数据库访问层

	connectors map[string]interfaces.Connector // 内置 connector 构建器
}

// Init 初始化 connector factory
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

// registerAllConnectors 注册所有 connector 构建器
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

// RegisterConnector 注册 connector 构建器
func (cf *connectorFactory) RegisterConnector(ctx context.Context, tp string, ct *interfaces.ConnectorType) error {
	cf.mu.Lock()
	defer cf.mu.Unlock()

	connector, exist := cf.connectors[tp]
	if exist {
		if err := cf.validateConnectorRegistration(tp, ct, connector); err != nil {
			return err
		}
		if ct.Mode == interfaces.ConnectorModeLocal {
			// 验证 FieldConfig 一致性（代码是单一真相来源）
			codeFieldConfig := connector.GetFieldConfig()
			if !reflect.DeepEqual(codeFieldConfig, ct.FieldConfig) {
				return fmt.Errorf("field config mismatch for local connector %s: code=%+v, registered=%+v",
					tp, codeFieldConfig, ct.FieldConfig)
			}
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

// ResolveConnectorTypeRegistration validates a connector type registration
// against the implementations assembled in the running binary. Only local
// connector definitions come from code; non-local definitions remain exactly
// as supplied by the caller.
func (cf *connectorFactory) ResolveConnectorTypeRegistration(_ context.Context,
	ct *interfaces.ConnectorType) (*interfaces.ConnectorType, error) {
	if ct == nil {
		return nil, fmt.Errorf("connector type registration is nil")
	}

	cf.mu.RLock()
	defer cf.mu.RUnlock()

	connector, exists := cf.connectors[ct.Type]
	if exists {
		if err := cf.validateConnectorRegistration(ct.Type, ct, connector); err != nil {
			return nil, err
		}
	}
	if ct.Mode != interfaces.ConnectorModeLocal {
		resolved := *ct
		return &resolved, nil
	}

	if !exists {
		return nil, fmt.Errorf("local connector %s:%s not implemented: %w", ct.Type, ct.Name, ErrConnectorUnavailable)
	}

	resolved := *ct
	resolved.FieldConfig = connector.GetFieldConfig()
	return &resolved, nil
}

// DeleteConnector 删除 connector 构建器
func (cf *connectorFactory) DeleteConnector(tp string) {
	cf.mu.Lock()
	defer cf.mu.Unlock()

	connector, exist := cf.connectors[tp]
	if !exist {
		return
	}
	if connector.GetMode() == interfaces.ConnectorModeLocal {
		// Local connectors represent capabilities compiled into the binary.
		// Deleting their database registration must not remove the implementation.
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
	}
}

// IsConnectorAvailable reports whether the running binary contains a registered
// implementation for the connector type.
func (cf *connectorFactory) IsConnectorAvailable(tp string) bool {
	cf.mu.RLock()
	defer cf.mu.RUnlock()

	_, exists := cf.connectors[tp]
	return exists
}

// CreateConnector 根据类型名称创建 connector 实例
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

// GetSensitiveFields 根据 connector 类型返回敏感字段列表
func (cf *connectorFactory) GetSensitiveFields(tp string) []string {
	cf.mu.RLock()
	defer cf.mu.RUnlock()

	if connector, ok := cf.connectors[tp]; ok {
		return connector.GetSensitiveFields()
	}
	return nil
}
