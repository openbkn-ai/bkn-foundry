// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

type Hit struct {
	Source map[string]any `json:"_source"`
	Sort   []any          `json:"sort"`
	Score  float64        `json:"_score"`
}

type IndexStats struct {
	DocCount    int64 `json:"doc_count"`
	StorageSize int64 `json:"storage_size"`
}

// OpenSearchAccess defines the OpenSearch access interface.
//
//go:generate mockgen -source ../interfaces/opensearch_access.go -destination ../interfaces/mock/mock_opensearch_access.go
type OpenSearchAccess interface {
	PutIndexTemplate(ctx context.Context, indexTemplateName string, body any) error

	CreateIndex(ctx context.Context, indexName string, body any) error

	// IndexExists checks whether the specified index exists.
	IndexExists(ctx context.Context, indexName string) (bool, error)

	// GetIndexStats gets index statistics.
	GetIndexStats(ctx context.Context, indexName string) (*IndexStats, error)

	// Refresh refreshes the specified index so all operations take effect immediately.
	Refresh(ctx context.Context, indexName string) error

	// DeleteIndex deletes an index by name.
	DeleteIndex(ctx context.Context, indexName string) error

	// InsertData writes data to an index with the specified document ID.
	InsertData(ctx context.Context, indexName string, docID string, data any) error

	// BulkInsertData writes data to an index in batches.
	BulkInsertData(ctx context.Context, indexName string, dataList []any) error

	// SearchData searches data in the specified index.
	SearchData(ctx context.Context, indexName string, query any) ([]Hit, error)

	// DeleteData deletes data by index name and document ID.
	DeleteData(ctx context.Context, indexName string, docID string) error

	// DeleteByQuery deletes data that matches a query.
	DeleteByQuery(ctx context.Context, indexName string, query any) error

	// BulkDeleteData deletes data in batches using document IDs.
	BulkDeleteData(ctx context.Context, indexName string, docIDs []string) error

	Count(ctx context.Context, indexName string, query any) ([]byte, error)
}
