// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/interfaces"
)

// indexDiscoverItem represents an index discover item.
type indexDiscoverItem struct {
	resource        *interfaces.Resource
	indexMeta       *interfaces.IndexMeta
	markAfterEnrich bool
}

// discoverIndexResources discovers index resources from an index connector.
// discoverIndexResources obtains and discovers indexes from the connector.
// Then coordinate with the existing resources and finally enrich the metadata information of the index
// Parameter
//
//	-ctx: Context information, used to control the timeout and cancellation of requests
//	-catalog: Catalog interface, containing relevant information about the catalog
//	- connector: Connector interface, used for interacting with data sources
//
// Return value:
//   - * interfaces. DiscoverResult: findings, including a new resource, outdated and not change resources statistics
//     -error: Error message, returned if an error occurs during the discovery process
func (dtw *DiscoverTaskWorker) discoverIndexResources(ctx context.Context,
	task *interfaces.DiscoverTask, catalog *interfaces.Catalog, connector interfaces.Connector,
	progress *discoverTaskReconcileProgress) (*interfaces.DiscoverResult, error) {

	// Check whether the connector implements the IndexConnector interface
	indexConnector, ok := connector.(interfaces.IndexConnector)
	if !ok {
		return nil, fmt.Errorf("connector does not support index discover")
	}

	// Step 1: List Indices: Obtain all indices
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

	// Step 2: Get Existing Resources: Find out if the db already exists and then do the comparison
	existingResources, err := dtw.rs.GetByCatalogID(ctx, catalog.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing resources: %w", err)
	}
	logger.Infof("Loaded %d existing resources for index discovery", len(existingResources))

	// Step 3: Reconcile: get the index data and insert it:
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

	// Step 4: Enrich: Enrich the metadata information for index entries
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

// reconcileIndexResources reconciles source indices with existing resources.
// reconcileIndexResources coordinates index resources and handles new resources, existing resources, and expired resources
// Parameter
//
//	-ctx: Context information, used to control the timeout and cancellation of requests
//	-catalog: Directory information, including metadata such as ID
//	-sourceIndices: List of source index metadata
//	-Existing Resources: A list of existingResources
//
// Return value:
//   - * interfaces. DiscoverResult: findings, including the directory ID and the statistics of all kinds of resources
//   - []indexDiscoverItem: A list of indexDiscoveritems, including resources and index metadata
//     -error: Error message, returned if an error occurs during processing
func (dtw *DiscoverTaskWorker) reconcileIndexResources(ctx context.Context, task *interfaces.DiscoverTask,
	catalog *interfaces.Catalog, sourceIndices []*interfaces.IndexMeta,
	existingResources []*interfaces.Resource) (*interfaces.DiscoverResult, []indexDiscoverItem, error) {

	actions := task.DiscoverActions

	// Initialize the discovery results and set the directory ID
	result := &interfaces.DiscoverResult{
		CatalogID: catalog.ID,
	}

	var items []indexDiscoverItem // Index to discover the list of items

	// Create a mapping of an existing resource with the source identifier as the key
	existingMap := make(map[string]*interfaces.Resource)
	for _, r := range existingResources {
		if r.Category != interfaces.ResourceCategoryIndex {
			continue
		}
		existingMap[r.SourceIdentifier] = r
	}
	// Create a source index mapping and name the index as the key
	sourceMap := make(map[string]*interfaces.IndexMeta)
	for _, idx := range sourceIndices {
		sourceMap[idx.Name] = idx
	}

	// Handle new and existing
	for _, idx := range sourceIndices {
		sourceIdentifier := idx.Name //test-index

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

	// Handle stale
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
func (dtw *DiscoverTaskWorker) createIndexResource(ctx context.Context, catalog *interfaces.Catalog, index *interfaces.IndexMeta) (*interfaces.Resource, error) {

	req := &interfaces.ResourceRequest{
		CatalogID:        catalog.ID,
		Name:             index.Name,
		Category:         interfaces.ResourceCategoryIndex,
		Status:           interfaces.ResourceStatusActive,
		SourceIdentifier: index.Name,
		SourceMetadata: map[string]any{
			"original_name":        index.Name,
			"original_description": "",
		},
	}
	resource, err := dtw.rs.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	return resource, nil
}

// enrichIndexMetadata enriches index resources with detailed metadata.
// enriches the metadata information for index entries
// Parameter
//
//	-ctx: Context information, used to control the timeout and cancellation of requests
//	-indexConnector: Index connector, used to obtain the metadata of the index
//	-items: List of index items that require rich metadata
//
// Return value:
//
//	-error: If an error occurs during processing, return an error message
func (dtw *DiscoverTaskWorker) enrichIndexMetadata(ctx context.Context, task *interfaces.DiscoverTask,
	indexConnector interfaces.IndexConnector, items []indexDiscoverItem, result *interfaces.DiscoverResult,
	progress *discoverTaskReconcileProgress) error {
	progress.SetMetadataTotal(len(items))

	// Traverse all the index entries that need to be processed
	for _, item := range items {
		idx := item.indexMeta
		resource := item.resource
		beforeHash := sourceSnapshotHash(resource)

		// Get detailed metadata (mappings) : Obtain detailed information about the index
		if err := indexConnector.GetIndexMeta(ctx, idx); err != nil {
			logger.Warnf("Failed to get metadata for index %s: %v", idx.Name, err)
			return err
		}

		// Map fields to SchemaDefinition
		var columns []*interfaces.Property
		for _, field := range idx.Mapping {
			// For {"ignore_above":256,"type":"keyword"}, remove type.
			delete(field.Attributes, "type")

			columns = append(columns, &interfaces.Property{
				Name:        field.Name,
				DisplayName: field.Name,
				Type:        field.Type,
				Description: "",

				OriginalName:        field.Name,
				OriginalType:        field.Type,
				OriginalDescription: "",
				Attributes:          field.Attributes,
				Features:            buildSubFieldFeatures(field.Name, field.SubFields),
			})
		}
		resource.SchemaDefinition = columns

		// Populate SourceMetadata
		sourceMetadata := make(map[string]any)
		if resource.SourceMetadata != nil {
			sourceMetadata = resource.SourceMetadata
		}

		sourceMetadata["properties"] = idx.Properties
		sourceMetadata["mapping"] = idx.Mapping
		sourceMetadata["original_name"] = idx.Name
		sourceMetadata["original_description"] = ""
		resource.SourceMetadata = sourceMetadata

		discoverStatus := resource.LastDiscoverStatus
		if item.markAfterEnrich {
			discoverStatus = discoverStatusAfterEnrich(resource, beforeHash)
			updateDiscoverResultForEnrichStatus(result, discoverStatus)
		}

		// Update Resource
		resource.LastDiscoverStatus = discoverStatus
		if err := dtw.rs.UpdateResource(ctx, resource); err != nil {
			logger.Errorf("Failed to update metadata for index %s: %v", idx.Name, err)
			return err
		}

		// Wait a bit to avoid overwhelming the server? No, it's fine for now.
		// Just logging
		logger.Infof("Enriched index %s: fields=%d", idx.Name, len(columns))
		if current, changed := progress.AdvanceMetadata(); changed {
			message := fmt.Sprintf("resource metadata enriched: %d/%d", progress.metadataProcessed, progress.metadataTotal)
			if err := dtw.updateProgress(ctx, task.ID, current, message); err != nil {
				return err
			}
		}
	}
	return nil
}

// osSubFieldTypeToFeatureType maps OpenSearch multi-field type to VEGA PropertyFeature type.
// If no other type is recognized and an empty string is returned, the caller should skip it and mark it as warn.
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

// buildSubFieldFeatures converts OpenSearch multi-field subfields to VEGA PropertyFeature.
// parentName: The full name of the parent field (such as "user.name"); subFields: Metadata of subfields that have been arranged in alphabetical order by Name.
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
