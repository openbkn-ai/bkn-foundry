// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"fmt"
)

type AnalyzerCapability struct {
	ID string `json:"id"`
}
type IndexCapabilities struct {
	FulltextAnalyzers []AnalyzerCapability `json:"fulltext_analyzers"`
	CheckedAt         int64                `json:"checked_at"`
}
type IndexCapabilitiesUnavailableError struct{ Cause error }

func (e *IndexCapabilitiesUnavailableError) Error() string {
	return fmt.Sprintf("index capabilities unavailable: %v", e.Cause)
}
func (e *IndexCapabilitiesUnavailableError) Unwrap() error { return e.Cause }

// LocalIndexManager manages local index storage backed by the local search engine.
//
//go:generate mockgen -source ../interfaces/local_index_manager.go -destination ../interfaces/mock/mock_local_index_manager.go
type LocalIndexManager interface {
	CreateIndex(ctx context.Context, indexName string, schema []*Property) error
	UpdateIndex(ctx context.Context, indexName string, schema []*Property) error
	DeleteIndex(ctx context.Context, indexName string) error
	CheckIndexExist(ctx context.Context, indexName string) (bool, error)
	ValidateAnalyzer(ctx context.Context, analyzer string) (bool, error)
	GetIndexCapabilities(ctx context.Context) (*IndexCapabilities, error)

	ListDocuments(ctx context.Context, indexName string, res *Resource, params *ResourceDataQueryParams) ([]map[string]any, int64, error)
	GetDocument(ctx context.Context, indexName string, docID string) (map[string]any, error)
	CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error)
	IndexDocuments(ctx context.Context, indexName string, documents map[string]map[string]any) ([]string, error)
	UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error)
	DeleteDocument(ctx context.Context, indexName string, docID string) error
	DeleteDocuments(ctx context.Context, indexName string, docIDs string) error
	DeleteDocumentsByQuery(ctx context.Context, indexName string, res *Resource, params *ResourceDataQueryParams) error
}
