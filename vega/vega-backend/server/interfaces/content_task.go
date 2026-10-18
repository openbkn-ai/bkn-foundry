// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/content_task.go -destination ../interfaces/mock/mock_content_task.go

// Content task states. Content tasks use only these four states.
const (
	ContentTaskStatusPending   string = "pending"
	ContentTaskStatusRunning   string = "running"
	ContentTaskStatusCompleted string = "completed"
	ContentTaskStatusFailed    string = "failed"
)

// ContentTaskRequest asks a content worker to process fileset resources pinned to one content version.
type ContentTaskRequest struct {
	CatalogID      string
	ContentVersion string
	ResourceIDs    []string
}

// ContentTaskRef identifies a dispatched content task.
type ContentTaskRef struct {
	TaskID string
}

// ContentTaskStatus reports the state of a dispatched content task.
type ContentTaskStatus struct {
	TaskID  string
	Status  string
	Message string
}

// ContentTaskDispatcher hands fileset resources to a content worker (parse and chunk) and reports task state.
// Core assembles a no-op implementation; an edition build may install a real one before the service runs.
type ContentTaskDispatcher interface {
	// Enabled reports whether a real implementation is assembled and currently usable.
	Enabled(ctx context.Context) bool
	// Dispatch submits req and returns the task reference.
	Dispatch(ctx context.Context, req *ContentTaskRequest) (*ContentTaskRef, error)
	// GetStatus returns the current state of a dispatched task.
	GetStatus(ctx context.Context, taskID string) (*ContentTaskStatus, error)
}
