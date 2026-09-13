// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"errors"
)

// ErrDocumentNotFound is returned by UpdateData when the target document does not exist.
var ErrDocumentNotFound = errors.New("document not found")

// Update results reported by OpenSearch for a single-document update.
const (
	UpdateResultUpdated = "updated"
	UpdateResultNoop    = "noop"
)

type Hit struct {
	Source map[string]interface{} `json:"_source"`
	Sort   []any                  `json:"sort"`
	Score  float64                `json:"_score"`
}

//go:generate mockgen -source ../interfaces/opensearch_access.go -destination ../interfaces/mock/mock_opensearch_access.go

// OpenSearchAccess defines the OpenSearch access interface.
type OpenSearchAccess interface {
	CreateIndex(ctx context.Context, indexName string, body any) error

	// IndexExists checks whether the specified index exists.
	IndexExists(ctx context.Context, indexName string) (bool, error)

	// DeleteIndex deletes an index by name.
	DeleteIndex(ctx context.Context, indexName string) error

	// InsertData writes data to an index with the specified document ID.
	InsertData(ctx context.Context, indexName string, docID string, data any) error

	// UpdateData applies a partial update to one existing document. body is an OpenSearch
	// _update body, either {"doc": {...}} or {"script": {...}}; the update is applied to
	// the latest version of the document without the caller reading it first. It returns
	// the result OpenSearch reports (UpdateResultUpdated, UpdateResultNoop, ...) and
	// ErrDocumentNotFound when the document does not exist.
	UpdateData(ctx context.Context, indexName string, docID string, body any) (string, error)

	// BulkInsertData writes data to an index in batches.
	BulkInsertData(ctx context.Context, indexName string, dataList []any) error

	// SearchData searches data in the specified index.
	SearchData(ctx context.Context, indexName string, query any) ([]Hit, error)

	// DeleteData deletes data by index name and document ID.
	DeleteData(ctx context.Context, indexName string, docID string) error

	// BulkDeleteData deletes data in batches using document IDs.
	BulkDeleteData(ctx context.Context, indexName string, docIDs []string) error

	Count(ctx context.Context, indexName string, query any) ([]byte, error)
}
