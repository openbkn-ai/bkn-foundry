// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/build_task"
	"vega-backend/logics/local_index"
	"vega-backend/logics/resource"
)

const (
	indexCleanupDefaultInterval         = time.Hour
	indexCleanupDefaultProtectionPeriod = 24 * time.Hour
	indexCleanupDefaultMaxDeletes       = 100
)

// IndexCleanupWorker periodically removes obsolete, Vega-managed build indexes.
// It owns all ownership and lifecycle decisions; LocalIndexManager only performs
// OpenSearch operations.
type IndexCleanupWorker struct {
	appSetting *common.AppSetting
	lim        interfaces.LocalIndexManager
	rs         interfaces.ResourceService
	bts        interfaces.BuildTaskService

	interval         time.Duration
	protectionPeriod time.Duration
	maxDeletesPerRun int

	wg     sync.WaitGroup
	stopCh chan struct{}
}

type indexCleanupCandidate struct {
	index      *interfaces.IndexMeta
	resourceID string
	taskID     string
	reason     string
}

type indexCleanupStats struct {
	scanned, ignored, protected, candidate, deleted, skipped, failed int
}

type resourceIndexSnapshot struct {
	byID       map[string]*interfaces.Resource
	references map[string]struct{}
}

func NewIndexCleanupWorker(appSetting *common.AppSetting) *IndexCleanupWorker {
	rs := resource.NewResourceService(appSetting)
	worker := &IndexCleanupWorker{
		appSetting: appSetting,
		lim:        local_index.NewLocalIndexManager(appSetting),
		rs:         rs,
		bts:        build_task.NewBuildTaskService(appSetting, rs),
		stopCh:     make(chan struct{}),
	}
	worker.interval = indexCleanupDefaultInterval
	if appSetting.IndexCleanup.Interval > 0 {
		worker.interval = appSetting.IndexCleanup.Interval
	}
	worker.protectionPeriod = indexCleanupDefaultProtectionPeriod
	if appSetting.IndexCleanup.ProtectionPeriod > 0 {
		worker.protectionPeriod = appSetting.IndexCleanup.ProtectionPeriod
	}
	worker.maxDeletesPerRun = indexCleanupDefaultMaxDeletes
	if appSetting.IndexCleanup.MaxDeletesPerRun > 0 {
		worker.maxDeletesPerRun = appSetting.IndexCleanup.MaxDeletesPerRun
	}
	return worker
}

func (icw *IndexCleanupWorker) Start() {
	if !icw.appSetting.IndexCleanup.WorkerEnabled {
		logger.Info("Index cleanup worker is disabled")
		return
	}
	icw.wg.Add(1)
	go func() {
		defer icw.wg.Done()
		icw.run(context.Background())
	}()
	logger.Info("Index cleanup worker started")
}

func (icw *IndexCleanupWorker) Stop() {
	close(icw.stopCh)
	icw.wg.Wait()
}

func (icw *IndexCleanupWorker) run(ctx context.Context) {
	icw.runOnce(ctx)
	ticker := time.NewTicker(icw.interval)
	defer ticker.Stop()
	for {
		select {
		case <-icw.stopCh:
			return
		case <-ticker.C:
			icw.runOnce(ctx)
		}
	}
}

func (icw *IndexCleanupWorker) runOnce(ctx context.Context) {
	startedAt := time.Now()
	stats := indexCleanupStats{}
	indexes, err := icw.lim.ListIndexes(ctx)
	if err != nil {
		logger.Errorf("Index cleanup scan failed: list indexes: %v", err)
		return
	}
	resources, err := icw.currentReferences(ctx)
	if err != nil {
		logger.Errorf("Index cleanup scan failed: list resource references: %v", err)
		return
	}

	candidates := make([]indexCleanupCandidate, 0)
	for _, index := range indexes {
		stats.scanned++
		resourceID, taskID, ok := logics.ParseBuildIndexName(index.Name)
		if !ok || index.CreationTime <= 0 {
			stats.ignored++
			continue
		}
		candidate, protected, err := icw.classify(ctx, index, resourceID, taskID, resources)
		if err != nil {
			stats.skipped++
			logger.Errorf("Index cleanup skipped index after ownership lookup failed: index=%s, error=%v", index.Name, err)
			continue
		}
		if protected {
			stats.protected++
			continue
		}
		if !candidate {
			stats.skipped++
			continue
		}
		if time.Since(time.UnixMilli(index.CreationTime)) < icw.protectionPeriod {
			stats.skipped++
			continue
		}
		stats.candidate++
		candidates = append(candidates, indexCleanupCandidate{index: index, resourceID: resourceID, taskID: taskID})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].index.CreationTime == candidates[j].index.CreationTime {
			return candidates[i].index.Name < candidates[j].index.Name
		}
		return candidates[i].index.CreationTime < candidates[j].index.CreationTime
	})
	for i, candidate := range candidates {
		if i >= icw.maxDeletesPerRun {
			stats.skipped++
			continue
		}
		if icw.appSetting.IndexCleanup.DryRun {
			logger.Infof("Index cleanup dry run: would delete index=%s", candidate.index.Name)
			continue
		}
		if !icw.recheckCandidate(ctx, candidate) {
			stats.skipped++
			continue
		}
		if err := icw.lim.DeleteIndex(ctx, candidate.index.Name); err != nil {
			stats.failed++
			logger.Errorf("Index cleanup delete failed: index=%s, error=%v", candidate.index.Name, err)
			continue
		}
		stats.deleted++
	}
	logger.Infof("Index cleanup scan completed: scanned=%d ignored=%d protected=%d candidate=%d deleted=%d skipped=%d failed=%d duration=%s dry_run=%t",
		stats.scanned, stats.ignored, stats.protected, stats.candidate, stats.deleted, stats.skipped, stats.failed,
		time.Since(startedAt), icw.appSetting.IndexCleanup.DryRun)
}

func (icw *IndexCleanupWorker) recheckCandidate(ctx context.Context, candidate indexCleanupCandidate) bool {
	resources, err := icw.currentReferences(ctx)
	if err != nil {
		logger.Errorf("Index cleanup recheck failed: index=%s, error=%v", candidate.index.Name, err)
		return false
	}
	candidateOK, _, err := icw.classify(ctx, candidate.index, candidate.resourceID, candidate.taskID, resources)
	if err != nil || !candidateOK {
		return false
	}
	return time.Since(time.UnixMilli(candidate.index.CreationTime)) >= icw.protectionPeriod
}

func (icw *IndexCleanupWorker) currentReferences(ctx context.Context) (*resourceIndexSnapshot, error) {
	resources, err := icw.rs.InternalList(ctx, interfaces.ResourcesQueryParams{})
	if err != nil {
		return nil, err
	}
	snapshot := &resourceIndexSnapshot{
		byID:       make(map[string]*interfaces.Resource, len(resources)),
		references: make(map[string]struct{}, len(resources)),
	}
	for _, resource := range resources {
		snapshot.byID[resource.ID] = resource
		if resource.LocalIndexName != "" {
			snapshot.references[resource.LocalIndexName] = struct{}{}
		}
	}
	return snapshot, nil
}

func (icw *IndexCleanupWorker) classify(ctx context.Context, index *interfaces.IndexMeta, resourceID, taskID string, resources *resourceIndexSnapshot) (candidate bool, protected bool, err error) {
	if _, ok := resources.references[index.Name]; ok {
		return false, true, nil
	}
	resource := resources.byID[resourceID]
	task, err := icw.bts.InternalGetByID(ctx, taskID)
	if err != nil {
		return false, false, err
	}
	if task != nil && task.ResourceID != resourceID {
		return false, true, nil
	}
	if resource == nil || task == nil {
		return true, false, nil
	}
	switch task.Status {
	case interfaces.BuildTaskStatusPending, interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping,
		interfaces.BuildTaskStatusStopped, interfaces.BuildTaskStatusFailed:
		return false, true, nil
	case interfaces.BuildTaskStatusCancelled:
		return true, false, nil
	case interfaces.BuildTaskStatusCompleted:
		return true, false, nil
	default:
		return false, true, nil
	}
}
