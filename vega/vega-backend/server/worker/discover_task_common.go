// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
)

// LastDiscoverStatus write policy:
//
//   - Status is a state machine written only when the lifecycle changes.
//   - LastDiscoverStatus is the most recent observation and is updated on every scan.
//   - markDiscover must stay outside state-machine guards so repeated absence scans retain "missing".
//   - "new" and "restored" are one-off events; the next scan replaces them with "unchanged" or "updated".
//   - "unchanged", "updated", and "missing" are continuous observations and are rewritten each time.
//   - Future observation fields should move to a dedicated event table instead of expanding Resource.
func (dh *DiscoverTaskWorker) markDiscover(ctx context.Context, resourceID string, status string) {
	if err := dh.rs.UpdateDiscoverStatus(ctx, resourceID, status); err != nil {
		logger.Errorf("Failed to update last discover status for resource %s: %v", resourceID, err)
	}
}

func discoverStatusAfterEnrich(resource *interfaces.Resource, beforeHash string) string {
	status := interfaces.DiscoverStatusUnchanged

	// Convert the structure to a map to facilitate the calculation of the hash after serialization (sorted by key)
	data, err := sonic.Marshal(resource.SourceMetadata)
	if err != nil {
		return ""
	}
	sourceMetadata := make(map[string]any)
	err = common.UnmarshalPreciseJSON(data, &sourceMetadata)
	if err != nil {
		return ""
	}
	resource.SourceMetadata = sourceMetadata

	newHash := sourceSnapshotHash(resource)
	if newHash != beforeHash {
		status = interfaces.DiscoverStatusUpdated
	}
	return status
}

func sourceSnapshotHash(resource *interfaces.Resource) string {
	if resource == nil {
		return ""
	}
	metadata := resource.SourceMetadata
	bytes, err := sonic.ConfigStd.Marshal(metadata)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(bytes)
	hashStr := hex.EncodeToString(sum[:])
	//logger.Infof("SourceMetadata hash: %s, orig: %s", hashStr, bytes)

	return hashStr
}

func updateDiscoverResultForEnrichStatus(result *interfaces.DiscoverResult, status string) {
	if result == nil {
		return
	}
	switch status {
	case interfaces.DiscoverStatusUpdated:
		result.UpdatedCount++
	case interfaces.DiscoverStatusUnchanged:
		result.UnchangedCount++
	case interfaces.DiscoverStatusError:
		result.FailedCount++
	}
}

func formatDiscoverResultMessage(result *interfaces.DiscoverResult) string {
	return fmt.Sprintf("Discover completed: %d new, %d stale, %d unchanged, %d updated, %d restored, %d failed",
		result.NewCount, result.StaleCount, result.UnchangedCount, result.UpdatedCount, result.RestoredCount, result.FailedCount)
}

// countOnlyActions 判断是否只执行计数，不排斥其他动作组合。
func countOnlyActions(actions *interfaces.DiscoverActions) bool {
	return actions != nil && actions.Count && !actions.Create && !actions.Refresh && !actions.MarkStale
}

// saveDiscoveredResource 在单个资源事务内编排元数据和精确统计保存。
func (dtw *DiscoverTaskWorker) saveDiscoveredResource(ctx context.Context, resource *interfaces.Resource, expectedUpdateTime int64, exactCount *int64, countTime int64) error {
	if exactCount == nil {
		return dtw.rs.InternalUpdateDiscoveryMetadata(ctx, nil, resource, expectedUpdateTime)
	}
	tx, err := logics.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := dtw.rs.InternalUpdateDiscoveryMetadata(ctx, tx, resource, expectedUpdateTime); err != nil {
		return err
	}
	if err := dtw.rs.InternalUpdateRowCount(ctx, tx, resource, *exactCount, countTime); err != nil {
		return err
	}
	return tx.Commit()
}
