// Copyright 2026 kowell.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const (
	DiscoverStrategyFullSync    string = "full_sync"
	DiscoverStrategyCreateOnly  string = "create_only"
	DiscoverStrategyCleanupOnly string = "cleanup_only"

	DiscoverActionCreate    string = "create"
	DiscoverActionRefresh   string = "refresh"
	DiscoverActionMarkStale string = "mark_stale"
)

// DiscoverActions represents the internal resource reconciliation actions
// derived from a business-level discover strategy.
type DiscoverActions struct {
	Create    bool
	Refresh   bool
	MarkStale bool
}

func IsValidDiscoverStrategy(strategy string) bool {
	switch strategy {
	case DiscoverStrategyFullSync, DiscoverStrategyCreateOnly, DiscoverStrategyCleanupOnly:
		return true
	default:
		return false
	}
}

func ActionsFromDiscoverStrategy(strategy string) DiscoverActions {
	switch strategy {
	case DiscoverStrategyCreateOnly:
		return DiscoverActions{Create: true}
	case DiscoverStrategyCleanupOnly:
		return DiscoverActions{MarkStale: true}
	default:
		return DiscoverActions{Create: true, Refresh: true, MarkStale: true}
	}
}
