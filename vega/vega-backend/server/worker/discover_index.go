// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// indexDiscoverItem represents an index discover item.
type indexDiscoverItem struct {
	resource        *interfaces.Resource
	indexMeta       *interfaces.IndexMeta
	markAfterEnrich bool
	countOnly       bool
}

func (dtw *DiscoverTaskWorker) discoverIndexResources(ctx context.Context,
	task *interfaces.DiscoverTask, catalog *interfaces.Catalog, connector interfaces.Connector,
	progress *discoverTaskReconcileProgress) (*interfaces.DiscoverResult, error) {

	indexConnector, ok := connector.(interfaces.IndexConnector)
	if !ok {
		return nil, fmt.Errorf("connector does not support index discover")
	}

	sourceIndices, err := indexConnector.ListIndexes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list indices: %w", err)
	}
	if current, changed := progress.MarkSourceListed(); changed {
		if err := dtw.updateProgress(ctx, task.ID, current, "source indices listed"); err != nil {
			return nil, err
		}
	}
	logger.Infof("Discovered %d indices from source", len(sourceIndices))

	existingResources, err := dtw.rs.InternalGetByCatalogID(ctx, catalog.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing resources: %w", err)
	}
	logger.Infof("Loaded %d existing resources for index discovery", len(existingResources))

	result, items, err := dtw.reconcileIndexResources(ctx, task, catalog, sourceIndices, existingResources)
	if err != nil {
		return nil, fmt.Errorf("failed to reconcile resources: %w", err)
	}
	if current, changed := progress.MarkResourcesReconciled(); changed {
		if err := dtw.updateProgress(ctx, task.ID, current, "resources reconciled"); err != nil {
			return nil, err
		}
	}
	logger.Infof("Reconciled %d index resources", len(items))

	if err := dtw.enrichIndexMetadata(ctx, task, indexConnector, items, result, progress); err != nil {
		return nil, fmt.Errorf("failed to enrich index metadata: %w", err)
	}
	if current, changed := progress.MarkMetadataEnriched(); changed {
		if err := dtw.updateProgress(ctx, task.ID, current, "resource metadata enriched"); err != nil {
			return nil, err
		}
	}
	logger.Infof("Enriched metadata for %d index resources", len(items))

	result.Message = formatDiscoverResultMessage(result)
	logger.Info(result.Message)

	return result, nil
}

func (dtw *DiscoverTaskWorker) reconcileIndexResources(ctx context.Context,
	task *interfaces.DiscoverTask, catalog *interfaces.Catalog, sourceIndices []*interfaces.IndexMeta,
	existingResources []*interfaces.Resource) (*interfaces.DiscoverResult, []indexDiscoverItem, error) {

	actions := task.DiscoverActions

	result := &interfaces.DiscoverResult{
		CatalogID: catalog.ID,
	}

	var items []indexDiscoverItem

	existingMap := make(map[string]*interfaces.Resource)
	for _, r := range existingResources {
		if r.Category != interfaces.ResourceCategoryIndex {
			continue
		}
		existingMap[r.SourceIdentifier] = r
	}

	sourceMap := make(map[string]*interfaces.IndexMeta)
	for _, idx := range sourceIndices {
		sourceMap[idx.Name] = idx
	}

	for _, idx := range sourceIndices {
		if dtw.stopped.Load() {
			return nil, nil, ErrWorkerManagerStopping
		}
		sourceIdentifier := idx.Name

		if resource, ok := existingMap[sourceIdentifier]; ok {
			if actions != nil && (actions.Refresh || actions.Count) {
				markAfterEnrich := actions.Refresh
				if actions.Refresh && resource.Status == interfaces.ResourceStatusStale {
					if err := dtw.rs.UpdateStatus(ctx, resource.ID, interfaces.ResourceStatusActive, ""); err != nil {
						logger.Errorf("Failed to reactivate resource %s: %v", resource.ID, err)
					} else {
						dtw.markDiscover(ctx, resource.ID, interfaces.DiscoverStatusRestored)
						resource.Status = interfaces.ResourceStatusActive
						resource.LastDiscoverStatus = interfaces.DiscoverStatusRestored
						result.RestoredCount++
						markAfterEnrich = false
					}
				}
				items = append(items, indexDiscoverItem{
					resource:        resource,
					indexMeta:       idx,
					markAfterEnrich: markAfterEnrich,
					countOnly:       !actions.Refresh,
				})
			}
		} else {
			if actions != nil && actions.Create {
				resource, err := dtw.createIndexResource(ctx, catalog, idx)
				if err != nil {
					logger.Errorf("Failed to create resource %s: %v", sourceIdentifier, err)
				} else {
					dtw.markDiscover(ctx, resource.ID, interfaces.DiscoverStatusNew)
					resource.LastDiscoverStatus = interfaces.DiscoverStatusNew
					result.NewCount++
					items = append(items, indexDiscoverItem{
						resource:  resource,
						indexMeta: idx,
					})
				}
			}
		}
	}

	if actions != nil && actions.MarkStale {
		for sourceIdentifier, existing := range existingMap {
			if dtw.stopped.Load() {
				return nil, nil, ErrWorkerManagerStopping
			}
			if _, ok := sourceMap[sourceIdentifier]; !ok {
				dtw.markDiscover(ctx, existing.ID, interfaces.DiscoverStatusMissing)
				if existing.Status == interfaces.ResourceStatusActive {
					if err := dtw.rs.UpdateStatus(ctx, existing.ID, interfaces.ResourceStatusStale, ""); err != nil {
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

// createIndexResource creates a new resource for an index.
func (dtw *DiscoverTaskWorker) createIndexResource(ctx context.Context,
	catalog *interfaces.Catalog, index *interfaces.IndexMeta) (*interfaces.Resource, error) {

	req := &interfaces.ResourceRequest{
		CatalogID:        catalog.ID,
		Builtin:          &catalog.Builtin,
		Name:             index.Name,
		Description:      index.Description,
		Category:         interfaces.ResourceCategoryIndex,
		Enabled:          true,
		Status:           interfaces.ResourceStatusActive,
		SourceIdentifier: index.Name,
		SourceMetadata: map[string]any{
			"original_name":        index.Name,
			"original_description": index.Description,
		},
	}
	resource, err := dtw.rs.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	return resource, nil
}

// enrichIndexMetadata refreshes source metadata while preserving business metadata.
func (dtw *DiscoverTaskWorker) enrichIndexMetadata(ctx context.Context, task *interfaces.DiscoverTask,
	indexConnector interfaces.IndexConnector, items []indexDiscoverItem, result *interfaces.DiscoverResult,
	progress *discoverTaskReconcileProgress) error {

	progress.SetMetadataTotal(len(items))

	for itemIndex, item := range items {
		if dtw.stopped.Load() {
			return ErrWorkerManagerStopping
		}
		idx := item.indexMeta
		resource := item.resource

		if item.countOnly {
			if err := dtw.enrichResourceIndexRowCount(ctx, task, resource, indexConnector, idx, result); err != nil {
				return err
			}
			if err := dtw.updateProgress(ctx, task.ID, 5+90*(itemIndex+1)/len(items), fmt.Sprintf("Exact count processed: %d/%d", itemIndex+1, len(items))); err != nil {
				return err
			}
			continue
		}

		beforeHash := sourceSnapshotHash(resource)

		if err := indexConnector.GetIndexMeta(ctx, idx); err != nil {
			logger.Warnf("Failed to get metadata for index %s: %v", idx.Name, err)
			resource.LastDiscoverStatus = interfaces.DiscoverStatusError
			resource.StatusMessage = fmt.Sprintf("discover metadata failed: %v", err)
			updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusError)
			expectedUpdateTime := resource.UpdateTime
			resource.Updater = task.Creator
			resource.UpdateTime = time.Now().UnixMilli()
			if updateErr := dtw.rs.InternalUpdateDiscoveryMetadata(ctx, nil, resource, expectedUpdateTime); updateErr != nil {
				logger.Errorf("Failed to update discover error for index %s: %v", idx.Name, updateErr)
				return updateErr
			}
			if current, changed := progress.AdvanceMetadata(); changed {
				message := fmt.Sprintf("resource metadata enriched: %d/%d", progress.metadataProcessed, progress.metadataTotal)
				if err := dtw.updateProgress(ctx, task.ID, current, message); err != nil {
					return err
				}
			}
			continue
		}

		var exactCount *int64
		if task.DiscoverActions != nil && task.DiscoverActions.Count {
			queryCtx, cancel := context.WithTimeout(ctx, resourceCountTimeout)
			count, err := indexConnector.CountRows(queryCtx, idx)
			cancel()
			if parentErr := ctx.Err(); parentErr != nil {
				return parentErr
			}
			if err == nil && count < 0 {
				err = fmt.Errorf("invalid negative row count")
			}
			if err != nil {
				result.FailedCount++
				logger.Warnf("Failed to count index %s during discovery: %v", idx.Name, err)
				if current, changed := progress.AdvanceMetadata(); changed {
					if err := dtw.updateProgress(ctx, task.ID, current, fmt.Sprintf("resource metadata enriched: %d/%d", progress.metadataProcessed, progress.metadataTotal)); err != nil {
						return err
					}
				}
				continue
			}
			exactCount = &count
		}

		existingProperties := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
		for _, property := range resource.SchemaDefinition {
			if property != nil {
				existingProperties[property.Name] = property
			}
		}

		var props []*interfaces.Property
		for _, field := range idx.Mapping {
			delete(field.Attributes, "type")
			nativeType := field.Type
			if field.ResolvedType != "" {
				nativeType = field.ResolvedType
			}

			property := &interfaces.Property{
				Name:        field.Name,
				DisplayName: field.Name,
				Type:        indexConnector.MapType(nativeType),
				Description: field.Description,

				OriginalName:        field.Name,
				OriginalType:        field.Type,
				OriginalDescription: field.Description,
				Attributes:          field.Attributes,
				Features:            buildSubFieldFeatures(field.Name, field.SubFields),
			}
			if existing, ok := existingProperties[field.Name]; ok {
				property.DisplayName = existing.DisplayName
				property.Description = resolveSourceDescription(existing.Description, existing.OriginalDescription, field.Description)
				property.Features = mergeIndexFeatures(existing.Features, property.Features)
			}
			props = append(props, property)
		}
		resource.SchemaDefinition = props

		resource.Description = resolveSourceDescription(resource.Description, sourceOriginalDescription(resource.SourceMetadata), idx.Description)

		sourceMetadata := resource.SourceMetadata
		if resource.SourceMetadata == nil {
			sourceMetadata = make(map[string]any)
		}
		sourceMetadata["original_name"] = idx.Name
		sourceMetadata["original_description"] = idx.Description
		observedAt := time.Now().UnixMilli()
		properties := idx.Properties
		if properties == nil {
			properties = map[string]any{}
		}

		if exactCount != nil {
			resource.RowCount = exactCount
			resource.RowCountTime = &observedAt
		}
		sourceMetadata["properties"] = properties
		sourceMetadata["mapping"] = idx.Mapping
		sourceMetadata["mapping_meta"] = idx.MappingMeta
		resource.SourceMetadata = sourceMetadata

		discoverStatus := resource.LastDiscoverStatus
		if item.markAfterEnrich {
			discoverStatus = discoverStatusAfterEnrich(resource, beforeHash)
			updateDiscoverResultForEnrichStatus(result, discoverStatus)
		}

		resource.LastDiscoverStatus = discoverStatus
		resource.LastDiscoverTime = observedAt
		resource.StatusMessage = ""
		expectedUpdateTime := resource.UpdateTime
		resource.Updater = task.Creator
		resource.UpdateTime = observedAt
		if err := dtw.saveDiscoveredResource(ctx, resource, expectedUpdateTime, exactCount, observedAt); err != nil {
			logger.Errorf("Failed to update metadata for index %s: %v", idx.Name, err)
			return err
		}

		logger.Debugf("Enriched index %s: fields=%d", idx.Name, len(props))
		if current, changed := progress.AdvanceMetadata(); changed {
			message := fmt.Sprintf("resource metadata enriched: %d/%d", progress.metadataProcessed, progress.metadataTotal)
			if err := dtw.updateProgress(ctx, task.ID, current, message); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergeIndexFeatures preserves business features and refreshes native features from the source mapping.
func mergeIndexFeatures(existing, native []interfaces.PropertyFeature) []interfaces.PropertyFeature {
	features := make([]interfaces.PropertyFeature, 0, len(existing)+len(native))
	for _, feature := range existing {
		if !feature.IsNative {
			features = append(features, feature)
		}
	}
	features = append(features, native...)
	if len(features) == 0 {
		return nil
	}
	return features
}

// osSubFieldTypeToFeatureType maps supported OpenSearch multi-field types to VEGA feature types.
func osSubFieldTypeToFeatureType(osType string) string {
	switch osType {
	case "keyword", "constant_keyword":
		return interfaces.PropertyFeatureType_Keyword
	case "text", "match_only_text":
		return interfaces.PropertyFeatureType_Fulltext
	case "knn_vector":
		return interfaces.PropertyFeatureType_Vector
	default:
		return ""
	}
}

// buildSubFieldFeatures converts OpenSearch multi-fields to VEGA property features.
func buildSubFieldFeatures(parentName string, subFields []interfaces.IndexSubFieldMeta) []interfaces.PropertyFeature {
	if len(subFields) == 0 {
		return nil
	}
	features := make([]interfaces.PropertyFeature, 0, len(subFields))
	for _, sub := range subFields {
		featureType := osSubFieldTypeToFeatureType(sub.Type)
		if featureType == "" {
			logger.Warnf("Skip unsupported opensearch sub-field type: parent=%s sub=%s type=%s", parentName, sub.Name, sub.Type)
			continue
		}
		features = append(features, interfaces.PropertyFeature{
			FeatureName: sub.Name,
			DisplayName: sub.Name,
			FeatureType: featureType,
			IsNative:    true,
			Config:      sub.Attributes,
		})
	}
	if len(features) == 0 {
		return nil
	}
	return features
}

// enrichResourceIndexRowCount 采集并保存单个索引的数量。
func (dtw *DiscoverTaskWorker) enrichResourceIndexRowCount(ctx context.Context,
	task *interfaces.DiscoverTask, resource *interfaces.Resource, connector interfaces.IndexConnector,
	meta *interfaces.IndexMeta, result *interfaces.DiscoverResult) error {

	queryCtx, cancel := context.WithTimeout(ctx, resourceCountTimeout)
	count, err := connector.CountRows(queryCtx, meta)
	cancel()

	if err == nil && count < 0 {
		err = fmt.Errorf("invalid negative row count")
	}
	if err == nil {
		err = dtw.rs.InternalUpdateRowCount(ctx, nil, resource, count, time.Now().UnixMilli())
	}
	if err != nil {
		if errors.Is(err, interfaces.ErrRowCountUnavailable) && task.ResourceID == "" {
			result.SkippedCount++
		} else {
			result.FailedCount++
		}
		logger.Warnf("Resource exact count failed: resource_id=%s error=%v", resource.ID, err)
	} else {
		result.UpdatedCount++
	}
	return nil
}
