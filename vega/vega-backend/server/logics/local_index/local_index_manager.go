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

var analyzerCandidates = []string{"standard", "english", "ik_max_word", "hanlp_standard", "hanlp_index"}

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

// ValidateFilterCondition 沿用本地索引的字段投影及连接器转换，不执行查询。
func (lim *localIndexManager) ValidateFilterCondition(condition interfaces.FilterCondition, properties []*interfaces.Property) error {
	if condition == nil {
		return nil
	}
	_, err := lim.lic.ConvertFilterCondition(condition, SchemaForQuery(properties))
	return err
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
		return nil, interfaces.NewIndexCapabilitiesUnavailableError(err)
	}
	lim.capabilityMu.RUnlock()

	lim.capabilityMu.Lock()
	defer lim.capabilityMu.Unlock()
	if lim.capabilitiesReady {
		return cloneIndexCapabilities(lim.capabilities), nil
	}
	if lim.capabilityErr != nil && time.Now().Before(lim.capabilityErrorExpires) {
		return nil, interfaces.NewIndexCapabilitiesUnavailableError(lim.capabilityErr)
	}

	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), capabilityProbeTimeout)
	capabilities, err := lim.probeIndexCapabilities(probeCtx)
	cancel()
	lim.storeIndexCapabilitiesLocked(capabilities, err)
	if err != nil {
		return nil, interfaces.NewIndexCapabilitiesUnavailableError(err)
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

func (lim *localIndexManager) ListDocuments(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	if err := validateQueryFields(res, params); err != nil {
		return nil, err
	}
	queryResource, err := resourceForQuery(res)
	if err != nil {
		return nil, err
	}
	decimalConfig, err := decimalAggregateConfig(res, params)
	if err != nil {
		return nil, err
	}
	if decimalConfig.Precision > 0 && params.Having != nil && params.Having.Field != "count(*)" {
		for _, property := range queryResource.SchemaDefinition {
			if property.Name != params.Aggregation.Property {
				continue
			}
			// 查询字段已是副本；补齐有效小数定义供 HAVING 使用，不修改资源属性。
			attributes := make(map[string]any, len(property.Attributes)+2)
			for key, value := range property.Attributes {
				attributes[key] = value
			}
			attributes["precision"] = decimalConfig.Precision
			attributes["scale"] = decimalConfig.Scale
			property.Attributes = attributes
			break
		}
	}
	result, err := lim.lic.ExecuteQuery(ctx, indexName, queryResource, params)
	if err != nil {
		return nil, err
	}
	if err := normalizeQueryJSONValues(res, params, result); err != nil {
		return nil, err
	}
	if err := normalizeDecimalAggregate(decimalConfig, params, result); err != nil {
		return nil, err
	}
	return result, nil
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

func (lim *localIndexManager) UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error) {
	return lim.lic.UpsertDocuments(ctx, indexName, updateRequests)
}

func (lim *localIndexManager) DeleteDocument(ctx context.Context, indexName string, docID string) error {
	return lim.lic.DeleteDocument(ctx, indexName, docID)
}

func (lim *localIndexManager) DeleteDocuments(ctx context.Context, indexName string, docIDs []string) error {
	return lim.lic.DeleteDocuments(ctx, indexName, docIDs)
}

func (lim *localIndexManager) DeleteDocumentsByQuery(ctx context.Context, indexName string,
	res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.DeleteByQueryResult, error) {
	return lim.lic.DeleteDocumentsByQuery(ctx, indexName, params, SchemaForQuery(res.SchemaDefinition))
}

func (lim *localIndexManager) IndexDocuments(ctx context.Context, indexName string,
	documents []interfaces.DocumentWrite) ([]interfaces.DocumentWriteResult, error) {
	return lim.lic.IndexDocuments(ctx, indexName, documents)
}

// SchemaForQuery projects a non-Index resource schema for queries on its managed local index.
// Native Index resources already carry their physical schema and do not need this conversion.
// JSON is stored only in _source; binary and other are unavailable for index queries.
// Managed documents and mappings are keyed by Property.Name; OriginalName only
// belongs to source connectors and must not leak into local-index DSL.
func SchemaForQuery(schema []*interfaces.Property) []*interfaces.Property {
	result := make([]*interfaces.Property, 0, len(schema))
	for _, property := range schema {
		if property == nil || !isQueryableFieldType(property.Type) {
			continue
		}
		cloned := *property
		cloned.OriginalName = property.Name
		// 查询使用索引的物理字符串语义，不继承源端大整数能力。
		if cloned.Type == interfaces.DataType_LargeInteger {
			cloned.Type = interfaces.DataType_String
		}
		result = append(result, &cloned)
	}
	return result
}

// resourceForQuery 准备托管索引的查询副本，保留源 schema 的属性和特征配置。
func resourceForQuery(resource *interfaces.Resource) (*interfaces.Resource, error) {
	if resource == nil {
		return nil, interfaces.NewConditionBuildError("resource is required for local index queries")
	}
	cloned := *resource
	cloned.SchemaDefinition = SchemaForQuery(resource.SchemaDefinition)
	for _, prop := range GeneratedFields(resource.SchemaDefinition) {
		cloned.SchemaDefinition = append(cloned.SchemaDefinition, prop)
	}
	return &cloned, nil
}

// normalizeQueryJSONValues 保持外部数据源的 JSON 文本输出合同；Dataset 和聚合结果保持原值。
func normalizeQueryJSONValues(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams, result *interfaces.ResourceDataQueryResult) error {
	if resource == nil || resource.Category == interfaces.ResourceCategoryDataset {
		return nil
	}
	if params != nil && (params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil) {
		return nil
	}
	for _, property := range resource.SchemaDefinition {
		if property == nil || property.Type != interfaces.DataType_Json {
			continue
		}
		for _, entry := range result.Entries {
			value, exists := entry[property.Name]
			if !exists {
				continue
			}
			text, err := common.JSONQueryValueText(value)
			if err != nil {
				return fmt.Errorf("failed to serialize JSON field %q", property.Name)
			}
			entry[property.Name] = text
		}
	}
	return nil
}

// validateQueryFields 校验不经过条件构造器的排序和聚合字段，沿用字段不存在的错误口径。
func validateQueryFields(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) error {
	if resource == nil || params == nil {
		return nil
	}
	fields := make([]string, 0, len(params.Sort)+len(params.GroupBy)+1)
	for _, sort := range params.Sort {
		if sort != nil {
			if params.Aggregation == nil && len(params.GroupBy) == 0 && (sort.Field == "_score" || sort.Field == "_id") {
				continue
			}
			// 聚合结果别名不是源字段，不能按源字段类型拒绝排序。
			if params.Aggregation != nil && (sort.Field == params.Aggregation.Alias || sort.Field == "__value") {
				continue
			}
			fields = append(fields, sort.Field)
		}
	}
	if params.Aggregation != nil {
		fields = append(fields, params.Aggregation.Property)
	}
	for _, group := range params.GroupBy {
		if group != nil {
			fields = append(fields, group.Property)
		}
	}
	queryFields := make(map[string]struct{}, len(resource.SchemaDefinition))
	for _, property := range SchemaForQuery(resource.SchemaDefinition) {
		queryFields[property.Name] = struct{}{}
	}
	for name := range GeneratedFields(resource.SchemaDefinition) {
		queryFields[name] = struct{}{}
	}
	for _, field := range fields {
		if _, ok := queryFields[field]; !ok {
			return interfaces.NewConditionBuildError("field %q not found in schema", field)
		}
	}

	return nil
}

// isQueryableFieldType 统一托管索引条件、排序和聚合可使用的字段类型。
func isQueryableFieldType(fieldType string) bool {
	switch fieldType {
	case interfaces.DataType_Json, interfaces.DataType_Binary, interfaces.DataType_Other:
		return false
	default:
		return true
	}
}
