// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_index_sync

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

const pollInterval = 5 * time.Second

// Service advances durable result intents without ever invoking business tools.
type Service struct {
	store   interfaces.ActionIndexSyncStore
	backend interfaces.ActionIndexSyncBackend
	now     func() time.Time
}

func New(store interfaces.ActionIndexSyncStore, backend interfaces.ActionIndexSyncBackend) *Service {
	return &Service{store: store, backend: backend, now: time.Now}
}

// Run resumes pending work after restart. Each result is claimed with a bounded
// lease, allowing another replica to recover it after the original process dies.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
			logger.Warnf("Action index synchronization scan failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) RunOnce(ctx context.Context) error {
	now := s.now().UnixMilli()
	jobs, err := s.store.ListIndexSync(ctx, now, 20)
	if err != nil {
		return err
	}
	for i := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		job := &jobs[i]
		token := uuid.NewV7().String()
		claimed, err := s.store.ClaimIndexSync(ctx, job, token, s.now().UnixMilli(), s.now().Add(2*time.Minute).UnixMilli())
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err = s.advance(runCtx, job)
		cancel()
		if err != nil {
			logger.Warnf("Action index synchronization result update failed for execution %s: %v", job.ExecutionID, err)
		}
	}
	return nil
}

func (s *Service) advance(ctx context.Context, job *interfaces.ActionIndexSyncJob) error {
	state := job.Sync
	if state.Executor.ID == "" || state.Executor.Type == "" {
		return fmt.Errorf("index synchronization initiator is missing")
	}
	// Recheck the saved initiator's current Vega permissions. A tool cannot grant
	// itself resource access by returning an affected resource identifier.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, state.Executor)
	allComplete := true
	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == "completed" {
			continue
		}
		if task.TaskID == "" {
			id, err := s.backend.CreateKeyBuild(ctx, task.ResourceID, task.Keys)
			if err != nil {
				return s.failed(ctx, job, "create keys task", err)
			}
			task.TaskID = id
			task.Status = "pending"
			state.Status = "submitted"
			// Persist each accepted task before moving to the next resource or chunk.
			if err := s.store.SaveIndexSync(ctx, job); err != nil {
				return err
			}
		}
		explicitRetry := task.Status == "retry_requested"
		status, err := s.backend.GetKeyBuild(ctx, task.TaskID)
		if err != nil {
			return s.failed(ctx, job, "read keys task", err)
		}
		task.Status = status
		switch status {
		case "completed":
		case "stopped", "cancelled":
			if !explicitRetry {
				state.Attempts = 4
				return s.failed(ctx, job, "stopped keys task", fmt.Errorf("keys task was explicitly stopped"))
			}
			fallthrough
		case "failed":
			if state.Attempts >= 4 {
				return s.failed(ctx, job, "keys task", fmt.Errorf("keys task retries exhausted"))
			}
			if err := s.backend.RetryKeyBuild(ctx, task.TaskID); err != nil {
				return s.failed(ctx, job, "retry keys task", err)
			}
			task.Status = "pending"
			return s.failed(ctx, job, "keys task", fmt.Errorf("keys task was not completed"))
		case "pending", "running", "stopping":
			allComplete = false
		default:
			return s.failed(ctx, job, "read keys task", fmt.Errorf("unknown keys task status"))
		}
	}
	state.Error = ""
	if allComplete {
		state.Status = "completed"
	} else {
		state.Status = "submitted"
	}
	return s.release(ctx, job)
}

func (s *Service) failed(ctx context.Context, job *interfaces.ActionIndexSyncJob, operation string, err error) error {
	job.Sync.Attempts++
	job.Sync.Error = syncError(operation, err)
	job.Sync.Status = "retrying"
	if job.Sync.Attempts >= 5 {
		job.Sync.Status = "failed"
	}
	return s.release(ctx, job)
}

func (s *Service) release(ctx context.Context, job *interfaces.ActionIndexSyncJob) error {
	job.Sync.NextAttemptAt = s.now().Add(time.Duration(1<<min(job.Sync.Attempts, 6)) * pollInterval).UnixMilli()
	job.Sync.LeaseUntil = 0
	return s.store.SaveIndexSync(ctx, job)
}
