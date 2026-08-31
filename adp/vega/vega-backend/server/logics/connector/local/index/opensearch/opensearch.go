// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package opensearch provides OpenSearch/ElasticSearch connector implementation.
package opensearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"

	"vega-backend/interfaces"
)

const (
	defaultBulkRequestMaxBytes = 32 * 1024 * 1024
)

type bulkRequestError struct {
	statusCode int
	detail     string
}

func (e *bulkRequestError) Error() string {
	return fmt.Sprintf("OpenSearch bulk request returned HTTP %d: %s", e.statusCode, e.detail)
}

type opensearchConfig struct {
	Host          string   `mapstructure:"host"`
	Port          int      `mapstructure:"port"`
	Username      string   `mapstructure:"username"`
	Password      string   `mapstructure:"password"`
	IndexPatterns []string `mapstructure:"index_patterns"`
}

// OpenSearchConnector implements IndexConnector for OpenSearch/ElasticSearch.
type OpenSearchConnector struct {
	enabled bool
	Config  *opensearchConfig
	client  *opensearch.Client

	bulkRequestMaxBytes int
}

// ValidateAnalyzer verifies that a configured analyzer is available in the connected OpenSearch cluster.
func (c *OpenSearchConnector) ValidateAnalyzer(ctx context.Context, analyzer string) (bool, error) {
	if err := c.Connect(ctx); err != nil {
		return false, err
	}
	analyzer = strings.TrimSpace(analyzer)
	if analyzer == "" {
		return true, nil
	}
	body, err := sonic.Marshal(map[string]any{"analyzer": analyzer, "text": "bkn"})
	if err != nil {
		return false, fmt.Errorf("marshal analyzer validation request: %w", err)
	}
	resp, err := c.client.Indices.Analyze(
		c.client.Indices.Analyze.WithContext(ctx),
		c.client.Indices.Analyze.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return false, fmt.Errorf("validate analyzer %q: %w", analyzer, err)
	}
	if resp.IsError() {
		detail := resp.String()
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusBadRequest && isAnalyzerNotFound(detail) {
			return false, nil
		}
		return false, fmt.Errorf("validate analyzer %q: OpenSearch returned %s: %s", analyzer, resp.Status(), detail)
	}
	_ = resp.Body.Close()
	return true, nil
}

func isAnalyzerNotFound(detail string) bool {
	message := strings.ToLower(detail)
	return strings.Contains(message, "failed to find global analyzer") ||
		strings.Contains(message, "analyzer not found") ||
		strings.Contains(message, "unknown analyzer")
}

// NewOpenSearchConnector creates the OpenSearch connector builder
func NewOpenSearchConnector() interfaces.IndexConnector {
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
		"host":           {Name: "主机地址", Type: "string", Description: "OpenSearch 服务器主机地址", Required: true, Encrypted: false},
		"port":           {Name: "端口号", Type: "integer", Description: "OpenSearch 服务器端口", Required: true, Encrypted: false},
		"username":       {Name: "用户名", Type: "string", Description: "认证用户名", Required: false, Encrypted: false},
		"password":       {Name: "密码", Type: "string", Description: "认证密码", Required: false, Encrypted: true},
		"index_patterns": {Name: "索引模式", Type: "array", Description: "索引匹配模式列表（可选，如 log-*）", Required: false, Encrypted: false},
	}
}

// New creates a new OpenSearch connector.
func (c *OpenSearchConnector) New(cfg interfaces.ConnectorConfig) (interfaces.Connector, error) {
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
func (c *OpenSearchConnector) CreateIndex(ctx context.Context, indexName string, schemaDefinition []*interfaces.Property) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	exist, err := c.indexExist(ctx, indexName)
	if err != nil {
		return err
	}
	// index exist
	if exist {
		return fmt.Errorf("index %s already exist", indexName)
	}

	// Construct field mapping
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

	// If there is a vector field, enable knn
	if hasVectorField {
		indexSettings := mapping["settings"].(map[string]any)["index"].(map[string]any)
		indexSettings["knn"] = true
	}

	data, err := sonic.Marshal(mapping)
	if err != nil {
		return err
	}
	createReq := opensearchapi.IndicesCreateRequest{
		Index: indexName,
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
func (c *OpenSearchConnector) UpdateIndex(ctx context.Context, indexName string, schemaDefinition []*interfaces.Property) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	exist, err := c.indexExist(ctx, indexName)
	if err != nil {
		return err
	}
	// index not exist
	if !exist {
		return fmt.Errorf("index %s not exist", indexName)
	}

	// Construct field mapping
	properties, _, err := c.buildFieldMappings(schemaDefinition)
	if err != nil {
		return err
	}

	// Build the properties mapping
	mappings := map[string]any{
		"properties": properties,
	}

	// Build a JSON string
	data, err := sonic.Marshal(mappings)
	if err != nil {
		return err
	}
	updateReq := opensearchapi.IndicesPutMappingRequest{
		Index: []string{indexName},
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
func (c *OpenSearchConnector) DeleteIndex(ctx context.Context, indexName string) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	exist, err := c.CheckIndexExist(ctx, indexName)
	if err != nil {
		return err
	}
	// index not exist
	if !exist {
		return nil
	}

	deleteReq := opensearchapi.IndicesDeleteRequest{
		Index: []string{indexName},
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
func (c *OpenSearchConnector) CheckIndexExist(ctx context.Context, indexName string) (bool, error) {
	if err := c.Connect(ctx); err != nil {
		return false, err
	}

	return c.indexExist(ctx, indexName)
}

// Create Documents
func (c *OpenSearchConnector) CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	encodedDocuments := make([][]byte, 0, len(documents))
	for _, document := range documents {
		encoded, err := encodeBulkDocument(indexName, document, c.bulkMaxBytes())
		if err != nil {
			return nil, err
		}
		encodedDocuments = append(encodedDocuments, encoded)
	}
	return c.indexBulkDocuments(ctx, encodedDocuments)
}

func (c *OpenSearchConnector) bulkMaxBytes() int {
	if c.bulkRequestMaxBytes > 0 {
		return c.bulkRequestMaxBytes
	}
	return defaultBulkRequestMaxBytes
}

func encodeBulkDocument(indexName string, document map[string]any, maxBytes int) ([]byte, error) {
	if document == nil {
		return nil, errors.New("bulk document is required")
	}
	opMeta := map[string]map[string]string{"index": {"_index": indexName}}
	body := make(map[string]any, len(document))
	for key, value := range document {
		if key == "_id" {
			if documentID, ok := value.(string); ok {
				opMeta["index"]["_id"] = documentID
				continue
			}
		}
		body[key] = value
	}
	opBytes, err := sonic.Marshal(opMeta)
	if err != nil {
		return nil, fmt.Errorf("marshal bulk operation metadata: %w", err)
	}
	bodyBytes, err := sonic.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal bulk document: %w", err)
	}
	encoded := make([]byte, 0, len(opBytes)+len(bodyBytes)+2)
	encoded = append(encoded, opBytes...)
	encoded = append(encoded, '\n')
	encoded = append(encoded, bodyBytes...)
	encoded = append(encoded, '\n')
	if len(encoded) > maxBytes {
		return nil, fmt.Errorf("bulk document is %d bytes, exceeding the %d byte request limit", len(encoded), maxBytes)
	}
	return encoded, nil
}

func (c *OpenSearchConnector) indexBulkDocuments(ctx context.Context, documents [][]byte) ([]string, error) {
	var documentIDs []string
	maxBytes := c.bulkMaxBytes()
	chunkDocuments := make([][]byte, 0)
	chunkBytes := 0
	for _, document := range documents {
		if len(chunkDocuments) > 0 && chunkBytes+len(document) > maxBytes {
			ids, err := c.indexBulkChunk(ctx, chunkDocuments)
			if err != nil {
				return nil, err
			}
			documentIDs = append(documentIDs, ids...)
			chunkDocuments = chunkDocuments[:0]
			chunkBytes = 0
		}
		chunkDocuments = append(chunkDocuments, document)
		chunkBytes += len(document)
	}
	if len(chunkDocuments) > 0 {
		ids, err := c.indexBulkChunk(ctx, chunkDocuments)
		if err != nil {
			return nil, err
		}
		documentIDs = append(documentIDs, ids...)
	}
	return documentIDs, nil
}

func (c *OpenSearchConnector) indexBulkChunk(ctx context.Context, documents [][]byte) ([]string, error) {
	ids, err := c.sendBulkRequest(ctx, documents)
	if err == nil {
		return ids, nil
	}
	if !shouldSplitBulkRequest(err) || len(documents) == 1 {
		return nil, err
	}

	splitAt := splitBulkDocumentsByBytes(documents)
	leftDocuments := documents[:splitAt+1]
	rightDocuments := documents[splitAt+1:]
	logger.Warnf("OpenSearch bulk request rejected; splitting %d documents into %d and %d documents by serialized bytes: %v",
		len(documents), len(leftDocuments), len(rightDocuments), err)
	leftIDs, err := c.sendBulkRequest(ctx, leftDocuments)
	if err != nil {
		return nil, err
	}
	rightIDs, err := c.sendBulkRequest(ctx, rightDocuments)
	if err != nil {
		return nil, err
	}
	return append(leftIDs, rightIDs...), nil
}

// splitBulkDocumentsByBytes 返回左批最后一条文档的下标。
// 该下标属于左批，右批从下一条文档开始。优先选择字节数不超过总量一半的最大左批；
// 首条文档本身超过一半时返回 0，以保证两批都非空。调用方必须传入至少两条文档。
func splitBulkDocumentsByBytes(documents [][]byte) int {
	totalBytes := 0
	for _, document := range documents {
		totalBytes += len(document)
	}
	targetBytes := totalBytes / 2
	currentBytes := 0
	for i, document := range documents {
		if currentBytes+len(document) > targetBytes {
			if i == 0 {
				return 0
			}
			return i - 1
		}
		currentBytes += len(document)
	}
	return len(documents) - 2
}

func shouldSplitBulkRequest(err error) bool {
	var requestErr *bulkRequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	if requestErr.statusCode == http.StatusRequestEntityTooLarge {
		return true
	}
	return requestErr.statusCode == http.StatusTooManyRequests &&
		strings.Contains(strings.ToLower(requestErr.detail), "rejected_execution_exception")
}

func (c *OpenSearchConnector) sendBulkRequest(ctx context.Context, documents [][]byte) ([]string, error) {
	size := 0
	for _, document := range documents {
		size += len(document)
	}
	body := make([]byte, 0, size)
	for _, document := range documents {
		body = append(body, document...)
	}
	req := opensearchapi.BulkRequest{
		Body:    bytes.NewReader(body),
		Refresh: "true",
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return nil, &bulkRequestError{statusCode: resp.StatusCode, detail: resp.String()}
	}

	var result map[string]any
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if hasErrors, ok := result["errors"].(bool); ok && hasErrors {
		return nil, bulkResponseError(result)
	}
	var documentIDs []string
	if items, ok := result["items"].([]any); ok {
		for _, item := range items {
			if itemMap, ok := item.(map[string]any); ok {
				if indexResult, ok := itemMap["index"].(map[string]any); ok {
					if documentID, ok := indexResult["_id"].(string); ok {
						documentIDs = append(documentIDs, documentID)
					}
				}
			}
		}
	}
	return documentIDs, nil
}

func bulkResponseError(result map[string]any) error {
	items, ok := result["items"].([]any)
	if !ok {
		return errors.New("bulk response contains failed operations")
	}
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		indexResult, ok := itemMap["index"].(map[string]any)
		if !ok {
			continue
		}
		errorObject, ok := indexResult["error"].(map[string]any)
		if !ok {
			continue
		}
		return fmt.Errorf("failed to create document, error type: %v, reason: %v", errorObject["type"], errorObject["reason"])
	}
	return errors.New("bulk response contains failed operations")
}

// IndexDocuments replaces complete documents when their IDs already exist and
// creates them otherwise. It uses the OpenSearch bulk index action.
func (c *OpenSearchConnector) IndexDocuments(ctx context.Context, indexName string, documents map[string]map[string]any) ([]string, error) {
	indexDocuments := make([]map[string]any, 0, len(documents))
	for documentID, document := range documents {
		if documentID == "" {
			return nil, fmt.Errorf("index document: id is required")
		}
		if document == nil {
			return nil, fmt.Errorf("index document %q: document is required", documentID)
		}
		copyDocument := make(map[string]any, len(document)+1)
		for key, value := range document {
			copyDocument[key] = value
		}
		copyDocument["_id"] = documentID
		indexDocuments = append(indexDocuments, copyDocument)
	}
	return c.CreateDocuments(ctx, indexName, indexDocuments)
}

// Get Document
func (c *OpenSearchConnector) GetDocument(ctx context.Context, indexName string, docID string) (map[string]any, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	req := opensearchapi.GetRequest{
		Index:      indexName,
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
func (c *OpenSearchConnector) DeleteDocument(ctx context.Context, indexName string, docID string) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	req := opensearchapi.DeleteRequest{
		Index:      indexName,
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
func (c *OpenSearchConnector) UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error) {
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
				"_index": indexName,
				"_id":    docID,
			},
		}
		if err := sonic.ConfigDefault.NewEncoder(&bulkBody).Encode(metadata); err != nil {
			return nil, err
		}

		// Write the document for the update operation and add the upsert function
		updateDoc := map[string]any{
			"doc":    document,
			"upsert": document, // When the document does not exist, use the entire Document as the new document
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

	// Check if any documents have failed to update
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
							// The successfully extracted document ID
							if docID, ok := updateRequests[i]["id"].(string); ok {
								successDocIDs = append(successDocIDs, docID)
							}
						} else {
							// Record error messages
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
func (c *OpenSearchConnector) DeleteDocuments(ctx context.Context, indexName string, docIDs string) error {
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
				"_index": indexName,
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
func (c *OpenSearchConnector) DeleteDocumentsByQuery(ctx context.Context, indexName string, params *interfaces.ResourceDataQueryParams, schemaDefinition []*interfaces.Property) error {
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
		Index:   []string{indexName},
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
func (c *OpenSearchConnector) indexExist(ctx context.Context, indexName string) (bool, error) {
	existsReq := opensearchapi.IndicesExistsRequest{
		Index: []string{indexName},
	}

	existsResp, err := existsReq.Do(ctx, c.client)
	if err != nil {
		return false, err
	}
	defer func() { _ = existsResp.Body.Close() }()

	return existsResp.StatusCode == 200, nil
}
