// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package local_index manages local index storage backed by OpenSearch.
package local_index

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"vega-backend/common"
	"vega-backend/interfaces"
	opensearchConnector "vega-backend/logics/connector/local/index/opensearch"
	"vega-backend/logics/filter_condition"
)

var (
	managerOnce sync.Once
	managerInst interfaces.LocalIndexManager
)

type localIndexManager struct {
	lic interfaces.IndexConnector // Local Index Connector

	capabilities  interfaces.IndexCapabilities
	capabilityErr error
}

var analyzerCandidates = []string{"standard", "english", "ik_max_word", "hanlp_index"}

// NewLocalIndexManager creates a LocalIndexManager.
func NewLocalIndexManager(appSetting *common.AppSetting) interfaces.LocalIndexManager {
	managerOnce.Do(func() {
		opensearchSetting, ok := appSetting.DepServices["opensearch"]
		if !ok {
			panic("opensearch service not found in depServices")
		}

		cfg := interfaces.ConnectorConfig{
			"host":          opensearchSetting["host"],
			"port":          opensearchSetting["port"],
			"username":      opensearchSetting["user"],
			"password":      opensearchSetting["password"],
			"index_pattern": opensearchSetting["index_pattern"],
		}

		connector, err := opensearchConnector.NewOpenSearchConnector().New(cfg)
		if err != nil {
			panic(fmt.Sprintf("failed to create OpenSearch connector: %v", err))
		}

		manager := &localIndexManager{
			lic:          connector.(interfaces.IndexConnector),
			capabilities: interfaces.IndexCapabilities{CheckedAt: time.Now().UnixMilli()},
		}
		for _, analyzer := range analyzerCandidates {
			available, err := manager.lic.ValidateAnalyzer(context.Background(), analyzer)
			if err != nil {
				manager.capabilityErr = err
				manager.capabilities.FulltextAnalyzers = nil
				break
			}
			if available {
				manager.capabilities.FulltextAnalyzers = append(manager.capabilities.FulltextAnalyzers, interfaces.AnalyzerCapability{ID: analyzer})
			}
		}
		managerInst = manager
	})
	return managerInst
}

func (lim *localIndexManager) CreateIndex(ctx context.Context, indexName string, schema []*interfaces.Property) error {
	return lim.lic.Create(ctx, indexName, schema)
}

func (lim *localIndexManager) UpdateIndex(ctx context.Context, indexName string, schema []*interfaces.Property) error {
	return lim.lic.Update(ctx, indexName, schema)
}

func (lim *localIndexManager) DeleteIndex(ctx context.Context, indexName string) error {
	return lim.lic.Delete(ctx, indexName)
}

func (lim *localIndexManager) CheckExist(ctx context.Context, indexName string) (bool, error) {
	return lim.lic.CheckExist(ctx, indexName)
}

func (lim *localIndexManager) ValidateAnalyzer(ctx context.Context, analyzer string) (bool, error) {
	if _, err := lim.GetIndexCapabilities(ctx); err != nil {
		return false, err
	}
	analyzer = strings.TrimSpace(analyzer)
	if analyzer == "" {
		return true, nil
	}
	available := map[string]struct{}{}
	for _, item := range lim.capabilities.FulltextAnalyzers {
		available[item.ID] = struct{}{}
	}
	_, ok := available[analyzer]
	return ok, nil
}

func (lim *localIndexManager) GetIndexCapabilities(_ context.Context) (*interfaces.IndexCapabilities, error) {
	if lim.capabilityErr != nil {
		return nil, &interfaces.IndexCapabilitiesUnavailableError{Cause: lim.capabilityErr}
	}
	result := lim.capabilities
	result.FulltextAnalyzers = append([]interfaces.AnalyzerCapability(nil), lim.capabilities.FulltextAnalyzers...)
	return &result, nil
}

func (lim *localIndexManager) ListDocuments(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
	queryResult, err := lim.lic.ExecuteQuery(ctx, indexName, res, params)
	if err != nil {
		return nil, 0, err
	}
	if params != nil {
		params.SearchAfter = append([]any(nil), queryResult.SearchAfter...)
	}

	return queryResult.Entries, queryResult.Total, nil
}

func (lim *localIndexManager) GetDocument(ctx context.Context, indexName string, docID string) (map[string]any, error) {
	return lim.lic.GetDocument(ctx, indexName, docID)
}

func (lim *localIndexManager) CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error) {
	return lim.lic.CreateDocuments(ctx, indexName, documents)
}

func (lim *localIndexManager) UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error) {
	return lim.lic.UpsertDocuments(ctx, indexName, updateRequests)
}

func (lim *localIndexManager) DeleteDocument(ctx context.Context, indexName string, docID string) error {
	return lim.lic.DeleteDocument(ctx, indexName, docID)
}

func (lim *localIndexManager) DeleteDocuments(ctx context.Context, indexName string, docIDs string) error {
	return lim.lic.DeleteDocuments(ctx, indexName, docIDs)
}

func (lim *localIndexManager) DeleteDocumentsByQuery(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) error {
	fieldMap := map[string]*interfaces.Property{}
	for _, prop := range res.SchemaDefinition {
		fieldMap[prop.Name] = prop
	}

	// 本地索引里还有构建任务生成的向量字段，它们不在资源 schema 上。不补进来，
	// knn_vector 条件会在字段查找阶段就被判成「字段不存在」。
	for name, prop := range interfaces.LocalIndexGeneratedFields(res) {
		fieldMap[name] = prop
	}
	actualFilterCond, err := filter_condition.NewFilterCondition(ctx, params.FilterCondCfg, fieldMap)
	if err != nil {
		return err
	}
	params.ActualFilterCond = actualFilterCond

	return lim.lic.DeleteDocumentsByQuery(ctx, indexName, params, res.SchemaDefinition)
}
