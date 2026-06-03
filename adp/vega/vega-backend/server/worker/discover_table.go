// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"fmt"

	"github.com/kweaver-ai/kweaver-go-lib/logger"

	"vega-backend/interfaces"
	"vega-backend/logics/connectors"
)

// tableDiscoverItem represents a table discover item.
type tableDiscoverItem struct {
	resource        *interfaces.Resource
	tableMeta       *interfaces.TableMeta
	markAfterEnrich bool
}

// discoverTableResources discovers table resources from a table connector.
// 分步执行：1. 获取表名列表 2. 创建/更新 Resource 3. 逐个补齐详细元数据
func (dh *DiscoverHandler) discoverTableResources(ctx context.Context, catalog *interfaces.Catalog,
	connector connectors.Connector, task *interfaces.DiscoverTask) (*interfaces.DiscoverResult, error) {

	tableConnector, ok := connector.(connectors.TableConnector)
	if !ok {
		return nil, fmt.Errorf("connector does not support table discover")
	}

	// Step 1: 获取表名列表
	sourceTables, err := tableConnector.ListTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	logger.Infof("Discovered %d tables from source", len(sourceTables))

	// Step 2: 获取现有 Resources
	existingResources, err := dh.rs.GetByCatalogID(ctx, catalog.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing resources: %w", err)
	}

	// Step 3: 对比并创建/更新 Resource（基础信息）
	result, items, err := dh.reconcileTableResources(ctx, catalog, sourceTables, existingResources, task.DiscoverActions)
	if err != nil {
		return nil, fmt.Errorf("failed to reconcile resources: %w", err)
	}

	// Step 4: 逐个补齐详细元数据:元数据采集就是补充每一个table的元数据信息
	if err := dh.enrichTableMetadata(ctx, tableConnector, items, result); err != nil {
		return nil, fmt.Errorf("failed to enrich table metadata: %w", err)
	}

	result.Message = formatDiscoverResultMessage(result)
	logger.Info(result.Message)

	return result, nil
}

// enrichTableMetadata 为表元数据添加详细信息
// 参数:
//   - ctx: 上下文信息，用于控制请求的超时和取消
//   - tableConnector: 表连接器，用于获取表的元数据
//   - items: 表发现项目列表，包含表元数据和资源信息
//
// 返回值:
//   - error: 如果在处理过程中发生错误，则返回错误信息
func (dh *DiscoverHandler) enrichTableMetadata(ctx context.Context, tableConnector connectors.TableConnector,
	items []tableDiscoverItem, result *interfaces.DiscoverResult) error {

	// 遍历所有表发现项目
	for _, item := range items {
		table := item.tableMeta   // 获取表元数据
		resource := item.resource // 获取资源信息
		beforeHash := sourceSnapshotHash(resource)

		// 获取详细元数据
		err := tableConnector.GetTableMeta(ctx, table)
		if err != nil {
			logger.Warnf("Failed to get metadata for table %s: %v", table.Name, err)
			return err
		}

		// 填充 Resource 元数据 ：schema_definition 字段
		resource.Database = table.Database
		resource.SchemaDefinition = []*interfaces.Property{}
		for _, column := range table.Columns {
			resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{
				Name:        column.Name,
				DisplayName: column.Name,
				Type:        tableConnector.MapType(column.Type),
				Description: column.Description,

				OriginalName:        column.Name,
				OriginalType:        column.Type,
				OriginalDescription: column.Description,
			})
		}
		// 填充 Resource 元数据 ：source_metadata 字段
		sourceMetadata := make(map[string]any)
		if resource.SourceMetadata != nil {
			sourceMetadata = resource.SourceMetadata
		}
		sourceMetadata["columns"] = table.Columns
		sourceMetadata["original_name"] = resource.SourceIdentifier
		sourceMetadata["original_description"] = table.Description
		if table.TableType != "" {
			sourceMetadata["table_type"] = table.TableType
		}
		if len(table.Properties) > 0 {
			sourceMetadata["properties"] = table.Properties
		}
		if len(table.PKs) > 0 {
			sourceMetadata["primary_keys"] = table.PKs
		}
		if len(table.Indices) > 0 {
			sourceMetadata["indices"] = table.Indices
		}
		if len(table.ForeignKeys) > 0 {
			sourceMetadata["foreign_keys"] = table.ForeignKeys
		}
		resource.SourceMetadata = sourceMetadata

		discoverStatus := resource.LastDiscoverStatus
		if item.markAfterEnrich {
			discoverStatus = discoverStatusAfterEnrich(resource, beforeHash)
			updateDiscoverResultForEnrichStatus(result, discoverStatus)
		}

		// 更新 Resource
		resource.LastDiscoverStatus = discoverStatus
		if err := dh.rs.UpdateResource(ctx, resource); err != nil {
			logger.Errorf("Failed to update metadata for table %s: %v", table.Name, err)
			return err
		}

		logger.Debugf("Enriched table %s: properties=%v, columns=%d, indices=%d, foreign_keys=%d", table.Name, table.Properties, len(table.Columns), len(table.Indices), len(table.ForeignKeys))
	}
	return nil
}

// reconcileTableResources reconciles source tables with existing resources.
func (dh *DiscoverHandler) reconcileTableResources(ctx context.Context, catalog *interfaces.Catalog, sourceTables []*interfaces.TableMeta,
	existingResources []*interfaces.Resource, actions *interfaces.DiscoverActions) (*interfaces.DiscoverResult, []tableDiscoverItem, error) {

	result := &interfaces.DiscoverResult{
		CatalogID: catalog.ID,
	}

	// 用于返回的 Discover Items
	var items []tableDiscoverItem

	// 构建现有资源的 map（按 SourceIdentifier 索引）
	existingMap := make(map[string]*interfaces.Resource)
	for _, r := range existingResources {
		if r.Category != interfaces.ResourceCategoryTable {
			continue
		}
		existingMap[r.SourceIdentifier] = r
	}

	// 构建源端表的 map
	sourceMap := make(map[string]*interfaces.TableMeta)
	for _, t := range sourceTables {
		sourceIdentifier := dh.buildSourceIdentifier(t)
		sourceMap[sourceIdentifier] = t
	}
	// 处理新增和保持的资源
	for _, table := range sourceTables {
		sourceIdentifier := dh.buildSourceIdentifier(table)

		if resource, ok := existingMap[sourceIdentifier]; ok {
			// 已存在，检查状态
			if actions != nil && actions.Refresh {
				markAfterEnrich := true
				if resource.Status == interfaces.ResourceStatusStale {
					// 之前标记为 stale，现在重新激活
					if err := dh.rs.UpdateStatus(ctx, resource.ID, interfaces.ResourceStatusActive, ""); err != nil {
						logger.Errorf("Failed to reactivate resource %s: %v", resource.ID, err)
					} else {
						dh.markDiscover(ctx, resource.ID, interfaces.DiscoverStatusRestored)
						resource.Status = interfaces.ResourceStatusActive
						resource.LastDiscoverStatus = interfaces.DiscoverStatusRestored
						result.RestoredCount++
						markAfterEnrich = false
					}
				}
				items = append(items, tableDiscoverItem{
					resource:        resource,
					tableMeta:       table,
					markAfterEnrich: markAfterEnrich,
				})
			}
		} else {
			// 新增资源 - 只在策略允许 create 时处理
			if actions != nil && actions.Create {
				resource, err := dh.createResource(ctx, catalog, table, sourceIdentifier)
				if err != nil {
					logger.Errorf("Failed to create resource %s: %v", sourceIdentifier, err)
				} else {
					dh.markDiscover(ctx, resource.ID, interfaces.DiscoverStatusNew)
					resource.LastDiscoverStatus = interfaces.DiscoverStatusNew
					result.NewCount++
					items = append(items, tableDiscoverItem{
						resource:  resource,
						tableMeta: table,
					})
				}
			}
		}
	}

	// 处理已删除的资源（标记为 stale） - 只在策略允许 mark_stale 时处理
	if actions != nil && actions.MarkStale {
		for sourceIdentifier, existing := range existingMap {
			if _, ok := sourceMap[sourceIdentifier]; !ok {
				dh.markDiscover(ctx, existing.ID, interfaces.DiscoverStatusMissing)
				if existing.Status == interfaces.ResourceStatusActive {
					if err := dh.rs.UpdateStatus(ctx, existing.ID, interfaces.ResourceStatusStale, ""); err != nil {
						logger.Errorf("Failed to mark resource %s as stale: %v", existing.ID, err)
					} else {
						result.StaleCount++
					}
				}
			}
		}
	}
	return result, items, nil
}

// buildSourceIdentifier builds the source identifier for a table.
func (dh *DiscoverHandler) buildSourceIdentifier(table *interfaces.TableMeta) string {
	identifier := table.Name
	if table.Schema != "" {
		identifier = fmt.Sprintf("%s.%s", table.Schema, identifier)
	}
	if table.Database != "" {
		return fmt.Sprintf("%s.%s", table.Database, identifier)
	}
	return identifier
}

// createResource creates a new resource.
func (dh *DiscoverHandler) createResource(ctx context.Context, catalog *interfaces.Catalog, table *interfaces.TableMeta, sourceIdentifier string) (*interfaces.Resource, error) {

	req := &interfaces.ResourceRequest{
		CatalogID:        catalog.ID,
		Name:             sourceIdentifier,
		Description:      table.Description,
		Category:         interfaces.ResourceCategoryTable,
		Status:           interfaces.ResourceStatusActive,
		Database:         table.Database,
		SourceIdentifier: sourceIdentifier,
		SourceMetadata: map[string]any{
			"original_name":        sourceIdentifier,
			"original_description": table.Description,
		},
	}
	resource, err := dh.rs.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	return resource, nil
}
