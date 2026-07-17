// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"

	"github.com/hibiken/asynq"
)

//go:generate mockgen -source ../interfaces/semantic_understanding_task_service.go -destination ../interfaces/mock/mock_semantic_understanding_task_service.go

type SemanticUnderstandingTaskService interface {
	CreateResourceTask(ctx context.Context, resourceID string, req *CreateSemanticUnderstandingTaskRequest) (*SemanticUnderstandingTask, error)
	CreateCatalogTask(ctx context.Context, catalogID string, req *CreateSemanticUnderstandingTaskRequest) (*SemanticUnderstandingTask, error)
	GetByID(ctx context.Context, id string) (*SemanticUnderstandingTask, error)
	List(ctx context.Context, params SemanticUnderstandingTaskQueryParams) ([]*SemanticUnderstandingTask, int64, error)
	Delete(ctx context.Context, ids []string, ignoreMissing bool) error

	MarkRunning(ctx context.Context, id string, agentTaskID string) (bool, error)
	ClaimRunning(ctx context.Context, id string) (bool, error)
	SetAgentTaskID(ctx context.Context, id string, agentTaskID string) (bool, error)
	MarkSucceeded(ctx context.Context, id string, resultJSON string, confidence float64, confidenceDetailJSON string) (bool, error)
	MarkFailed(ctx context.Context, id string, failureDetail string) (bool, error)
	MarkApplied(ctx context.Context, id string, applied bool, applyDetailJSON string) (bool, error)

	DebugTaskQueue() <-chan *asynq.Task

	InternalGetByID(ctx context.Context, id string) (*SemanticUnderstandingTask, error)
	InternalMarkApplied(ctx context.Context, tx *sql.Tx, id string, applied bool, applyDetailJSON string) (bool, error)
}
