// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	jsonv2 "encoding/json/v2"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
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
	data, err := jsoncodec.Marshal(resource.SourceMetadata)
	if err != nil {
		return ""
	}
	sourceMetadata := make(map[string]any)
	err = jsoncodec.Unmarshal(data, &sourceMetadata)
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
	metadata := any(resource.SourceMetadata)
	if resource.SourceMetadata == nil {
		// 库中读出的 null 会解码成 nil map，比较时保留它与空对象的区别。
		metadata = nil
	}
	encoded, err := jsoncodec.Marshal(metadata, jsonv2.Deterministic(true))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
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
