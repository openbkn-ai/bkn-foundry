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

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// filesetDiscoverItem represents a fileset discover item.
type filesetDiscoverItem struct {
	resource        *interfaces.Resource
	meta            *interfaces.FilesetMeta
	markAfterEnrich bool
}

// Page size and page cap for connectors that implement PagedFilesetLister.
const (
	filesetListPageSize = 1000
	filesetListMaxPages = 10000
)

// discoverFilesetResources discovers fileset resources from a fileset connector.
func (dtw *DiscoverTaskWorker) discoverFilesetResources(ctx context.Context,
	task *interfaces.DiscoverTask, catalog *interfaces.Catalog, connector interfaces.Connector,
	progress *discoverTaskReconcileProgress) (*interfaces.DiscoverResult, error) {

	filesetConnector, ok := connector.(interfaces.FilesetConnector)
	if !ok {
		return nil, fmt.Errorf("connector does not support fileset discover")
	}

	// The full listing must succeed before any resource is touched, so a failed page never marks resources stale.
	sourceFilesets, err := dtw.listSourceFilesets(ctx, task, filesetConnector, progress)
	if err != nil {
		return nil, fmt.Errorf("failed to list filesets: %w", err)
	}
	if current, changed := progress.MarkSourceListed(); changed {
		if err := dtw.updateProgress(ctx, task.ID, current, "source filesets listed"); err != nil {
			return nil, err
		}
	}
	logger.Infof("Discovered %d fileset objects from source", len(sourceFilesets))

	existingResources, err := dtw.rs.InternalGetByCatalogID(ctx, catalog.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing resources: %w", err)
	}
	logger.Infof("Loaded %d existing resources for fileset discovery", len(existingResources))

	result, items, err := dtw.reconcileFilesetResources(ctx, task, catalog, sourceFilesets, existingResources)
	if err != nil {
		return nil, fmt.Errorf("failed to reconcile fileset resources: %w", err)
	}
	if current, changed := progress.MarkResourcesReconciled(); changed {
		if err := dtw.updateProgress(ctx, task.ID, current, "resources reconciled"); err != nil {
			return nil, err
		}
	}
	logger.Infof("Reconciled %d fileset resources", len(items))

	if err := dtw.enrichFilesetMetadata(ctx, task, items, result, progress); err != nil {
		return nil, fmt.Errorf("failed to enrich fileset metadata: %w", err)
	}
	if current, changed := progress.MarkMetadataEnriched(); changed {
		if err := dtw.updateProgress(ctx, task.ID, current, "resource metadata enriched"); err != nil {
			return nil, err
		}
	}
	logger.Infof("Enriched metadata for %d fileset resources", len(items))

	result.Message = formatDiscoverResultMessage(result)
	logger.Info(result.Message)

	return result, nil
}

// listSourceFilesets lists every fileset in the discovery scope, page by page when the connector
// implements PagedFilesetLister and in a single ListFilesets call otherwise.
func (dtw *DiscoverTaskWorker) listSourceFilesets(ctx context.Context, task *interfaces.DiscoverTask,
	connector interfaces.FilesetConnector, progress *discoverTaskReconcileProgress) ([]*interfaces.FilesetMeta, error) {

	lister, ok := connector.(interfaces.PagedFilesetLister)
	if !ok {
		return connector.ListFilesets(ctx)
	}

	var filesets []*interfaces.FilesetMeta
	seenCursors := make(map[string]struct{})
	cursor := ""
	for page := 1; ; page++ {
		if page > filesetListMaxPages {
			return nil, fmt.Errorf("fileset listing exceeded %d pages", filesetListMaxPages)
		}
		if dtw.stopped.Load() {
			return nil, ErrWorkerManagerStopping
		}
		result, err := lister.ListFilesetsPage(ctx, cursor, filesetListPageSize)
		if err != nil {
			return nil, fmt.Errorf("list fileset page %d: %w", page, err)
		}
		if result == nil {
			return nil, fmt.Errorf("list fileset page %d: connector returned no page", page)
		}
		filesets = append(filesets, result.Items...)
		// Per-page updates also refresh the task heartbeat during long listings.
		if err := dtw.updateProgress(ctx, task.ID, progress.lastProgress, fmt.Sprintf("source filesets listed: page %d, %d objects", page, len(filesets))); err != nil {
			return nil, err
		}

		if result.NextCursor == "" {
			return filesets, nil
		}
		if _, repeated := seenCursors[result.NextCursor]; repeated || result.NextCursor == cursor {
			return nil, fmt.Errorf("list fileset page %d: cursor %q repeated", page, result.NextCursor)
		}
		seenCursors[result.NextCursor] = struct{}{}
		cursor = result.NextCursor
	}
}

func (dtw *DiscoverTaskWorker) reconcileFilesetResources(ctx context.Context, task *interfaces.DiscoverTask,
	catalog *interfaces.Catalog, source []*interfaces.FilesetMeta,
	existingResources []*interfaces.Resource) (*interfaces.DiscoverResult, []filesetDiscoverItem, error) {

	actions := task.DiscoverActions
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
		if dtw.stopped.Load() {
			return nil, nil, ErrWorkerManagerStopping
		}
		sourceIdentifier := filesetSourceIdentifier(fs)
		if resource, ok := existingMap[sourceIdentifier]; ok {
			markAfterEnrich := true
			if actions != nil && actions.Refresh {
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
				items = append(items, filesetDiscoverItem{resource: resource, meta: fs, markAfterEnrich: markAfterEnrich})
			}
		} else {
			if actions != nil && actions.Create {
				resource, err := dtw.createFilesetResource(ctx, catalog, fs, sourceIdentifier)
				if err != nil {
					logger.Errorf("Failed to create fileset resource %s: %v", sourceIdentifier, err)
				} else {
					dtw.markDiscover(ctx, resource.ID, interfaces.DiscoverStatusNew)
					resource.LastDiscoverStatus = interfaces.DiscoverStatusNew
					result.NewCount++
					items = append(items, filesetDiscoverItem{resource: resource, meta: fs})
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

func filesetSourceIdentifier(fs *interfaces.FilesetMeta) string {
	if fs.DisplayPath != "" {
		return fs.DisplayPath
	}
	return fs.ID
}

func (dtw *DiscoverTaskWorker) createFilesetResource(ctx context.Context, catalog *interfaces.Catalog, fs *interfaces.FilesetMeta, sourceIdentifier string) (*interfaces.Resource, error) {
	meta := fs.SourceMetadata
	if meta == nil {
		meta = make(map[string]any)
	}
	meta["original_name"] = fs.Name
	meta["original_description"] = ""
	req := &interfaces.ResourceRequest{
		CatalogID:        catalog.ID,
		Builtin:          &catalog.Builtin,
		Name:             fs.Name,
		Category:         interfaces.ResourceCategoryFileset,
		Enabled:          true,
		Status:           interfaces.ResourceStatusActive,
		Schema:           "",
		SourceIdentifier: sourceIdentifier,
		SourceMetadata:   meta,
	}
	resource, err := dtw.rs.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	return resource, nil
}

func (dtw *DiscoverTaskWorker) enrichFilesetMetadata(ctx context.Context, task *interfaces.DiscoverTask,
	items []filesetDiscoverItem, result *interfaces.DiscoverResult, progress *discoverTaskReconcileProgress) error {
	progress.SetMetadataTotal(len(items))

	for _, item := range items {
		if dtw.stopped.Load() {
			return ErrWorkerManagerStopping
		}
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

		observedAt := time.Now().UnixMilli()
		resource.LastDiscoverStatus = discoverStatus
		resource.LastDiscoverTime = observedAt
		expectedUpdateTime := resource.UpdateTime
		resource.Updater = task.Creator
		resource.UpdateTime = observedAt
		if err := dtw.rs.InternalUpdateDiscoveryMetadata(ctx, nil, resource, expectedUpdateTime); err != nil {
			logger.Errorf("Failed to update fileset resource %s: %v", resource.ID, err)
			return err
		}
		logger.Infof("Enriched fileset resource %s (%s)", resource.Name, fs.ID)
		if current, changed := progress.AdvanceMetadata(); changed {
			message := fmt.Sprintf("resource metadata enriched: %d/%d", progress.metadataProcessed, progress.metadataTotal)
			if err := dtw.updateProgress(ctx, task.ID, current, message); err != nil {
				return err
			}
		}
	}
	return nil
}
