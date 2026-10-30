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

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

const defaultBulkRequestMaxBytes = 32 * 1024 * 1024

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

// ValidateBuildSchema 当前无额外源类型限制，通用构建规则由调用方校验。
func (c *OpenSearchConnector) ValidateBuildSchema(_ *interfaces.Resource) error {
	return nil
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
		"host":           {Name: "Host", Type: "string", Description: "OpenSearch server host", Required: true, Encrypted: false},
		"port":           {Name: "Port", Type: "integer", Description: "OpenSearch server port", Required: true, Encrypted: false},
		"username":       {Name: "Username", Type: "string", Description: "Authentication username", Required: false, Encrypted: false},
		"password":       {Name: "Password", Type: "string", Description: "Authentication password", Required: false, Encrypted: true},
		"index_patterns": {Name: "Index patterns", Type: "array", Description: "Optional index matching patterns, such as log-*", Required: false, Encrypted: false},
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
func (c *OpenSearchConnector) CreateIndex(ctx context.Context, indexName string, properties map[string]any, mappingMeta map[string]string) error {
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

	mappings := map[string]any{
		"properties": properties,
	}
	if len(mappingMeta) > 0 {
		mappings["_meta"] = mappingMeta
	}

	indexSettings := map[string]any{
		"knn": true,
	}

	settings := map[string]any{
		"index": indexSettings,
	}

	config := map[string]any{
		"settings": settings,
		"mappings": mappings,
	}

	data, err := sonic.Marshal(config)
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
func (c *OpenSearchConnector) UpdateIndex(ctx context.Context, indexName string, properties map[string]any) error {
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

// CreateDocuments 仅适配原始文档和创建结果，复用统一写入流程。
func (c *OpenSearchConnector) CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error) {
	writes := make([]interfaces.DocumentWrite, len(documents))
	for i, document := range documents {
		id, _ := document["_id"].(string)
		writes[i] = interfaces.DocumentWrite{ID: id, Document: document}
	}
	results, err := c.writeDocuments(ctx, indexName, writes, true)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(results))
	for i, result := range results {
		if result.Status < 200 || result.Status >= 300 || result.ID == "" {
			return nil, fmt.Errorf("create document failed with status %d", result.Status)
		}
		ids[i] = result.ID
	}
	return ids, nil
}

func (c *OpenSearchConnector) bulkMaxBytes() int {
	if c.bulkRequestMaxBytes > 0 {
		return c.bulkRequestMaxBytes
	}
	return defaultBulkRequestMaxBytes
}

// encodeBulkDocument 将 ID 写入 metadata，并从传入文档中删除 _id。
func encodeBulkDocument(indexName, id string, document map[string]any, maxBytes int) ([]byte, error) {
	if document == nil {
		return nil, errors.New("bulk document is required")
	}

	opMeta := map[string]string{"_index": indexName}
	if id != "" {
		opMeta["_id"] = id
	}
	delete(document, "_id")
	opBytes, err := sonic.Marshal(map[string]any{"index": opMeta})
	if err != nil {
		return nil, fmt.Errorf("marshal bulk operation metadata: %w", err)
	}
	bodyBytes, err := sonic.Marshal(document)
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

// splitBulkDocumentsByBytes 返回左批最后一条文档的下标。
// 该下标属于左批，右批从下一条文档开始。优先选择字节数最接近总量一半的切点；
// 首条文档本身超过一半或最后一条文档导致右批为空时，选择能保证两批非空的切点。
// 调用方必须传入至少两条文档。
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
			if i == len(documents)-1 {
				return i - 1
			}
			leftDistance := targetBytes - currentBytes
			rightDistance := currentBytes + len(document) - targetBytes
			if leftDistance <= rightDistance {
				return i - 1
			}
			return i
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
		if resp.StatusCode == http.StatusNotFound {
			return nil, nil //nolint:nilnil // Nil result represents an expected absence condition.
		}
		return nil, fmt.Errorf("failed to get document: %s", resp.String())
	}

	var result map[string]any
	if err := common.DecodePreciseJSON(resp.Body, &result); err != nil {
		return nil, err
	}

	source, ok := result["_source"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("document not found")
	}

	source["_id"] = result["_id"]

	return source, nil
}

// GetDocuments retrieves documents through OpenSearch _mget. The result keeps
// the input ID order and represents missing documents with nil entries.
func (c *OpenSearchConnector) GetDocuments(ctx context.Context, indexName string, docIDs []string) ([]map[string]any, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	body, err := sonic.Marshal(map[string]any{"ids": docIDs})
	if err != nil {
		return nil, fmt.Errorf("marshal document IDs: %w", err)
	}
	req := opensearchapi.MgetRequest{Index: indexName, Body: bytes.NewReader(body)}
	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.IsError() {
		return nil, fmt.Errorf("failed to get documents: %s", resp.String())
	}

	var result struct {
		Documents []struct {
			ID     string         `json:"_id"`
			Found  bool           `json:"found"`
			Source map[string]any `json:"_source"`
		} `json:"docs"`
	}
	if err := common.DecodePreciseJSON(resp.Body, &result); err != nil {
		return nil, err
	}
	if len(result.Documents) != len(docIDs) {
		return nil, fmt.Errorf("multi-get returned %d documents for %d IDs", len(result.Documents), len(docIDs))
	}

	documents := make([]map[string]any, len(docIDs))
	for i, document := range result.Documents {
		if !document.Found {
			continue
		}
		if document.Source == nil {
			return nil, fmt.Errorf("multi-get document %q has no source", document.ID)
		}
		document.Source["_id"] = document.ID
		documents[i] = document.Source
	}
	return documents, nil
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
					if status, ok := common.NumberAsInt64(updateResult["status"]); ok {
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
func (c *OpenSearchConnector) DeleteDocuments(ctx context.Context, indexName string, docIDs []string) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}

	var bulkBody bytes.Buffer
	for _, docID := range docIDs {
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
	var result map[string]any
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode delete documents response: %w", err)
	}
	if hasErrors, ok := result["errors"].(bool); ok && hasErrors {
		return bulkDeleteResponseError(result)
	}

	return nil
}

func bulkDeleteResponseError(result map[string]any) error {
	items, ok := result["items"].([]any)
	if !ok {
		return errors.New("bulk delete response contains failed operations")
	}
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		deleteResult, ok := itemMap["delete"].(map[string]any)
		if !ok {
			continue
		}
		errorObject, ok := deleteResult["error"].(map[string]any)
		if !ok {
			continue
		}
		return fmt.Errorf("failed to delete document, error type: %v, reason: %v", errorObject["type"], errorObject["reason"])
	}
	return errors.New("bulk delete response contains failed operations")
}

// Delete Documents By Query
func (c *OpenSearchConnector) DeleteDocumentsByQuery(ctx context.Context, indexName string, params *interfaces.ResourceDataQueryParams, schemaDefinition []*interfaces.Property) (*interfaces.DeleteByQueryResult, error) {
	if params == nil || params.ActualFilterCond == nil {
		return nil, errors.New("delete-by-query requires a non-empty filter condition")
	}
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	filterQuery, err := c.ConvertFilterCondition(params.ActualFilterCond, schemaDefinition)
	if err != nil {
		return nil, err
	}
	if filterQuery == nil {
		return nil, errors.New("delete-by-query filter produced an empty query")
	}
	if isUnconditionalDeleteQuery(filterQuery) {
		return nil, errors.New("delete-by-query filter produced an unconditional query")
	}
	query := map[string]any{"query": filterQuery}

	queryBytes, err := sonic.Marshal(query)
	if err != nil {
		return nil, err
	}

	refresh := true
	req := opensearchapi.DeleteByQueryRequest{
		Index:   []string{indexName},
		Body:    bytes.NewReader(queryBytes),
		Refresh: &refresh,
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var response struct {
		Deleted          *int64 `json:"deleted"`
		VersionConflicts *int64 `json:"version_conflicts"`
		TimedOut         *bool  `json:"timed_out"`
		Failures         *[]struct {
			Status int `json:"status"`
		} `json:"failures"`
	}
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("invalid delete-by-query response: %w", err)
	}
	if response.Deleted == nil || response.VersionConflicts == nil || response.TimedOut == nil ||
		response.Failures == nil || *response.Deleted < 0 || *response.VersionConflicts < 0 {
		return nil, errors.New("incomplete delete-by-query response")
	}

	result := &interfaces.DeleteByQueryResult{
		Deleted:          *response.Deleted,
		VersionConflicts: *response.VersionConflicts,
		TimedOut:         *response.TimedOut,
		Failures:         make([]interfaces.DocumentWriteResult, 0, len(*response.Failures)),
	}
	for _, failure := range *response.Failures {
		if failure.Status < 400 || failure.Status > 599 {
			failure.Status = http.StatusInternalServerError
		}
		entry := interfaces.DocumentWriteResult{
			Status:    failure.Status,
			ErrorCode: interfaces.DocumentRejected,
		}
		switch {
		case failure.Status == 409:
			entry.ErrorCode = interfaces.DocumentConflict
		case failure.Status == 400 || failure.Status == 413 || failure.Status == 422:
			entry.ErrorCode = interfaces.DocumentInvalid
		case failure.Status == 408 || failure.Status == 429 || failure.Status >= 500:
			entry.ErrorCode = interfaces.DocumentUnavailable
			entry.Retryable = true
		}
		result.Failures = append(result.Failures, entry)
	}
	if resp.IsError() || result.TimedOut || result.VersionConflicts > 0 || len(result.Failures) > 0 {
		return result, interfaces.NewDeleteByQueryIncompleteError(result)
	}

	return result, nil
}

func isUnconditionalDeleteQuery(query map[string]any) bool {
	if _, ok := query["match_all"]; ok {
		return true
	}
	booleanQuery, ok := query["bool"].(map[string]any)
	if !ok || len(booleanQuery) != 1 {
		return false
	}
	must, ok := booleanQuery["must"].([]map[string]any)
	return ok && len(must) == 0
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

// IndexDocuments 完整替换文档，按输入顺序逐条返回结果。
func (c *OpenSearchConnector) IndexDocuments(ctx context.Context, indexName string,
	documents []interfaces.DocumentWrite) ([]interfaces.DocumentWriteResult, error) {

	return c.writeDocuments(ctx, indexName, documents, false)
}

// writeDocuments 共用编码、拆批及响应处理；创建允许自动 ID，并保留失败即停止语义。
func (c *OpenSearchConnector) writeDocuments(ctx context.Context, indexName string,
	documents []interfaces.DocumentWrite, create bool) ([]interfaces.DocumentWriteResult, error) {

	results := make([]interfaces.DocumentWriteResult, len(documents))
	encoded := make([][]byte, len(documents))
	validCount := 0
	for i, document := range documents {
		data, err := encodeBulkDocument(indexName, document.ID, document.Document, c.bulkMaxBytes())
		if err == nil && !create && document.ID == "" {
			err = errors.New("document id is required")
		}
		if err != nil {
			if create {
				return nil, err
			}
			results[i] = interfaces.DocumentWriteResult{ID: document.ID, Status: 400, ErrorCode: interfaces.DocumentInvalid}
			continue
		}
		encoded[i] = data
		validCount++
	}
	if validCount == 0 {
		return results, nil
	}
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	for start := 0; start < len(documents); {
		end, size := start, 0
		for end < len(documents) && size+len(encoded[end]) <= c.bulkMaxBytes() {
			size += len(encoded[end])
			end++
		}
		chunk := make([][]byte, 0, end-start)
		writes := make([]interfaces.DocumentWrite, 0, end-start)
		positions := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			if encoded[i] != nil {
				chunk = append(chunk, encoded[i])
				writes = append(writes, documents[i])
				positions = append(positions, i)
			}
		}
		if len(chunk) > 0 {
			stored := c.indexDocumentsChunk(ctx, indexName, writes, chunk, create)
			failed := false
			for i, position := range positions {
				results[position] = stored[i]
				if stored[i].Status < 200 || stored[i].Status >= 300 {
					failed = true
				}
			}
			if create && failed {
				return results, nil
			}
		}
		start = end
	}
	return results, nil
}

// indexDocumentsChunk 保留既有 413 / rejected_execution_exception 的单次拆批恢复。
func (c *OpenSearchConnector) indexDocumentsChunk(ctx context.Context, indexName string, documents []interfaces.DocumentWrite, encoded [][]byte, stopOnFailure bool) []interfaces.DocumentWriteResult {
	results, err := c.sendIndexBulkRequest(ctx, indexName, documents, encoded)
	if err == nil || len(encoded) < 2 || !shouldSplitBulkRequest(err) {
		return results
	}
	split := splitBulkDocumentsByBytes(encoded) + 1
	left, _ := c.sendIndexBulkRequest(ctx, indexName, documents[:split], encoded[:split])
	if stopOnFailure {
		for _, result := range left {
			if result.Status < 200 || result.Status >= 300 {
				return append(left, results[split:]...)
			}
		}
	}
	right, _ := c.sendIndexBulkRequest(ctx, indexName, documents[split:], encoded[split:])
	return append(left, right...)
}

// sendIndexBulkRequest 将请求未知状态和逐条响应分开处理，不公开源端错误细节。
func (c *OpenSearchConnector) sendIndexBulkRequest(ctx context.Context, indexName string, documents []interfaces.DocumentWrite, encoded [][]byte) (results []interfaces.DocumentWriteResult, err error) {
	// 在发送边界记录每次请求的失败，包含拆批后调用方不再上抛的错误。
	defer func() {
		if err != nil {
			logger.Errorf("OpenSearch bulk request failed: index=%q, documents=%d, error=%v", indexName, len(documents), err)
		}
	}()
	results = make([]interfaces.DocumentWriteResult, len(documents))
	var body bytes.Buffer
	for i, document := range documents {
		body.Write(encoded[i])
		results[i] = interfaces.DocumentWriteResult{ID: document.ID, Status: 503, ErrorCode: interfaces.DocumentOutcomeUnknown, Retryable: true}
	}
	request := opensearchapi.BulkRequest{Body: &body, Refresh: "true"}
	response, err := request.Do(ctx, c.client)
	if err != nil {
		return results, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.IsError() {
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			for i, document := range documents {
				results[i] = interfaces.DocumentWriteResult{ID: document.ID, Status: response.StatusCode, ErrorCode: interfaces.DocumentRejected}
			}
		}
		if response.StatusCode == 400 || response.StatusCode == 413 || response.StatusCode == 429 {
			for i, document := range documents {
				results[i] = interfaces.DocumentWriteResult{ID: document.ID, Status: response.StatusCode, ErrorCode: interfaces.DocumentInvalid}
				if response.StatusCode == 429 {
					results[i].ErrorCode = interfaces.DocumentUnavailable
					results[i].Retryable = true
				}
			}
		}
		return results, &bulkRequestError{statusCode: response.StatusCode, detail: response.String()}
	}
	var decoded struct {
		Items []struct {
			Index *struct {
				ID     string `json:"_id"`
				Status int    `json:"status"`
				Error  *struct {
					Type   string `json:"type"`
					Reason string `json:"reason"`
				} `json:"error"`
			} `json:"index"`
		} `json:"items"`
	}
	if err := sonic.ConfigDefault.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return results, err
	}
	if len(decoded.Items) != len(documents) {
		return results, errors.New("bulk response item count does not match request")
	}
	for i, item := range decoded.Items {
		if item.Index == nil || item.Index.ID == "" || (documents[i].ID != "" && item.Index.ID != documents[i].ID) || item.Index.Status < 200 || item.Index.Status > 599 {
			logger.Errorf("OpenSearch bulk response item is invalid: index=%q, id=%q, position=%d", indexName, documents[i].ID, i)
			continue
		}
		entry := interfaces.DocumentWriteResult{ID: item.Index.ID, Status: item.Index.Status}
		switch {
		case entry.Status >= 200 && entry.Status < 300:
		case entry.Status == 409:
			entry.ErrorCode = interfaces.DocumentConflict
		case entry.Status == 400 || entry.Status == 413 || entry.Status == 422:
			entry.ErrorCode = interfaces.DocumentInvalid
		case entry.Status == 408 || entry.Status == 429 || entry.Status >= 500:
			entry.ErrorCode = interfaces.DocumentUnavailable
			entry.Retryable = true
		default:
			entry.ErrorCode = interfaces.DocumentRejected
		}
		if entry.Status >= 300 {
			errorType, reason := "", ""
			if item.Index.Error != nil {
				errorType, reason = item.Index.Error.Type, item.Index.Error.Reason
			}
			logger.Errorf("OpenSearch bulk document failed: index=%q, id=%q, status=%d, type=%q, reason=%q", indexName, entry.ID, entry.Status, errorType, reason)
		}
		results[i] = entry
	}
	return results, nil
}
