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

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// filesetDiscoverItem represents a fileset discover item.
type filesetDiscoverItem struct {
	resource        *interfaces.Resource
	meta            *interfaces.FilesetMeta
	markAfterEnrich bool
}

// Defaults and bounds for paged fileset discovery.
const (
	defaultFilesetPageSize      = 20
	maxFilesetPageSize          = 1000
	defaultFilesetMaxObjects    = 10_000_000
	filesetPageMaxAttempts      = 3
	filesetPageRetryBaseBackoff = time.Second
)

// discoverFilesetResources discovers fileset resources from a fileset connector.
func (dtw *DiscoverTaskWorker) discoverFilesetResources(ctx context.Context,
	task *interfaces.DiscoverTask, catalog *interfaces.Catalog, connector interfaces.Connector,
	progress *discoverTaskReconcileProgress) (*interfaces.DiscoverResult, error) {

	filesetConnector, ok := connector.(interfaces.FilesetConnector)
	if !ok {
		return nil, fmt.Errorf("connector does not support fileset discover")
	}
	if lister, ok := connector.(interfaces.PagedFilesetLister); ok {
		return dtw.discoverPagedFilesetResources(ctx, task, catalog, lister, progress)
	}

	sourceFilesets, err := filesetConnector.ListFilesets(ctx)
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

// discoverPagedFilesetResources applies each page as soon as it is listed, so memory holds one page plus the
// catalog's lightweight source refs instead of every fileset and every resource row. Missing resources are
// marked stale only after every page has been listed and applied; any failure returns before stale marking,
// keeping the pages already applied.
func (dtw *DiscoverTaskWorker) discoverPagedFilesetResources(ctx context.Context,
	task *interfaces.DiscoverTask, catalog *interfaces.Catalog, lister interfaces.PagedFilesetLister,
	progress *discoverTaskReconcileProgress) (*interfaces.DiscoverResult, error) {

	refs, err := dtw.rs.InternalGetSourceRefsByCatalogID(ctx, catalog.ID, interfaces.ResourceCategoryFileset)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing resource refs: %w", err)
	}
	refBySource := make(map[string]interfaces.ResourceSourceRef, len(refs))
	for _, ref := range refs {
		refBySource[ref.SourceIdentifier] = ref
	}
	logger.Infof("Loaded %d existing resource refs for paged fileset discovery", len(refBySource))

	pageSize, maxObjects := dtw.filesetPageSize(), dtw.filesetMaxObjects()
	result := &interfaces.DiscoverResult{CatalogID: catalog.ID}
	seen := make(map[string]struct{}, len(refBySource))
	seenCursors := make(map[string]struct{})
	cursor := ""
	listed := 0
	for page := 1; ; page++ {
		if dtw.stopped.Load() {
			return nil, ErrWorkerManagerStopping
		}
		fsPage, err := dtw.listFilesetPage(ctx, lister, page, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		listed += len(fsPage.Items)
		if listed > maxObjects {
			return nil, fmt.Errorf("fileset listing exceeded the safety limit of %d objects at page %d (cursor %q)", maxObjects, page, cursor)
		}
		if err := dtw.applyFilesetPage(ctx, task, catalog, fsPage.Items, refBySource, seen, result); err != nil {
			return nil, fmt.Errorf("failed to apply fileset page %d (cursor %q): %w", page, cursor, err)
		}
		// Per-page updates show real progress and refresh the task heartbeat; they fail once the task stops running.
		message := fmt.Sprintf("filesets processed: page %d, %d objects (%d new, %d updated, %d unchanged, %d restored)",
			page, listed, result.NewCount, result.UpdatedCount, result.UnchangedCount, result.RestoredCount)
		if err := dtw.updateProgress(ctx, task.ID, progress.lastProgress, message); err != nil {
			return nil, fmt.Errorf("stop fileset discovery after page %d: %w", page, err)
		}

		if fsPage.NextCursor == "" {
			break
		}
		if _, repeated := seenCursors[fsPage.NextCursor]; repeated || fsPage.NextCursor == cursor {
			return nil, fmt.Errorf("list fileset page %d: cursor %q repeated", page, fsPage.NextCursor)
		}
		seenCursors[fsPage.NextCursor] = struct{}{}
		cursor = fsPage.NextCursor
	}
	logger.Infof("Listed and applied %d fileset objects", listed)

	if actions := task.DiscoverActions; actions != nil && actions.MarkStale {
		for sourceIdentifier, ref := range refBySource {
			if _, ok := seen[sourceIdentifier]; ok {
				continue
			}
			if dtw.stopped.Load() {
				return nil, ErrWorkerManagerStopping
			}
			dtw.markFilesetMissing(ctx, ref.ID, ref.Status, result)
		}
	}

	progress.MarkSourceListed()
	progress.MarkResourcesReconciled()
	if current, changed := progress.MarkMetadataEnriched(); changed {
		if err := dtw.updateProgress(ctx, task.ID, current, "resources reconciled and metadata enriched"); err != nil {
			return nil, err
		}
	}

	result.Message = formatDiscoverResultMessage(result)
	logger.Info(result.Message)
	return result, nil
}

// listFilesetPage lists one page, retrying transient failures with exponential backoff.
func (dtw *DiscoverTaskWorker) listFilesetPage(ctx context.Context, lister interfaces.PagedFilesetLister,
	page int, cursor string, limit int) (*interfaces.FilesetPage, error) {

	for attempt := 1; ; attempt++ {
		fsPage, err := lister.ListFilesetsPage(ctx, cursor, limit)
		if err == nil {
			if fsPage == nil {
				return nil, fmt.Errorf("list fileset page %d (cursor %q): connector returned no page", page, cursor)
			}
			return fsPage, nil
		}
		if attempt >= filesetPageMaxAttempts || !isRetryableFilesetListError(ctx, err) {
			return nil, fmt.Errorf("list fileset page %d (cursor %q) failed after %d attempt(s): %w", page, cursor, attempt, err)
		}
		backoff := filesetPageRetryBaseBackoff << (attempt - 1)
		logger.Warnf("List fileset page %d (cursor %q) failed on attempt %d, retrying in %s: %v", page, cursor, attempt, backoff, err)
		if err := dtw.waitFilesetPageRetry(ctx, backoff); err != nil {
			return nil, fmt.Errorf("list fileset page %d (cursor %q) retry interrupted: %w", page, cursor, err)
		}
	}
}

// isRetryableFilesetListError reports whether a listing error may succeed on retry.
func isRetryableFilesetListError(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	var forbidden *interfaces.SourceReadForbiddenError
	var invalid *interfaces.SourceQueryInvalidParameterError
	var unsupported *interfaces.UnsupportedOperationError
	return !errors.As(err, &forbidden) && !errors.As(err, &invalid) && !errors.As(err, &unsupported)
}

// waitFilesetPageRetry waits for backoff unless ctx ends first.
func (dtw *DiscoverTaskWorker) waitFilesetPageRetry(ctx context.Context, backoff time.Duration) error {
	if dtw.filesetRetryWait != nil {
		return dtw.filesetRetryWait(ctx, backoff)
	}
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// applyFilesetPage creates, restores and enriches the resources of one page and records what it saw.
func (dtw *DiscoverTaskWorker) applyFilesetPage(ctx context.Context, task *interfaces.DiscoverTask,
	catalog *interfaces.Catalog, page []*interfaces.FilesetMeta, refBySource map[string]interfaces.ResourceSourceRef,
	seen map[string]struct{}, result *interfaces.DiscoverResult) error {

	var ids []string
	for _, fs := range page {
		if ref, ok := refBySource[filesetSourceIdentifier(fs)]; ok {
			ids = append(ids, ref.ID)
		}
	}
	existingMap := make(map[string]*interfaces.Resource, len(ids))
	if len(ids) > 0 {
		byID, err := dtw.rs.InternalGetByIDs(ctx, ids)
		if err != nil {
			return fmt.Errorf("failed to get existing resources: %w", err)
		}
		for _, resource := range byID {
			existingMap[resource.SourceIdentifier] = resource
		}
	}

	items, err := dtw.upsertFilesetResources(ctx, task.DiscoverActions, catalog, page, existingMap, result)
	if err != nil {
		return err
	}
	for _, fs := range page {
		seen[filesetSourceIdentifier(fs)] = struct{}{}
	}
	// A resource created on this page must be matched, not created again, if a later page repeats its identifier.
	for _, item := range items {
		if _, ok := refBySource[item.resource.SourceIdentifier]; !ok {
			refBySource[item.resource.SourceIdentifier] = interfaces.ResourceSourceRef{
				ID: item.resource.ID, SourceIdentifier: item.resource.SourceIdentifier, Status: item.resource.Status,
			}
		}
	}
	// Page-level enrichment reports through the page message, so it gets its own progress tracker.
	return dtw.enrichFilesetMetadata(ctx, task, items, result, &discoverTaskReconcileProgress{})
}

// filesetPageSize returns the configured page size, or the default for a worker built without settings.
func (dtw *DiscoverTaskWorker) filesetPageSize() int {
	if dtw.filesetPageSizeSetting > 0 {
		return dtw.filesetPageSizeSetting
	}
	return defaultFilesetPageSize
}

// filesetMaxObjects returns the configured safety limit, or the default for a worker built without settings.
func (dtw *DiscoverTaskWorker) filesetMaxObjects() int {
	if dtw.filesetMaxObjectsSetting > 0 {
		return dtw.filesetMaxObjectsSetting
	}
	return defaultFilesetMaxObjects
}

// resolveFilesetDiscoverSettings validates the discover settings, falling back to defaults with a warning.
func resolveFilesetDiscoverSettings(cfg common.DiscoverConfig) (pageSize int, maxObjects int) {
	pageSize, maxObjects = cfg.FilesetPageSize, cfg.FilesetMaxObjects
	if pageSize < 1 || pageSize > maxFilesetPageSize {
		if pageSize != 0 {
			logger.Warnf("discover.filesetPageSize %d is outside 1-%d, using default %d", pageSize, maxFilesetPageSize, defaultFilesetPageSize)
		}
		pageSize = defaultFilesetPageSize
	}
	if maxObjects < 1 {
		if maxObjects != 0 {
			logger.Warnf("discover.filesetMaxObjects %d is not positive, using default %d", maxObjects, defaultFilesetMaxObjects)
		}
		maxObjects = defaultFilesetMaxObjects
	}
	return pageSize, maxObjects
}

func (dtw *DiscoverTaskWorker) reconcileFilesetResources(ctx context.Context, task *interfaces.DiscoverTask,
	catalog *interfaces.Catalog, source []*interfaces.FilesetMeta,
	existingResources []*interfaces.Resource) (*interfaces.DiscoverResult, []filesetDiscoverItem, error) {

	actions := task.DiscoverActions
	result := &interfaces.DiscoverResult{
		CatalogID: catalog.ID,
	}

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

	items, err := dtw.upsertFilesetResources(ctx, actions, catalog, source, existingMap, result)
	if err != nil {
		return nil, nil, err
	}

	if actions != nil && actions.MarkStale {
		for sourceIdentifier, existing := range existingMap {
			if dtw.stopped.Load() {
				return nil, nil, ErrWorkerManagerStopping
			}
			if _, ok := sourceMap[sourceIdentifier]; !ok {
				dtw.markFilesetMissing(ctx, existing.ID, existing.Status, result)
			}
		}
	}
	return result, items, nil
}

// upsertFilesetResources creates new resources and collects existing ones of source for enrichment.
func (dtw *DiscoverTaskWorker) upsertFilesetResources(ctx context.Context, actions *interfaces.DiscoverActions,
	catalog *interfaces.Catalog, source []*interfaces.FilesetMeta, existingMap map[string]*interfaces.Resource,
	result *interfaces.DiscoverResult) ([]filesetDiscoverItem, error) {

	var items []filesetDiscoverItem
	for _, fs := range source {
		if dtw.stopped.Load() {
			return nil, ErrWorkerManagerStopping
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
	return items, nil
}

// markFilesetMissing records a resource missing from the source and marks it stale when it was active.
func (dtw *DiscoverTaskWorker) markFilesetMissing(ctx context.Context, id string, status string, result *interfaces.DiscoverResult) {
	dtw.markDiscover(ctx, id, interfaces.DiscoverStatusMissing)
	if status != interfaces.ResourceStatusActive {
		return
	}
	if err := dtw.rs.UpdateStatus(ctx, id, interfaces.ResourceStatusStale, ""); err != nil {
		logger.Errorf("Failed to mark resource %s as stale: %v", id, err)
		return
	}
	result.StaleCount++
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
