// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package opensearch provides OpenSearch/ElasticSearch connector implementation.
package opensearch

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/mitchellh/mapstructure"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"

	"vega-backend/interfaces"
	"vega-backend/logics/connectors"
)

type opensearchConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	Username     string `mapstructure:"username"`
	Password     string `mapstructure:"password"`
	IndexPattern string `mapstructure:"index_pattern"`
}

// OpenSearchConnector implements IndexConnector for OpenSearch/ElasticSearch.
type OpenSearchConnector struct {
	enabled bool
	Config  *opensearchConfig
	client  *opensearch.Client
}

// NewOpenSearchConnector 创建 OpenSearch connector 构建器
func NewOpenSearchConnector() connectors.IndexConnector {
	return &OpenSearchConnector{}
}

// GetType returns the data source type.
func (c *OpenSearchConnector) GetType() string {
	return interfaces.ConnectorTypeOpenSearch
}

// GetName returns the data source name.
func (c *OpenSearchConnector) GetName() string {
	return interfaces.ConnectorTypeOpenSearch
}

// GetMode returns the connector mode.
func (c *OpenSearchConnector) GetMode() string {
	return interfaces.ConnectorModeLocal
}

// GetCategory returns the connector category.
func (c *OpenSearchConnector) GetCategory() string {
	return interfaces.ConnectorCategoryIndex
}

// GetEnabled returns the enabled status.
func (c *OpenSearchConnector) GetEnabled() bool {
	return c.enabled
}

// SetEnabled sets the enabled status.
func (c *OpenSearchConnector) SetEnabled(enabled bool) {
	c.enabled = enabled
}

// GetSensitiveFields returns the sensitive fields for OpenSearch connector.
func (c *OpenSearchConnector) GetSensitiveFields() []string {
	return []string{"password"}
}

// GetFieldConfig returns the field configuration for OpenSearch connector.
func (c *OpenSearchConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host":          {Name: "主机地址", Type: "string", Description: "OpenSearch 服务器主机地址", Required: true, Encrypted: false},
		"port":          {Name: "端口号", Type: "integer", Description: "OpenSearch 服务器端口", Required: true, Encrypted: false},
		"username":      {Name: "用户名", Type: "string", Description: "认证用户名", Required: false, Encrypted: false},
		"password":      {Name: "密码", Type: "string", Description: "认证密码", Required: false, Encrypted: true},
		"index_pattern": {Name: "索引模式", Type: "string", Description: "索引匹配模式（可选，如 log-*）", Required: false, Encrypted: false},
	}
}

// New creates a new OpenSearch connector.
func (c *OpenSearchConnector) New(cfg interfaces.ConnectorConfig) (connectors.Connector, error) {
	var osCfg opensearchConfig
	if err := mapstructure.Decode(cfg, &osCfg); err != nil {
		return nil, fmt.Errorf("failed to decode opensearch config: %w", err)
	}

	return &OpenSearchConnector{
		Config: &osCfg,
	}, nil
}

// Connect establishes connection to OpenSearch.
func (c *OpenSearchConnector) Connect(ctx context.Context) error {
	if c.client != nil {
		return nil
	}

	cfg := opensearch.Config{
		Addresses: []string{fmt.Sprintf("http://%s:%d", c.Config.Host, c.Config.Port)},
		Username:  c.Config.Username,
		Password:  c.Config.Password,
	}
	// TODO: Handle SSL/TLS options if needed

	client, err := opensearch.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create opensearch client: %w", err)
	}

	c.client = client
	return nil
}

// Close closes the connection.
func (c *OpenSearchConnector) Close(ctx context.Context) error {
	c.client = nil
	return nil
}

// Ping checks the connection.
func (c *OpenSearchConnector) Ping(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	req := opensearchapi.InfoRequest{}
	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.IsError() {
		return fmt.Errorf("ping failed: %s", resp.String())
	}
	return nil
}

// TestConnection tests the connection to OpenSearch.
func (c *OpenSearchConnector) TestConnection(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	return c.Ping(ctx)
}

// Create index
func (c *OpenSearchConnector) Create(ctx context.Context, name string, schemaDefinition []*interfaces.Property) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	exist, err := c.indexExist(ctx, name)
	if err != nil {
		return err
	}
	// index exist
	if exist {
		return fmt.Errorf("index %s already exist", name)
	}

	// 构建字段映射
	properties, hasVectorField, err := c.buildFieldMappings(schemaDefinition)
	if err != nil {
		return err
	}

	mappings := map[string]any{
		"properties": properties,
	}

	mapping := map[string]any{
		"mappings": mappings,
	}

	mapping["settings"] = map[string]any{
		"index": map[string]any{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
	}

	// 如果有vector字段，开启knn
	if hasVectorField {
		indexSettings := mapping["settings"].(map[string]any)["index"].(map[string]any)
		indexSettings["knn"] = true
	}

	data, err := sonic.Marshal(mapping)
	if err != nil {
		return err
	}
	createReq := opensearchapi.IndicesCreateRequest{
		Index: name,
		Body:  bytes.NewReader(data),
	}

	createResp, err := createReq.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer func() { _ = createResp.Body.Close() }()

	if createResp.IsError() {
		return fmt.Errorf("failed to create index: %s", createResp.String())
	}

	return nil
}

// Update index.
func (c *OpenSearchConnector) Update(ctx context.Context, name string, schemaDefinition []*interfaces.Property) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	exist, err := c.indexExist(ctx, name)
	if err != nil {
		return err
	}
	// index not exist
	if !exist {
		return fmt.Errorf("index %s not exist", name)
	}

	// 构建字段映射
	properties, _, err := c.buildFieldMappings(schemaDefinition)
	if err != nil {
		return err
	}

	// 构建properties映射
	mappings := map[string]any{
		"properties": properties,
	}

	// 构建 JSON 字符串
	data, err := sonic.Marshal(mappings)
	if err != nil {
		return err
	}
	updateReq := opensearchapi.IndicesPutMappingRequest{
		Index: []string{name},
		Body:  bytes.NewReader(data),
	}
	updateResp, err := updateReq.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer func() { _ = updateResp.Body.Close() }()

	if updateResp.IsError() {
		return fmt.Errorf("failed to update index mapping: %s", updateResp.String())
	}

	return nil
}

// Delete a Dataset.
func (c *OpenSearchConnector) Delete(ctx context.Context, name string) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	exist, err := c.CheckExist(ctx, name)
	if err != nil {
		return err
	}
	// index not exist
	if !exist {
		return nil
	}

	deleteReq := opensearchapi.IndicesDeleteRequest{
		Index: []string{name},
	}

	deleteResp, err := deleteReq.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer func() { _ = deleteResp.Body.Close() }()

	if deleteResp.IsError() {
		return fmt.Errorf("failed to delete index: %s", deleteResp.String())
	}

	return nil
}

// Check Index Exist
func (c *OpenSearchConnector) CheckExist(ctx context.Context, name string) (bool, error) {
	if err := c.Connect(ctx); err != nil {
		return false, err
	}

	return c.indexExist(ctx, name)
}

// Create Documents
func (c *OpenSearchConnector) CreateDocuments(ctx context.Context, name string, documents []map[string]any) ([]string, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	var bulkBody strings.Builder
	for _, doc := range documents {
		opMeta := map[string]map[string]string{
			"index": {
				"_index": name,
			},
		}
		// if _id in doc, use it as document id
		if docID, ok := doc["_id"].(string); ok {
			opMeta["index"]["_id"] = docID
			delete(doc, "_id")
		}

		if err := sonic.ConfigDefault.NewEncoder(&bulkBody).Encode(opMeta); err != nil {
			return nil, err
		}
		if err := sonic.ConfigDefault.NewEncoder(&bulkBody).Encode(doc); err != nil {
			return nil, err
		}
	}

	req := opensearchapi.BulkRequest{
		Body:    strings.NewReader(bulkBody.String()),
		Refresh: "true",
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return nil, fmt.Errorf("failed to create documents: %s", resp.String())
	}

	var result map[string]interface{}
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if errors, ok := result["errors"].(bool); ok && errors {
		// 遍历所有操作结果，检查是否有失败
		if items, ok := result["items"].([]interface{}); ok {
			for _, item := range items {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if indexResult, ok := itemMap["index"].(map[string]interface{}); ok {
						if errorObj, ok := indexResult["error"].(map[string]interface{}); ok {
							// 找到失败的文档，返回错误
							return nil, fmt.Errorf("failed to create document, error type: %s, reason: %s", errorObj["type"].(string), errorObj["reason"].(string))
						}
					}
				}
			}
		}
	}

	var docIDs []string
	if items, ok := result["items"].([]interface{}); ok {
		for _, item := range items {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if indexResult, ok := itemMap["index"].(map[string]interface{}); ok {
					if docID, ok := indexResult["_id"].(string); ok {
						docIDs = append(docIDs, docID)
					}
				}
			}
		}
	}

	return docIDs, nil
}

// Get Document
func (c *OpenSearchConnector) GetDocument(ctx context.Context, name string, docID string) (map[string]any, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	req := opensearchapi.GetRequest{
		Index:      name,
		DocumentID: docID,
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return nil, fmt.Errorf("failed to get document: %s", resp.String())
	}

	var result map[string]any
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	source, ok := result["_source"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("document not found")
	}

	source["_id"] = result["_id"]

	return source, nil
}

// Delete Document
func (c *OpenSearchConnector) DeleteDocument(ctx context.Context, name string, docID string) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	req := opensearchapi.DeleteRequest{
		Index:      name,
		DocumentID: docID,
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return fmt.Errorf("failed to delete document: %s", resp.String())
	}

	return nil
}

// Update Documents
func (c *OpenSearchConnector) UpsertDocuments(ctx context.Context, name string, updateRequests []map[string]any) ([]string, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	var bulkBody bytes.Buffer
	for _, updateReq := range updateRequests {
		docID, ok := updateReq["id"].(string)
		if !ok {
			continue
		}
		document := updateReq["document"]
		if document == nil {
			continue
		}

		metadata := map[string]map[string]string{
			"update": {
				"_index": name,
				"_id":    docID,
			},
		}
		if err := sonic.ConfigDefault.NewEncoder(&bulkBody).Encode(metadata); err != nil {
			return nil, err
		}

		// 写入更新操作的文档，添加upsert功能
		updateDoc := map[string]any{
			"doc":    document,
			"upsert": document, // 当文档不存在时，使用整个document作为新文档
		}
		if err := sonic.ConfigDefault.NewEncoder(&bulkBody).Encode(updateDoc); err != nil {
			return nil, err
		}
	}

	req := opensearchapi.BulkRequest{
		Body:    &bulkBody,
		Refresh: "true",
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return nil, fmt.Errorf("failed to update documents: %s", resp.String())
	}

	// 检查是否有部分文档更新失败
	var result map[string]interface{}
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var successDocIDs []string
	var errMsg string
	if items, ok := result["items"].([]interface{}); ok {
		for i, item := range items {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if updateResult, ok := itemMap["update"].(map[string]interface{}); ok {
					if status, ok := updateResult["status"].(float64); ok {
						if status < 400 {
							// 提取成功的文档ID
							if docID, ok := updateRequests[i]["id"].(string); ok {
								successDocIDs = append(successDocIDs, docID)
							}
						} else {
							// 记录错误信息
							if errMsg == "" {
								errMsg = fmt.Sprintf("error type: %s, reason: %s", updateResult["error"].(map[string]interface{})["type"].(string), updateResult["error"].(map[string]interface{})["reason"].(string))
							}
						}
					}
				}
			}
		}
	}

	if errMsg != "" {
		return successDocIDs, fmt.Errorf("%s", errMsg)
	}

	return successDocIDs, nil
}

// Delete Documents
func (c *OpenSearchConnector) DeleteDocuments(ctx context.Context, name string, docIDs string) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	docIDList := strings.Split(docIDs, ",")

	var bulkBody bytes.Buffer
	for _, docID := range docIDList {
		docID = strings.TrimSpace(docID)
		if docID == "" {
			continue
		}

		metadata := map[string]map[string]string{
			"delete": {
				"_index": name,
				"_id":    docID,
			},
		}
		if err := sonic.ConfigDefault.NewEncoder(&bulkBody).Encode(metadata); err != nil {
			return err
		}
	}

	req := opensearchapi.BulkRequest{
		Body:    &bulkBody,
		Refresh: "true",
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return fmt.Errorf("failed to delete documents: %s", resp.String())
	}

	return nil
}

// Delete Documents By Query
func (c *OpenSearchConnector) DeleteDocumentsByQuery(ctx context.Context, name string, params *interfaces.ResourceDataQueryParams, schemaDefinition []*interfaces.Property) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	query := map[string]any{
		"query": map[string]any{
			"match_all": map[string]any{},
		},
	}

	if params != nil && params.ActualFilterCond != nil {
		filterQuery, err := c.ConvertFilterCondition(params.ActualFilterCond, schemaDefinition)
		if err != nil {
			return err
		}
		if filterQuery != nil {
			query["query"] = filterQuery
		}
	}

	queryBytes, err := sonic.Marshal(query)
	if err != nil {
		return err
	}

	refresh := true
	req := opensearchapi.DeleteByQueryRequest{
		Index:   []string{name},
		Body:    bytes.NewReader(queryBytes),
		Refresh: &refresh,
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return fmt.Errorf("failed to delete documents: %s", resp.String())
	}

	return nil
}

// index exist
func (c *OpenSearchConnector) indexExist(ctx context.Context, name string) (bool, error) {
	existsReq := opensearchapi.IndicesExistsRequest{
		Index: []string{name},
	}

	existsResp, err := existsReq.Do(ctx, c.client)
	if err != nil {
		return false, err
	}
	defer func() { _ = existsResp.Body.Close() }()

	return existsResp.StatusCode == 200, nil
}
