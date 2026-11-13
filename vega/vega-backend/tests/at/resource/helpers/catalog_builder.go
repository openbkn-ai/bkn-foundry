// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package helpers

import (
	"vega-backend-tests/at/setup"
)

// ========== MariaDB Catalog Builder ==========

// MariaDBCatalogBuilder MariaDB Catalog payload构建器
type MariaDBCatalogBuilder struct {
	config        setup.MariaDBConfig
	connectorType string
}

// NewMariaDBCatalogBuilder 创建MariaDB Catalog构建器
func NewMariaDBCatalogBuilder(config setup.MariaDBConfig) *MariaDBCatalogBuilder {
	return &MariaDBCatalogBuilder{
		config:        config,
		connectorType: "mariadb",
	}
}

// GetConnectorType 返回connector类型
func (b *MariaDBCatalogBuilder) GetConnectorType() string {
	return b.connectorType
}

// GetPassword 获取明文密码，由服务端负责存储加密
func (b *MariaDBCatalogBuilder) GetPassword() string {
	return b.config.Password
}

// BuildCreatePayload 构建MariaDB catalog创建payload
func (b *MariaDBCatalogBuilder) BuildCreatePayload() map[string]any {
	return map[string]any{
		"name":           GenerateUniqueName("test-mariadb-catalog"),
		"connector_type": b.connectorType,
		"connector_config": map[string]any{
			"host":     b.config.Host,
			"port":     b.config.Port,
			"username": b.config.Username,
			"password": b.GetPassword(),
			"database": b.config.Database,
		},
	}
}

// ========== OpenSearch Catalog Builder ==========

// OpenSearchCatalogBuilder OpenSearch Catalog payload构建器
type OpenSearchCatalogBuilder struct {
	config        setup.OpenSearchConfig
	connectorType string
}

// NewOpenSearchCatalogBuilder 创建OpenSearch Catalog构建器
func NewOpenSearchCatalogBuilder(config setup.OpenSearchConfig) *OpenSearchCatalogBuilder {
	return &OpenSearchCatalogBuilder{
		config:        config,
		connectorType: "opensearch",
	}
}

// GetConnectorType 返回connector类型
func (b *OpenSearchCatalogBuilder) GetConnectorType() string {
	return b.connectorType
}

// GetPassword 获取明文密码，由服务端负责存储加密
func (b *OpenSearchCatalogBuilder) GetPassword() string {
	return b.config.Password
}

// BuildCreatePayload 构建OpenSearch catalog创建payload
func (b *OpenSearchCatalogBuilder) BuildCreatePayload() map[string]any {
	return map[string]any{
		"name":           GenerateUniqueName("test-opensearch-catalog"),
		"connector_type": b.connectorType,
		"connector_config": map[string]any{
			"host":     b.config.Host,
			"port":     b.config.Port,
			"username": b.config.Username,
			"password": b.GetPassword(),
			"use_ssl":  b.config.UseSSL,
		},
	}
}

// ========== Factory Function ==========

// NewCatalogPayloadBuilder 根据connector类型创建对应的CatalogPayloadBuilder
func NewCatalogPayloadBuilder(connectorType string, config *setup.TestConfig) CatalogPayloadBuilder {
	switch connectorType {
	case "mariadb":
		b := NewMariaDBCatalogBuilder(config.TargetMariaDB)
		return b
	case "opensearch":
		b := NewOpenSearchCatalogBuilder(config.TargetOpenSearch)
		return b
	default:
		return nil
	}
}
