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

// tableDiscoverItem represents a table discover item.
type tableDiscoverItem struct {
	resource        *interfaces.Resource
	tableMeta       *interfaces.TableMeta
	markAfterEnrich bool
}

// discoverTableResources discovers table resources from a table connector.
// Step-by-step execution: 1. Obtain the list of table names 2. Create/update Resource 3. Complete the detailed metadata one by one
func (dtw *DiscoverTaskWorker) discoverTableResources(ctx context.Context,
	task *interfaces.DiscoverTask, catalog *interfaces.Catalog, connector interfaces.Connector,
	progress *discoverTaskReconcileProgress) (*interfaces.DiscoverResult, error) {

	tableConnector, ok := connector.(interfaces.TableConnector)
	if !ok {
		return nil, fmt.Errorf("connector does not support table discover")
	}

	// Step 1: Obtain the list of names
	sourceTables, err := tableConnector.ListTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	if current, changed := progress.MarkSourceListed(); changed {
		if err := dtw.updateProgress(ctx, task.ID, current, "source tables listed"); err != nil {
			return nil, err
		}
	}
	logger.Infof("Discovered %d tables from source", len(sourceTables))

	// Step 2: Obtain the existing Resources
	existingResources, err := dtw.rs.GetByCatalogID(ctx, catalog.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing resources: %w", err)
	}
	logger.Infof("Loaded %d existing resources for table discovery", len(existingResources))

	// Step 3: Compare and create/update Resources (basic information)
	result, items, err := dtw.reconcileTableResources(ctx, task, catalog, sourceTables, existingResources)
	if err != nil {
		return nil, fmt.Errorf("failed to reconcile resources: %w", err)
	}
	if current, changed := progress.MarkResourcesReconciled(); changed {
		if err := dtw.updateProgress(ctx, task.ID, current, "resources reconciled"); err != nil {
			return nil, err
		}
	}
	logger.Infof("Reconciled %d table resources", len(items))

	// Step 4: Complete the detailed metadata one by one: Metadata collection is to supplement the metadata information of each table
	if err := dtw.enrichTableMetadata(ctx, task, tableConnector, items, result, progress); err != nil {
		return nil, fmt.Errorf("failed to enrich table metadata: %w", err)
	}
	if current, changed := progress.MarkMetadataEnriched(); changed {
		if err := dtw.updateProgress(ctx, task.ID, current, "resource metadata enriched"); err != nil {
			return nil, err
		}
	}
	logger.Infof("Enriched metadata for %d table resources", len(items))

	result.Message = formatDiscoverResultMessage(result)
	logger.Info(result.Message)

	return result, nil
}

// reconcileTableResources reconciles source tables with existing resources.
func (dtw *DiscoverTaskWorker) reconcileTableResources(ctx context.Context,
	task *interfaces.DiscoverTask, catalog *interfaces.Catalog, sourceTables []*interfaces.TableMeta,
	existingResources []*interfaces.Resource) (*interfaces.DiscoverResult, []tableDiscoverItem, error) {

	actions := task.DiscoverActions

	result := &interfaces.DiscoverResult{
		CatalogID: catalog.ID,
	}

	// Used for returning Discover Items
	var items []tableDiscoverItem

	// Build a map of the existing resources (indexed by SourceIdentifier)
	existingMap := make(map[string]*interfaces.Resource)
	for _, r := range existingResources {
		if r.Category != interfaces.ResourceCategoryTable {
			continue
		}
		existingMap[r.SourceIdentifier] = r
	}

	// Build the map of the source table
	sourceMap := make(map[string]*interfaces.TableMeta)
	for _, t := range sourceTables {
		sourceIdentifier := dtw.buildSourceIdentifier(t)
		sourceMap[sourceIdentifier] = t
	}

	// Handle newly added and retained resources
	for _, table := range sourceTables {
		sourceIdentifier := dtw.buildSourceIdentifier(table)

		if resource, ok := existingMap[sourceIdentifier]; ok {
			// Existing. Check the status
			if actions != nil && actions.Refresh {
				markAfterEnrich := true
				if resource.Status == interfaces.ResourceStatusStale {
					// Previously marked as stale, now reactivated
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
				items = append(items, tableDiscoverItem{
					resource:        resource,
					tableMeta:       table,
					markAfterEnrich: markAfterEnrich,
				})
			}
		} else {
			// New resources - Only processed when the policy allows create
			if actions != nil && actions.Create {
				resource, err := dtw.createTableResource(ctx, catalog, table, sourceIdentifier)
				if err != nil {
					logger.Errorf("Failed to create resource %s: %v", sourceIdentifier, err)
				} else {
					dtw.markDiscover(ctx, resource.ID, interfaces.DiscoverStatusNew)
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

	// Handle deleted resources (marked as stale) - only handle when the policy allows mark_stale
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

// createTableResource creates a new resource.
func (dtw *DiscoverTaskWorker) createTableResource(ctx context.Context, catalog *interfaces.Catalog,
	table *interfaces.TableMeta, sourceIdentifier string) (*interfaces.Resource, error) {

	req := &interfaces.ResourceRequest{
		CatalogID:        catalog.ID,
		Name:             sourceIdentifier,
		Description:      table.Description,
		Category:         interfaces.ResourceCategoryTable,
		Status:           interfaces.ResourceStatusActive,
		Schema:           table.Schema,
		SourceIdentifier: sourceIdentifier,
		SourceMetadata: map[string]any{
			"original_name":        sourceIdentifier,
			"original_description": table.Description,
		},
	}
	resource, err := dtw.rs.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	return resource, nil
}

// add details to the table metadata
// Parameter
//
//	-ctx: Context information, used to control the timeout and cancellation of requests
//	-tableConnector: A table connector used to obtain the metadata of a table
//	-items: List of table discovery items, including table metadata and resource information
//
// Return value:
//
//	-error: If an error occurs during processing, return an error message
func (dtw *DiscoverTaskWorker) enrichTableMetadata(ctx context.Context, task *interfaces.DiscoverTask,
	tableConnector interfaces.TableConnector, items []tableDiscoverItem, result *interfaces.DiscoverResult,
	progress *discoverTaskReconcileProgress) error {

	progress.SetMetadataTotal(len(items))

	// Traverse all tables to discover items
	for _, item := range items {
		table := item.tableMeta   // Obtain the table metadata
		resource := item.resource // Obtain resource information
		beforeHash := sourceSnapshotHash(resource)

		// Obtain detailed metadata
		if err := tableConnector.GetTableMeta(ctx, table); err != nil {
			logger.Warnf("Failed to get metadata for table %s: %v", table.Name, err)
			resource.LastDiscoverStatus = interfaces.DiscoverStatusError
			resource.StatusMessage = fmt.Sprintf("discover metadata failed: %v", err)
			updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusError)
			expectedUpdateTime := resource.UpdateTime
			resource.Updater = task.Creator
			resource.UpdateTime = time.Now().UnixMilli()
			if updateErr := dtw.rs.InternalUpdateDiscoveryMetadata(ctx, nil, resource, expectedUpdateTime); updateErr != nil {
				logger.Errorf("Failed to update discover error for table %s: %v", table.Name, updateErr)
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

		// Fill in the Resource metadata: schema_definition field
		resource.Schema = table.Schema
		existingProperties := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
		for _, property := range resource.SchemaDefinition {
			if property != nil {
				existingProperties[property.Name] = property
			}
		}

		var props []*interfaces.Property
		for _, column := range table.Columns {
			property := &interfaces.Property{
				Name:        column.Name,
				DisplayName: column.Name,
				Type:        tableConnector.MapType(column.Type),
				Description: column.Description,

				OriginalName:        column.Name,
				OriginalType:        column.Type,
				OriginalDescription: column.Description,
			}
			if existing, ok := existingProperties[column.Name]; ok {
				property.DisplayName = existing.DisplayName
				property.Description = resolveSourceDescription(existing.Description, existing.OriginalDescription, column.Description)
				property.Features = existing.Features
			}
			props = append(props, property)
		}
		resource.SchemaDefinition = props

		resource.Description = resolveSourceDescription(resource.Description, sourceOriginalDescription(resource.SourceMetadata), table.Description)

		// Fill in the Resource metadata: source_metadata field
		sourceMetadata := make(map[string]any)
		if resource.SourceMetadata != nil {
			sourceMetadata = resource.SourceMetadata
		}
		sourceMetadata["original_name"] = resource.SourceIdentifier
		sourceMetadata["original_description"] = table.Description
		sourceMetadata["columns"] = table.Columns
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

		// Update Resource
		resource.LastDiscoverStatus = discoverStatus
		resource.StatusMessage = ""
		expectedUpdateTime := resource.UpdateTime
		resource.Updater = task.Creator
		resource.UpdateTime = time.Now().UnixMilli()
		if err := dtw.rs.InternalUpdateDiscoveryMetadata(ctx, nil, resource, expectedUpdateTime); err != nil {
			logger.Errorf("Failed to update metadata for table %s: %v", table.Name, err)
			return err
		}

		logger.Debugf("Enriched table %s: properties=%v, columns=%d, indices=%d, foreign_keys=%d", table.Name, table.Properties, len(table.Columns), len(table.Indices), len(table.ForeignKeys))
		if current, changed := progress.AdvanceMetadata(); changed {
			message := fmt.Sprintf("resource metadata enriched: %d/%d", progress.metadataProcessed, progress.metadataTotal)
			if err := dtw.updateProgress(ctx, task.ID, current, message); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildSourceIdentifier builds the source identifier for a table.
func (dtw *DiscoverTaskWorker) buildSourceIdentifier(table *interfaces.TableMeta) string {
	identifier := table.Name
	if table.Schema != "" {
		return fmt.Sprintf("%s.%s", table.Schema, identifier)
	}
	if table.Database != "" {
		identifier = fmt.Sprintf("%s.%s", table.Database, identifier)
	}
	return identifier
}

// resolveSourceDescription keeps a business description unless it is absent or still matches the previous source description.
func resolveSourceDescription(description, originalDescription, discoveredDescription string) string {
	if description == "" || description == originalDescription {
		return discoveredDescription
	}
	return description
}
