// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

// RunBuildWorker resumes only journaled work. It never copies a request context,
// stores a user credential, or turns a status GET into a new inference.
func RunBuildWorker(ctx context.Context, queue interfaces.KBBuildQueue, drafts interfaces.DraftAccess, references interfaces.KBReferenceAccess) {
	if queue == nil || drafts == nil {
		return
	}
	// Reported once at startup: registration happens before this worker starts.
	if missing := kb.MissingProviders(); len(missing) > 0 {
		logger.Errorf("Knowledge base is installed without: %s; those operations stay unavailable", strings.Join(missing, ", "))
	}
	worker := &buildWorker{queue: queue, drafts: drafts, references: references, lease: uuid.NewString(), access: kb.ForWorker, log: NewWorkerLog("build")}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		worker.tick(ctx)
	}
}

type buildWorker struct {
	queue      interfaces.KBBuildQueue
	drafts     interfaces.DraftAccess
	references interfaces.KBReferenceAccess
	lease      string
	access     func() (kb.Providers, kb.WorkerAccess)
	log        *WorkerLog
}

func (w *buildWorker) tick(ctx context.Context) {
	providers, access := w.access()
	if access == kb.WorkerNotInstalled {
		return
	}
	paused := ""
	if access == kb.WorkerPause {
		paused = "licence not known yet; builds paused, not cancelled"
	}
	w.log.Condition("licence", paused, "licence known; builds resumed")
	if access == kb.WorkerPause {
		return
	}
	if providers.Builds == nil {
		w.log.Problem("configuration", errors.New("no build runtime is installed"))
		return
	}
	var coordinator *Coordinator
	var timeout time.Duration
	var step func(context.Context, *Coordinator, string, string) error
	if access == kb.WorkerClean {
		coordinator, timeout = &Coordinator{Store: w.queue, Runtime: providers.Builds}, 30*time.Second
		step = func(ctx context.Context, c *Coordinator, project, id string) error {
			return c.Stop(ctx, project, id, w.lease)
		}
	} else {
		records, ok := providers.Datasets.(kb.DatasetFileRecords)
		if !ok {
			w.log.Problem("configuration", errors.New("no dataset storage is installed"))
			return
		}
		coordinator, timeout = &Coordinator{Store: w.queue, Drafts: w.drafts, Runtime: providers.Builds, Records: records}, 180*time.Second
		if datasets, ok := providers.Datasets.(referenceDatasets); ok && w.references != nil {
			coordinator.Assessor = &ReferenceService{Store: w.references, Datasets: datasets}
		}
		step = func(ctx context.Context, c *Coordinator, project, id string) error {
			return c.Step(ctx, project, id, w.lease)
		}
	}
	w.log.Problem("configuration", nil)
	keys, err := w.queue.Runnable(ctx, 8)
	w.log.Problem("queue", err)
	if err != nil {
		return
	}
	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		before := w.observe(ctx, key)
		stepCtx, cancel := context.WithTimeout(ctx, timeout)
		err := step(stepCtx, coordinator, key.ProjectID, key.ID)
		cancel()
		item := key.ProjectID + "/" + key.ID
		if errors.Is(err, ErrBuildChanged) {
			err = nil // Another writer or a cancellation won; that is not a failure.
		}
		w.log.Result(item, err)
		w.log.Transition(item, stateChanges(before, w.observe(ctx, key)))
	}
}

func (w *buildWorker) observe(ctx context.Context, key interfaces.KBBuildKey) *interfaces.KBBuild {
	record, err := w.queue.Get(ctx, key.ProjectID, key.ID)
	if err != nil || record == nil {
		return nil
	}
	return record.Build
}

// StateChanges lists what changed between two committed readings of a build.
func stateChanges(before, after *interfaces.KBBuild) []string {
	if before == nil || after == nil {
		return nil
	}
	changes := []string{}
	if before.State != after.State {
		changes = append(changes, fmt.Sprintf("%s -> %s", before.State, after.State))
	}
	for i := range after.Files {
		if i < len(before.Files) && before.Files[i].State != after.Files[i].State {
			changes = append(changes, fmt.Sprintf("file %d: %s -> %s %s", i, before.Files[i].State, after.Files[i].State, after.Files[i].Reason))
		}
	}
	return changes
}
