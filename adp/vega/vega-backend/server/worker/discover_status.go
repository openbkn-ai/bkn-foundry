// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/kweaver-ai/kweaver-go-lib/logger"

	"vega-backend/interfaces"
)

// LastDiscoverStatus 写入策略：
//   - Status 是状态机：受幂等守卫保护，仅在生命周期翻转时写入。
//   - LastDiscoverStatus 是"最近一次观察"：每次扫描都覆盖，反映本次结果。
//   - 因此 markDiscover 必须放在状态机守卫之外，避免已 stale 的资源
//     在持续缺席的扫描中失去 missing 标记。
//   - new/restored 是一次性事件标签，下次扫描自动让位给 unchanged/updated；
//     unchanged/updated/missing 是持续观察，每次重写。
//   - 未来若再加同类"观察"字段，应考虑下沉到独立事件表（PR-B 方向），
//     而非继续往 Resource 实体塞。
func (dh *DiscoverHandler) markDiscover(ctx context.Context, resourceID string, status string) {
	if err := dh.rs.UpdateDiscoverStatus(ctx, resourceID, status); err != nil {
		logger.Errorf("Failed to update last discover status for resource %s: %v", resourceID, err)
	}
}

func discoverStatusAfterEnrich(resource *interfaces.Resource, beforeHash string) string {
	status := interfaces.DiscoverStatusUnchanged

	// 将结构体转换为map，方便序列化后计算hash（按key排序）
	data, err := sonic.Marshal(resource.SourceMetadata)
	if err != nil {
		return ""
	}
	sourceMetadata := make(map[string]any)
	err = sonic.Unmarshal(data, &sourceMetadata)
	if err != nil {
		return ""
	}
	resource.SourceMetadata = sourceMetadata

	if sourceSnapshotHash(resource) != beforeHash {
		status = interfaces.DiscoverStatusUpdated
	}
	return status
}

func sourceSnapshotHash(resource *interfaces.Resource) string {
	if resource == nil {
		return ""
	}
	bytes, err := sonic.ConfigStd.Marshal(resource.SourceMetadata)
	if err != nil {
		return ""
	}
	sum := sha1.Sum(bytes)
	hashStr := hex.EncodeToString(sum[:])
	logger.Infof("SourceMetadata hash: %s, orig: %s", hashStr, bytes)
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
	}
}

func formatDiscoverResultMessage(result *interfaces.DiscoverResult) string {
	return fmt.Sprintf("Discover completed: %d new, %d stale, %d unchanged, %d updated, %d restored",
		result.NewCount, result.StaleCount, result.UnchangedCount, result.UpdatedCount, result.RestoredCount)
}
