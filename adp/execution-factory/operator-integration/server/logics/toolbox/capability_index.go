// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
)

// The capability index is optional to this service. Tests and any other construction path that
// does not wire it must still be able to write tools, so every call goes through these guards
// rather than touching the field directly. A tool write is not worth a panic because an index
// happened not to be configured.

func (s *ToolServiceImpl) syncToolsIndex(ctx context.Context, boxID string, toolIDs []string) {
	if s.CapabilityIndex == nil {
		return
	}
	s.CapabilityIndex.SyncToolsAsync(ctx, boxID, toolIDs)
}

func (s *ToolServiceImpl) syncBoxIndex(ctx context.Context, boxID string) {
	if s.CapabilityIndex == nil {
		return
	}
	s.CapabilityIndex.SyncBoxAsync(ctx, boxID)
}

// ReconcileCapabilityIndexAsync is the importer's hook: imported rows never pass through the
// per-write syncs above, so the whole index is brought in line once the import has committed.
func (s *ToolServiceImpl) ReconcileCapabilityIndexAsync(ctx context.Context) {
	if s.CapabilityIndex == nil {
		return
	}
	s.CapabilityIndex.RequestReconcile(ctx)
}

func (s *ToolServiceImpl) forgetBoxIndex(ctx context.Context, boxID string) {
	if s.CapabilityIndex == nil {
		return
	}
	s.CapabilityIndex.ForgetBoxAsync(ctx, boxID)
}
