// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// ActionIndexSync is saved atomically with the successful invocation result.
// Lease fields are internal coordination data, never caller-controlled input.
type ActionIndexSync struct {
	Status        string                `json:"status"`
	Executor      AccountInfo           `json:"executor"`
	Tasks         []ActionIndexSyncTask `json:"tasks,omitempty"`
	Error         string                `json:"error,omitempty"`
	Attempts      int                   `json:"attempts"`
	NextAttemptAt int64                 `json:"next_attempt_at"`
	LeaseUntil    int64                 `json:"lease_until"`
	LeaseToken    string                `json:"lease_token,omitempty"`
	Revision      int64                 `json:"revision"`
}

type ActionIndexSyncTask struct {
	ResourceID string           `json:"resource_id"`
	Keys       []map[string]any `json:"keys"`
	TaskID     string           `json:"task_id,omitempty"`
	Status     string           `json:"status,omitempty"`
}

type ActionIndexSyncJob struct {
	KNID        string           `json:"kn_id"`
	ExecutionID string           `json:"execution_id"`
	Seq         int              `json:"seq"`
	Sync        *ActionIndexSync `json:"index_sync"`
}

// ActionIndexSyncStore coordinates multiple replicas using conditional updates.
type ActionIndexSyncStore interface {
	ListIndexSync(ctx context.Context, now int64, limit int) ([]ActionIndexSyncJob, error)
	ClaimIndexSync(ctx context.Context, job *ActionIndexSyncJob, token string, now, until int64) (bool, error)
	SaveIndexSync(ctx context.Context, job *ActionIndexSyncJob) error
	RetryIndexSync(ctx context.Context, knID, execID string, now int64) (int, error)
}

// ActionIndexSyncBackend delegates admission, authorization and visibility to Vega.
type ActionIndexSyncBackend interface {
	CreateKeyBuild(ctx context.Context, resourceID string, keys []map[string]any) (string, error)
	GetKeyBuild(ctx context.Context, taskID string) (string, error)
	RetryKeyBuild(ctx context.Context, taskID string) error
}
