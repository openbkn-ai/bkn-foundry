// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

type AnalyzerCapability struct {
	ID string `json:"id"`
}
type IndexCapabilities struct {
	FulltextAnalyzers []AnalyzerCapability `json:"fulltext_analyzers"`
	CheckedAt         int64                `json:"checked_at"`
}

// LocalIndexManager manages local index storage backed by the local search engine.
//
//go:generate mockgen -source ../interfaces/local_index_manager.go -destination ../interfaces/mock/mock_local_index_manager.go
type LocalIndexManager interface {
	// SchemaForQuery 投影主字段及独立物理子字段、生成字段，由连接器分别判断能力；构造失败返回错误。
	SchemaForQuery(schema []*Property) ([]*Property, error)
	// GeneratedFields 返回托管索引生成字段，使用完整 mapping 补齐过滤能力；构造失败返回错误。
	GeneratedFields(schema []*Property) (map[string]*Property, error)

	// ValidateFilterability 按最终主字段映射校验显式开启，不查询模型或索引。
	ValidateFilterability(schema []*Property) error

	// ValidateFilterCondition 复用本地索引查询转换校验完整条件树，不连接索引。
	ValidateFilterCondition(condition FilterCondition, schema []*Property) error

	ListIndexes(ctx context.Context) ([]*IndexMeta, error)
	GetIndexMeta(ctx context.Context, index *IndexMeta) error
	CreateIndex(ctx context.Context, indexName string, schema []*Property, mappingMeta map[string]string) error
	UpdateIndex(ctx context.Context, indexName string, schema []*Property) error
	DeleteIndex(ctx context.Context, indexName string) error
	CheckIndexExist(ctx context.Context, indexName string) (bool, error)
	ValidateAnalyzer(ctx context.Context, analyzer string) (bool, error)
	GetIndexCapabilities(ctx context.Context) (*IndexCapabilities, error)

	ListDocuments(ctx context.Context, indexName string, res *Resource, params *ResourceDataQueryParams) (*ResourceDataQueryResult, error)
	GetDocument(ctx context.Context, indexName string, docID string) (map[string]any, error)
	GetDocuments(ctx context.Context, indexName string, docIDs []string) ([]map[string]any, error)
	CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error)
	IndexDocuments(ctx context.Context, indexName string, documents []DocumentWrite) ([]DocumentWriteResult, error)
	UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error)
	DeleteDocument(ctx context.Context, indexName string, docID string) error
	DeleteDocuments(ctx context.Context, indexName string, docIDs []string) error
	DeleteDocumentsByQuery(ctx context.Context, indexName string, res *Resource, params *ResourceDataQueryParams) (*DeleteByQueryResult, error)
}
