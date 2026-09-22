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

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	opensearchConnector "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/index/opensearch"
)

var (
	managerOnce sync.Once
	managerInst interfaces.LocalIndexManager
)

const (
	capabilityProbeTimeout = 10 * time.Second
	capabilityErrorTTL     = 30 * time.Second
)

type localIndexManager struct {
	lic interfaces.IndexConnector // Local Index Connector

	capabilityMu           sync.RWMutex
	capabilities           interfaces.IndexCapabilities
	capabilitiesReady      bool
	capabilityErr          error
	capabilityErrorExpires time.Time
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
			"host":           opensearchSetting["host"],
			"port":           opensearchSetting["port"],
			"username":       opensearchSetting["user"],
			"password":       opensearchSetting["password"],
			"index_patterns": opensearchSetting["index_patterns"],
		}

		connector, err := opensearchConnector.NewOpenSearchConnector().New(cfg)
		if err != nil {
			panic(fmt.Sprintf("failed to create OpenSearch connector: %v", err))
		}

		manager := &localIndexManager{
			lic: connector.(interfaces.IndexConnector),
		}

		probeCtx, cancel := context.WithTimeout(context.Background(), capabilityProbeTimeout)
		capabilities, probeErr := manager.probeIndexCapabilities(probeCtx)
		cancel()
		manager.capabilityMu.Lock()
		manager.storeIndexCapabilitiesLocked(capabilities, probeErr)
		manager.capabilityMu.Unlock()
		managerInst = manager
	})
	return managerInst
}

func (lim *localIndexManager) ListIndexes(ctx context.Context) ([]*interfaces.IndexMeta, error) {
	return lim.lic.ListIndexes(ctx)
}

func (lim *localIndexManager) GetIndexMeta(ctx context.Context, index *interfaces.IndexMeta) error {
	return lim.lic.GetIndexMeta(ctx, index)
}

func (lim *localIndexManager) CreateIndex(ctx context.Context, indexName string, schema []*interfaces.Property, mappingMeta map[string]string) error {
	properties, err := buildFieldMappings(schema)
	if err != nil {
		return err
	}
	return lim.lic.CreateIndex(ctx, indexName, properties, mappingMeta)
}

func (lim *localIndexManager) UpdateIndex(ctx context.Context, indexName string, schema []*interfaces.Property) error {
	properties, err := buildFieldMappings(schema)
	if err != nil {
		return err
	}
	return lim.lic.UpdateIndex(ctx, indexName, properties)
}

func (lim *localIndexManager) DeleteIndex(ctx context.Context, indexName string) error {
	return lim.lic.DeleteIndex(ctx, indexName)
}

func (lim *localIndexManager) CheckIndexExist(ctx context.Context, indexName string) (bool, error) {
	return lim.lic.CheckIndexExist(ctx, indexName)
}

func (lim *localIndexManager) ValidateAnalyzer(ctx context.Context, analyzer string) (bool, error) {
	capabilities, err := lim.GetIndexCapabilities(ctx)
	if err != nil {
		return false, err
	}
	analyzer = strings.TrimSpace(analyzer)
	if analyzer == "" {
		return true, nil
	}
	available := map[string]struct{}{}
	for _, item := range capabilities.FulltextAnalyzers {
		available[item.ID] = struct{}{}
	}
	_, ok := available[analyzer]
	return ok, nil
}

func (lim *localIndexManager) GetIndexCapabilities(ctx context.Context) (*interfaces.IndexCapabilities, error) {
	lim.capabilityMu.RLock()
	if lim.capabilitiesReady {
		capabilities := cloneIndexCapabilities(lim.capabilities)
		lim.capabilityMu.RUnlock()
		return capabilities, nil
	}
	if lim.capabilityErr != nil && time.Now().Before(lim.capabilityErrorExpires) {
		err := lim.capabilityErr
		lim.capabilityMu.RUnlock()
		return nil, &interfaces.IndexCapabilitiesUnavailableError{Cause: err}
	}
	lim.capabilityMu.RUnlock()

	lim.capabilityMu.Lock()
	defer lim.capabilityMu.Unlock()
	if lim.capabilitiesReady {
		return cloneIndexCapabilities(lim.capabilities), nil
	}
	if lim.capabilityErr != nil && time.Now().Before(lim.capabilityErrorExpires) {
		return nil, &interfaces.IndexCapabilitiesUnavailableError{Cause: lim.capabilityErr}
	}

	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), capabilityProbeTimeout)
	capabilities, err := lim.probeIndexCapabilities(probeCtx)
	cancel()
	lim.storeIndexCapabilitiesLocked(capabilities, err)
	if err != nil {
		return nil, &interfaces.IndexCapabilitiesUnavailableError{Cause: err}
	}
	return cloneIndexCapabilities(capabilities), nil
}

func (lim *localIndexManager) storeIndexCapabilitiesLocked(capabilities interfaces.IndexCapabilities, err error) {
	if err != nil {
		lim.capabilities = interfaces.IndexCapabilities{}
		lim.capabilitiesReady = false
		lim.capabilityErr = err
		lim.capabilityErrorExpires = time.Now().Add(capabilityErrorTTL)
		return
	}
	lim.capabilities = capabilities
	lim.capabilitiesReady = true
	lim.capabilityErr = nil
	lim.capabilityErrorExpires = time.Time{}
}

func (lim *localIndexManager) probeIndexCapabilities(ctx context.Context) (interfaces.IndexCapabilities, error) {
	capabilities := interfaces.IndexCapabilities{}
	for _, analyzer := range analyzerCandidates {
		available, err := lim.lic.ValidateAnalyzer(ctx, analyzer)
		if err != nil {
			return interfaces.IndexCapabilities{}, err
		}
		if available {
			capabilities.FulltextAnalyzers = append(capabilities.FulltextAnalyzers, interfaces.AnalyzerCapability{ID: analyzer})
		}
	}
	capabilities.CheckedAt = time.Now().UnixMilli()
	return capabilities, nil
}

func cloneIndexCapabilities(capabilities interfaces.IndexCapabilities) *interfaces.IndexCapabilities {
	result := capabilities
	result.FulltextAnalyzers = append([]interfaces.AnalyzerCapability(nil), capabilities.FulltextAnalyzers...)
	return &result
}

func (lim *localIndexManager) ListDocuments(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
	queryResult, err := lim.lic.ExecuteQuery(ctx, indexName, resourceForQuery(res), params)
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

func (lim *localIndexManager) GetDocuments(ctx context.Context, indexName string, docIDs []string) ([]map[string]any, error) {
	return lim.lic.GetDocuments(ctx, indexName, docIDs)
}

func (lim *localIndexManager) CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error) {
	return lim.lic.CreateDocuments(ctx, indexName, documents)
}

func (lim *localIndexManager) IndexDocuments(ctx context.Context, indexName string, documents map[string]map[string]any) ([]string, error) {
	return lim.lic.IndexDocuments(ctx, indexName, documents)
}

func (lim *localIndexManager) UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error) {
	return lim.lic.UpsertDocuments(ctx, indexName, updateRequests)
}

func (lim *localIndexManager) DeleteDocument(ctx context.Context, indexName string, docID string) error {
	return lim.lic.DeleteDocument(ctx, indexName, docID)
}

func (lim *localIndexManager) DeleteDocuments(ctx context.Context, indexName string, docIDs []string) error {
	return lim.lic.DeleteDocuments(ctx, indexName, docIDs)
}

func (lim *localIndexManager) DeleteDocumentsByQuery(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) error {
	return lim.lic.DeleteDocumentsByQuery(ctx, indexName, params, SchemaForQuery(res.SchemaDefinition))
}

// SchemaForQuery returns the physical schema exposed by a managed local index.
// Managed documents and mappings are keyed by Property.Name; OriginalName only
// belongs to source connectors and must not leak into local-index DSL.
func SchemaForQuery(schema []*interfaces.Property) []*interfaces.Property {
	result := make([]*interfaces.Property, 0, len(schema))
	for _, property := range schema {
		cloned := *property
		cloned.OriginalName = property.Name
		result = append(result, &cloned)
	}
	return result
}

func resourceForQuery(resource *interfaces.Resource) *interfaces.Resource {
	if resource == nil {
		return nil
	}
	cloned := *resource
	cloned.SchemaDefinition = SchemaForQuery(resource.SchemaDefinition)
	return &cloned
}
