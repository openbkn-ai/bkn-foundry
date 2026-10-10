// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const (
	DiscoverStrategyFullSync    string = "full_sync"
	DiscoverStrategyCreateOnly  string = "create_only"
	DiscoverStrategyCleanupOnly string = "cleanup_only"
	DiscoverStrategyCountOnly   string = "count_only"
)

// DiscoverActions represents the internal resource discovery actions
// derived from a business-level discover strategy.
type DiscoverActions struct {
	Create    bool
	Refresh   bool
	MarkStale bool
	Count     bool
}

func IsValidDiscoverStrategy(strategy string) bool {
	switch strategy {
	case DiscoverStrategyFullSync,
		DiscoverStrategyCreateOnly,
		DiscoverStrategyCleanupOnly,
		DiscoverStrategyCountOnly:
		return true
	default:
		return false
	}
}

func ActionsFromDiscoverStrategy(strategy string) DiscoverActions {
	switch strategy {
	case DiscoverStrategyCountOnly:
		return DiscoverActions{Count: true}
	case DiscoverStrategyCreateOnly:
		return DiscoverActions{Create: true}
	case DiscoverStrategyCleanupOnly:
		return DiscoverActions{MarkStale: true}
	default:
		return DiscoverActions{Create: true, Refresh: true, MarkStale: true}
	}
}

// SupportsResourceCount 判断资源类型是否支持后台精确计数。
// Dataset 在读取时实时统计，不参与后台计数任务。
func SupportsResourceCount(category string) bool {
	switch category {
	case ResourceCategoryTable, ResourceCategoryIndex, ResourceCategoryLogicalView:
		return true
	default:
		return false
	}
}
