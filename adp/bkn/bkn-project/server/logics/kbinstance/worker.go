// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"time"

	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// RunBuildWorker resumes only journaled work. It never copies a request context,
// stores a user credential, or turns a status GET into a new inference.
func RunBuildWorker(ctx context.Context, queue interfaces.KBBuildQueue, drafts interfaces.DraftAccess, references interfaces.KBReferenceAccess) {
	if queue == nil || drafts == nil {
		return
	}
	lease := uuid.NewString()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		providers, enabled := kb.Lookup()
		if !enabled {
			runtime := kb.CleanupRuntime()
			if runtime == nil {
				continue
			}
			keys, err := queue.Runnable(ctx, 8)
			if err != nil {
				continue
			}
			c := &Coordinator{Store: queue, Runtime: runtime}
			for _, key := range keys {
				stepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				_ = c.Stop(stepCtx, key.ProjectID, key.ID, lease)
				cancel()
			}
			continue
		}
		if providers.Builds == nil {
			continue
		}
		records, ok := providers.Datasets.(kb.DatasetFileRecords)
		if !ok {
			continue
		}
		keys, err := queue.Runnable(ctx, 8)
		if err != nil {
			continue
		}
		c := &Coordinator{Store: queue, Drafts: drafts, Runtime: providers.Builds, Records: records}
		if datasets, ok := providers.Datasets.(referenceDatasets); ok && references != nil {
			c.Assessor = &ReferenceService{Store: references, Datasets: datasets}
		}
		for _, key := range keys {
			if ctx.Err() != nil {
				return
			}
			stepCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
			_ = c.Step(stepCtx, key.ProjectID, key.ID, lease)
			cancel()
		}
	}
}
