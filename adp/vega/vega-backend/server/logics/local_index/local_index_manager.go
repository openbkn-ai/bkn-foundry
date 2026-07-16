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
	"sync"

	"vega-backend/common"
	"vega-backend/interfaces"
	opensearchConnector "vega-backend/logics/connectors/local/index/opensearch"
	"vega-backend/logics/filter_condition"
)

var (
	managerOnce sync.Once
	managerInst interfaces.LocalIndexManager
)

type localIndexManager struct {
	c interfaces.IndexConnector
}

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

		managerInst = &localIndexManager{
			c: connector.(interfaces.IndexConnector),
		}
	})
	return managerInst
}

func (lim *localIndexManager) CreateIndex(ctx context.Context, indexName string, schema []*interfaces.Property) error {
	return lim.c.Create(ctx, indexName, schema)
}

func (lim *localIndexManager) UpdateIndex(ctx context.Context, indexName string, schema []*interfaces.Property) error {
	return lim.c.Update(ctx, indexName, schema)
}

func (lim *localIndexManager) DeleteIndex(ctx context.Context, indexName string) error {
	return lim.c.Delete(ctx, indexName)
}

func (lim *localIndexManager) CheckExist(ctx context.Context, indexName string) (bool, error) {
	return lim.c.CheckExist(ctx, indexName)
}

func (lim *localIndexManager) ListDocuments(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
	queryResult, err := lim.c.ExecuteQuery(ctx, indexName, res, params)
	if err != nil {
		return nil, 0, err
	}

	return queryResult.Rows, queryResult.Total, nil
}

func (lim *localIndexManager) GetDocument(ctx context.Context, indexName string, docID string) (map[string]any, error) {
	return lim.c.GetDocument(ctx, indexName, docID)
}

func (lim *localIndexManager) CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error) {
	return lim.c.CreateDocuments(ctx, indexName, documents)
}

func (lim *localIndexManager) UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error) {
	return lim.c.UpsertDocuments(ctx, indexName, updateRequests)
}

func (lim *localIndexManager) DeleteDocument(ctx context.Context, indexName string, docID string) error {
	return lim.c.DeleteDocument(ctx, indexName, docID)
}

func (lim *localIndexManager) DeleteDocuments(ctx context.Context, indexName string, docIDs string) error {
	return lim.c.DeleteDocuments(ctx, indexName, docIDs)
}

func (lim *localIndexManager) DeleteDocumentsByQuery(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) error {
	fieldMap := map[string]*interfaces.Property{}
	for _, prop := range res.SchemaDefinition {
		fieldMap[prop.Name] = prop
	}

	actualFilterCond, err := filter_condition.NewFilterCondition(ctx, params.FilterCondCfg, fieldMap)
	if err != nil {
		return err
	}
	params.ActualFilterCond = actualFilterCond

	return lim.c.DeleteDocumentsByQuery(ctx, indexName, params, res.SchemaDefinition)
}
