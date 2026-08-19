// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	attr "go.opentelemetry.io/otel/attribute"

	"ontology-query/common"
	"ontology-query/interfaces"
)

var (
	osAccessOnce sync.Once
	osAccess     interfaces.OpenSearchAccess
	//osAddress    string
)

type openSearchAccess struct {
	appSetting *common.AppSetting
	client     *opensearch.Client
}

func NewOpenSearchAccess(appSetting *common.AppSetting) interfaces.OpenSearchAccess {
	osAccessOnce.Do(func() {
		osAccess = &openSearchAccess{
			appSetting: appSetting,
			client:     rest.NewOpenSearchClient(appSetting.OpenSearchSetting),
		}
	})

	return osAccess
}

// CreateIndex creates an index with the specified name and configuration.
// It creates an OpenSearch index from the provided index name and body configuration.
// Parameters:
//   - ctx: Context that controls the request lifecycle.
//   - indexName: Name of the index to create.
//   - body: Index configuration, including settings and mappings.
//
// Returns nil on success or the underlying error on failure.
func (o *openSearchAccess) CreateIndex(ctx context.Context, indexName string, body any) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateIndex")
	defer span.End()

	span.SetAttributes(attr.Key("index_name").String(indexName))

	// Encode body as JSON bytes.
	bodyBytes, err := sonic.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal index body: %w", err)
	}

	// Create the index request.
	req := opensearchapi.IndicesCreateRequest{
		Index: indexName,
		Body:  bytes.NewBuffer(bodyBytes),
	}

	// Execute the index request.
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return fmt.Errorf("failed to create index %s: %w", indexName, err)
	}
	defer func() { _ = res.Body.Close() }()

	// Check the response status.
	if res.IsError() {
		return fmt.Errorf("create index %s failed: %s, %s", indexName, res.Status(), res.String())
	}

	return nil
}

// IndexExists checks whether the specified index exists.
// It checks whether the index already exists in OpenSearch.
// Parameters:
//   - ctx: Context that controls the request lifecycle.
//   - indexName: Name of the index to check.
//
// Returns true when the index exists, false when it does not, or false with an error on failure.
// Example:
//
//	exists, err := client.IndexExists(ctx, "my-index")
//	if err != nil {
//	    // Handle the error.
//	}
//	if exists {
//	    // The index exists; creation can be skipped.
//	} else {
//	    // The index does not exist and must be created.
//	}
func (o *openSearchAccess) IndexExists(ctx context.Context, indexName string) (bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "IndexExists")
	defer span.End()

	span.SetAttributes(attr.Key("index_name").String(indexName))

	// Create the index-existence request.
	req := opensearchapi.IndicesExistsRequest{
		Index: []string{indexName},
	}

	// Execute the request.
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return false, fmt.Errorf("failed to check index existence: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	// Determine index existence from the response status code.
	// 200 - index exists.
	// 404 - index does not exist.
	// Other status codes - error.
	switch res.StatusCode {
	case 200:
		return true, nil
	case 404:
		return false, nil
	default:
		return false, fmt.Errorf("check index existence failed: %s, %s", res.Status(), res.String())
	}
}

func (o *openSearchAccess) DeleteIndex(ctx context.Context, indexName string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteIndex")
	defer span.End()

	span.SetAttributes(attr.Key("index_name").String(indexName))

	// Create the delete-index request.
	req := opensearchapi.IndicesDeleteRequest{
		Index: []string{indexName},
	}

	// Execute the delete-index request.
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return fmt.Errorf("failed to delete index %s: %w", indexName, err)
	}
	defer func() { _ = res.Body.Close() }()

	// Check the response status.
	if res.IsError() {
		return fmt.Errorf("delete index %s failed: %s, %s", indexName, res.Status(), res.String())
	}

	return nil
}

// InsertData writes data to an index with the specified document ID.
// It inserts one document into the specified OpenSearch index.
// Parameters:
//   - ctx: Context that controls the request lifecycle.
//   - indexName: Target index name.
//   - id: Unique document identifier.
//   - data: Document data to insert; any serializable struct or map.
//
// Returns nil on success or the underlying error on failure.
// The index is refreshed after insertion so data is immediately searchable.
func (o *openSearchAccess) InsertData(ctx context.Context, indexName string, docID string, data any) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "InsertData")
	defer span.End()

	span.SetAttributes(
		attr.Key("index_name").String(indexName),
		attr.Key("doc_id").String(docID))

	// Encode data as JSON.
	jsonData, err := sonic.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// Create the index request with the document ID.
	req := opensearchapi.IndexRequest{
		Index:      indexName,
		DocumentID: docID,
		Body:       bytes.NewReader(jsonData),
		Refresh:    "true", // Refresh immediately so data is searchable.
	}

	// Execute the request.
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return fmt.Errorf("failed to insert data with ID: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return fmt.Errorf("insert data with ID failed: %s, %s", res.Status(), res.String())
	}

	return nil
}

// BulkInsertData writes data to an index in batches.
// It efficiently inserts multiple documents into the specified OpenSearch index.
// The bulk API significantly improves large-volume insertion throughput over individual inserts.
// Parameters:
//   - ctx: Context that controls the request lifecycle.
//   - indexName: Target index name.
//   - dataList: Document data list; every item must contain an "id" field as its document ID.
//
// Returns nil on success or the underlying error on failure.
// The index is refreshed after insertion so data is immediately searchable.
// Performance: keep each bulk insertion within a reasonable document count, such as 1000-5000.
// Example:
//
//	dataList := []any{
//	map[string]interface{}{"id": "doc1", "title": "Document 1"},
//	map[string]interface{}{"id": "doc2", "title": "Document 2"},
//	}
func (o *openSearchAccess) BulkInsertData(ctx context.Context, indexName string, dataList []any) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "BulkInsertData")
	defer span.End()

	span.SetAttributes(attr.Key("index_name").String(indexName))

	if len(dataList) == 0 {
		return nil
	}

	var buf bytes.Buffer

	for _, data := range dataList {
		// Prepare metadata.
		meta := map[string]any{
			"index": map[string]any{
				"_index": indexName,
				"_id":    data.(map[string]any)["__id"],
			},
		}

		// Write the metadata line.
		metaJSON, err := sonic.Marshal(meta)
		if err != nil {
			return fmt.Errorf("failed to marshal bulk metadata: %w", err)
		}
		buf.Write(metaJSON)
		buf.WriteByte('\n')

		// Write the data line.
		dataJSON, err := sonic.Marshal(data)
		if err != nil {
			return fmt.Errorf("failed to marshal bulk data: %w", err)
		}
		buf.Write(dataJSON)
		buf.WriteByte('\n')
	}

	// Create the bulk request.
	req := opensearchapi.BulkRequest{
		Body:    &buf,
		Refresh: "true",
	}

	// Execute the request.
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return fmt.Errorf("failed to bulk insert data: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return fmt.Errorf("bulk insert data failed: %s, %s", res.Status(), res.String())
	}

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var resp struct {
		Took   int             `json:"took"`
		Errors bool            `json:"errors"`
		Items  json.RawMessage `json:"items"`
	}

	if err := sonic.Unmarshal(resBody, &resp); err != nil {
		return fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	if resp.Errors {
		return fmt.Errorf("bulk insert data failed: %s", resp.Items)
	}

	return nil
}

// SearchData searches data in the specified index.
// It searches the specified index using the provided query.
// It supports complex query DSL, including full-text search, filters, and aggregations.
// Parameters:
//   - ctx: Context that controls the request lifecycle.
//   - indexName: Name of the index to search.
//   - query: Query condition in any valid OpenSearch query-DSL structure.
//
// Returns complete documents in a result list or an error on failure.
// Example:
//
//	query := map[string]interface{}{
//	  "query": map[string]interface{}{
//	"match": map[string]interface{}{"title": "search keyword"},
//	  },
//	}
func (o *openSearchAccess) SearchData(ctx context.Context, indexName string, query any) ([]interfaces.Hit, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "SearchData")
	defer span.End()

	span.SetAttributes(attr.Key("index_name").String(indexName))

	// Encode the query as JSON.
	queryJSON, err := sonic.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}
	logger.Debug(string(queryJSON))

	// Create the search request.
	req := opensearchapi.SearchRequest{
		Index: []string{indexName},
		Body:  bytes.NewReader(queryJSON),
	}

	// Execute the request.
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return nil, fmt.Errorf("failed to search data: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return nil, fmt.Errorf("search data failed: %s, %s", res.Status(), res.String())
	}

	// Parse the response.
	var searchResult struct {
		Hits struct {
			Hits []struct {
				Source map[string]interface{} `json:"_source"`
				Sort   []any                  `json:"sort"`
				Score  float64                `json:"_score"`
			} `json:"hits"`
		} `json:"hits"`
	}

	cfg := sonic.Config{UseInt64: true}.Froze()
	if err := cfg.NewDecoder(res.Body).Decode(&searchResult); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	// Extract search results.
	results := make([]interfaces.Hit, 0, len(searchResult.Hits.Hits))
	for _, hit := range searchResult.Hits.Hits {
		results = append(results, hit)
	}

	return results, nil
}

// DeleteData deletes one document from the specified index.
// It deletes one document from the specified index by document ID.
// A missing document (404) is not treated as an error.
// Parameters:
//   - ctx: Context that controls the request lifecycle.
//   - indexName: Target index name.
//   - id: Document ID to delete.
//
// Returns nil on success or the underlying error on failure.
// The index is refreshed after deletion so the result is immediately visible.
func (o *openSearchAccess) DeleteData(ctx context.Context, indexName string, docID string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "DeleteData")
	defer span.End()

	span.SetAttributes(
		attr.Key("index_name").String(indexName),
		attr.Key("doc_id").String(docID))

	req := opensearchapi.DeleteRequest{
		Index:      indexName,
		DocumentID: docID,
		Refresh:    "true", // Refresh immediately so deletions are visible.
	}

	res, err := req.Do(ctx, o.client)
	if err != nil {
		return fmt.Errorf("failed to delete data %s from index %s: %w", docID, indexName, err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		// A 404 means the document does not exist and is not treated as an error.
		if res.StatusCode == 404 {
			return nil
		}
		return fmt.Errorf("delete data %s from index %s failed: %s, %s", docID, indexName, res.Status(), res.String())
	}

	return nil
}

// BulkDeleteData deletes data from an index in batches.
// It efficiently deletes multiple documents from the specified index.
// The bulk API significantly improves large-volume deletion throughput.
// Parameters:
//   - ctx: Context that controls the request lifecycle.
//   - indexName: Target index name.
//   - idList: List of document IDs to delete.
//
// Returns nil on success or the underlying error on failure.
// The index is refreshed after deletion so the result is immediately visible.
// Performance: keep each bulk deletion within a reasonable document count, such as 1000-5000.
// Fault tolerance: a missing document for one ID does not affect deletion of other documents.
func (o *openSearchAccess) BulkDeleteData(ctx context.Context, indexName string, docIDs []string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "BulkDeleteData")
	defer span.End()

	span.SetAttributes(attr.Key("index_name").String(indexName))

	if len(docIDs) == 0 {
		return nil // Return immediately for an empty list to avoid an unnecessary network request.
	}

	var buf bytes.Buffer

	// Build the bulk-delete request, with delete metadata on each line.
	for _, docID := range docIDs {
		// Create delete-operation metadata.
		action := map[string]interface{}{
			"delete": map[string]interface{}{
				"_index": indexName,
				"_id":    docID,
			},
		}

		// Write the operation metadata line.
		actionBytes, err := sonic.Marshal(action)
		if err != nil {
			return fmt.Errorf("failed to marshal delete action: %w", err)
		}
		buf.Write(actionBytes)
		buf.WriteByte('\n')
	}

	// Create the bulk request and refresh immediately so deletions are visible.
	req := opensearchapi.BulkRequest{
		Body:    &buf,
		Refresh: "true", // Refresh immediately so deletions are visible.
	}

	// Execute the bulk-delete request.
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return fmt.Errorf("failed to bulk delete data: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	// Check the response status.
	if res.IsError() {
		return fmt.Errorf("bulk delete data failed: %s, %s", res.Status(), res.String())
	}

	return nil
}

func (o *openSearchAccess) Count(ctx context.Context, indexName string, query any) ([]byte, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Count")
	defer span.End()

	span.SetAttributes(attr.Key("index_name").String(indexName))

	// Encode the query as JSON.
	queryJSON, err := sonic.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Create the search request.
	ignoreUnavailable := true
	req := opensearchapi.CountRequest{
		Index:             []string{indexName},
		Body:              bytes.NewReader(queryJSON),
		IgnoreUnavailable: &ignoreUnavailable,
	}

	// Execute the request.
	res, err := req.Do(ctx, o.client)
	if err != nil {
		return nil, fmt.Errorf("failed to Count: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return nil, fmt.Errorf("Count failed: %s, %s", res.Status(), res.String())
	}

	// Parse the response.
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			return
		}
	}(res.Body)

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	return resBytes, nil
}
