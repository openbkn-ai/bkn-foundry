// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/interfaces"
)

// indexDiscoverItem represents an index discover item.
type indexDiscoverItem struct {
	resource        *interfaces.Resource
	indexMeta       *interfaces.IndexMeta
	markAfterEnrich bool
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

	existingResources, err := dtw.rs.GetByCatalogID(ctx, catalog.ID)
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
		sourceIdentifier := idx.Name

		if resource, ok := existingMap[sourceIdentifier]; ok {
			if actions != nil && actions.Refresh {
				markAfterEnrich := true
				if resource.Status == interfaces.ResourceStatusStale {
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
		Name:             index.Name,
		Description:      index.Description,
		Category:         interfaces.ResourceCategoryIndex,
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

	for _, item := range items {
		idx := item.indexMeta
		resource := item.resource
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

		existingProperties := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
		for _, property := range resource.SchemaDefinition {
			if property != nil {
				existingProperties[property.Name] = property
			}
		}

		var props []*interfaces.Property
		for _, field := range idx.Mapping {
			delete(field.Attributes, "type")

			property := &interfaces.Property{
				Name:        field.Name,
				DisplayName: field.Name,
				Type:        indexConnector.MapType(field.Type),
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

		sourceMetadata := make(map[string]any)
		if resource.SourceMetadata != nil {
			sourceMetadata = resource.SourceMetadata
		}
		sourceMetadata["original_name"] = idx.Name
		sourceMetadata["original_description"] = idx.Description
		sourceMetadata["properties"] = idx.Properties
		sourceMetadata["mapping"] = idx.Mapping
		sourceMetadata["mapping_meta"] = idx.MappingMeta
		resource.SourceMetadata = sourceMetadata

		discoverStatus := resource.LastDiscoverStatus
		if item.markAfterEnrich {
			discoverStatus = discoverStatusAfterEnrich(resource, beforeHash)
			updateDiscoverResultForEnrichStatus(result, discoverStatus)
		}

		resource.LastDiscoverStatus = discoverStatus
		resource.StatusMessage = ""
		expectedUpdateTime := resource.UpdateTime
		resource.Updater = task.Creator
		resource.UpdateTime = time.Now().UnixMilli()
		if err := dtw.rs.InternalUpdateDiscoveryMetadata(ctx, nil, resource, expectedUpdateTime); err != nil {
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
	case "keyword":
		return interfaces.PropertyFeatureType_Keyword
	case "text":
		return interfaces.PropertyFeatureType_Fulltext
	case "dense_vector", "knn_vector":
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
		fullName := parentName + "." + sub.Name
		features = append(features, interfaces.PropertyFeature{
			FeatureName: fullName,
			DisplayName: fullName,
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
