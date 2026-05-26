// Copyright 2026 kowell.ai
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

// filesetDiscoverItem represents a fileset discover item.
type filesetDiscoverItem struct {
	resource        *interfaces.Resource
	meta            *interfaces.FilesetMeta
	markAfterEnrich bool
}

// discoverFilesetResources discovers fileset resources from a fileset connector.
func (dh *DiscoverHandler) discoverFilesetResources(ctx context.Context, catalog *interfaces.Catalog,
	connector connectors.Connector, task *interfaces.DiscoverTask) (*interfaces.DiscoverResult, error) {

	filesetConnector, ok := connector.(connectors.FilesetConnector)
	if !ok {
		return nil, fmt.Errorf("connector does not support fileset discover")
	}

	sourceFilesets, err := filesetConnector.ListFilesets(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list filesets: %w", err)
	}
	logger.Infof("Discovered %d fileset objects from source", len(sourceFilesets))

	existingResources, err := dh.rs.GetByCatalogID(ctx, catalog.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing resources: %w", err)
	}

	result, items, err := dh.reconcileFilesetResources(ctx, catalog, sourceFilesets, existingResources, task.DiscoverActions)
	if err != nil {
		return nil, fmt.Errorf("failed to reconcile fileset resources: %w", err)
	}

	if err := dh.enrichFilesetMetadata(ctx, items, result); err != nil {
		return nil, fmt.Errorf("failed to enrich fileset metadata: %w", err)
	}

	result.Message = formatDiscoverResultMessage(result)
	logger.Info(result.Message)

	return result, nil
}

func (dh *DiscoverHandler) reconcileFilesetResources(ctx context.Context, catalog *interfaces.Catalog, source []*interfaces.FilesetMeta,
	existingResources []*interfaces.Resource, actions *interfaces.DiscoverActions) (*interfaces.DiscoverResult, []filesetDiscoverItem, error) {
	result := &interfaces.DiscoverResult{
		CatalogID: catalog.ID,
	}
	var items []filesetDiscoverItem

	existingMap := make(map[string]*interfaces.Resource)
	for _, r := range existingResources {
		if r.Category != interfaces.ResourceCategoryFileset {
			continue
		}
		existingMap[r.SourceIdentifier] = r
	}

	sourceMap := make(map[string]*interfaces.FilesetMeta)
	for _, fs := range source {
		sid := filesetSourceIdentifier(fs)
		sourceMap[sid] = fs
	}

	for _, fs := range source {
		sourceIdentifier := filesetSourceIdentifier(fs)
		if resource, ok := existingMap[sourceIdentifier]; ok {
			markAfterEnrich := true
			if actions != nil && actions.Refresh {
				if resource.Status == interfaces.ResourceStatusStale {
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
				items = append(items, filesetDiscoverItem{resource: resource, meta: fs, markAfterEnrich: markAfterEnrich})
			}
		} else {
			if actions != nil && actions.Create {
				resource, err := dh.createFilesetResource(ctx, catalog, fs, sourceIdentifier)
				if err != nil {
					logger.Errorf("Failed to create fileset resource %s: %v", sourceIdentifier, err)
				} else {
					dh.markDiscover(ctx, resource.ID, interfaces.DiscoverStatusNew)
					resource.LastDiscoverStatus = interfaces.DiscoverStatusNew
					result.NewCount++
					items = append(items, filesetDiscoverItem{resource: resource, meta: fs})
				}
			}
		}
	}

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

func filesetSourceIdentifier(fs *interfaces.FilesetMeta) string {
	if fs.DisplayPath != "" {
		return fs.DisplayPath
	}
	return fs.ID
}

func (dh *DiscoverHandler) createFilesetResource(ctx context.Context, catalog *interfaces.Catalog, fs *interfaces.FilesetMeta, sourceIdentifier string) (*interfaces.Resource, error) {
	meta := fs.SourceMetadata
	if meta == nil {
		meta = make(map[string]any)
	}
	meta["original_name"] = fs.Name
	meta["original_description"] = ""
	req := &interfaces.ResourceRequest{
		CatalogID:        catalog.ID,
		Name:             fs.Name,
		Category:         interfaces.ResourceCategoryFileset,
		Status:           interfaces.ResourceStatusActive,
		Database:         "",
		SourceIdentifier: sourceIdentifier,
		SourceMetadata:   meta,
	}
	resource, err := dh.rs.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	return resource, nil
}

func (dh *DiscoverHandler) enrichFilesetMetadata(ctx context.Context, items []filesetDiscoverItem, result *interfaces.DiscoverResult) error {
	for _, item := range items {
		fs := item.meta
		resource := item.resource
		beforeHash := sourceSnapshotHash(resource)

		sourceMetadata := resource.SourceMetadata
		if sourceMetadata == nil {
			sourceMetadata = make(map[string]any)
		}
		for k, v := range fs.SourceMetadata {
			sourceMetadata[k] = v
		}
		sourceMetadata["original_name"] = fs.Name
		sourceMetadata["original_description"] = ""
		sourceMetadata["columns"] = fs.Columns
		resource.SourceMetadata = sourceMetadata
		resource.SchemaDefinition = []*interfaces.Property{}
		for _, col := range fs.Columns {
			resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{
				Name:         col.Name,
				Type:         col.Type,
				OriginalType: col.Type,
				DisplayName:  col.Name,
				OriginalName: col.Name,
				Description:  "",
			})
		}

		discoverStatus := resource.LastDiscoverStatus
		if item.markAfterEnrich {
			discoverStatus = discoverStatusAfterEnrich(resource, beforeHash)
			updateDiscoverResultForEnrichStatus(result, discoverStatus)
		}

		resource.LastDiscoverStatus = discoverStatus
		if err := dh.rs.UpdateResource(ctx, resource); err != nil {
			logger.Errorf("Failed to update fileset resource %s: %v", resource.ID, err)
			return err
		}
		logger.Infof("Enriched fileset resource %s (%s)", resource.Name, fs.ID)
	}
	return nil
}
