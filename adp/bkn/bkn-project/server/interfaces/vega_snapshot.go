// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// VegaSnapshotAccess reads one complete cursor query for a current operator.
// Callers supply server-generated SQL and a live project/model guard. Results
// are never silently truncated to Query's single-page limit. No credentials or
// arbitrary client SQL are persisted for background use.
type VegaSnapshotAccess interface {
	QuerySnapshot(context.Context, VegaAccount, string, int, int, func(context.Context) error) ([]map[string]any, error)
}
