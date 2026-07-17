// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source ../interfaces/semantic_understanding_task_access.go -destination ../interfaces/mock/mock_semantic_understanding_task_access.go

type SemanticUnderstandingTaskAccess interface {
	Create(ctx context.Context, task *SemanticUnderstandingTask) error
	GetByID(ctx context.Context, id string) (*SemanticUnderstandingTask, error)
	GetByIDs(ctx context.Context, ids []string) ([]*SemanticUnderstandingTask, error)
	FindActiveByInputHash(ctx context.Context, scope string, inputHash string) (*SemanticUnderstandingTask, error)
	List(ctx context.Context, params SemanticUnderstandingTaskQueryParams) ([]*SemanticUnderstandingTask, int64, error)
	Delete(ctx context.Context, id string) error
	DeleteByIDs(ctx context.Context, ids []string) (int64, error)

	MarkRunning(ctx context.Context, id string, agentTaskID string) (bool, error)
	ClaimRunning(ctx context.Context, id string) (bool, error)
	SetAgentTaskID(ctx context.Context, id string, agentTaskID string) (bool, error)
	MarkSucceeded(ctx context.Context, id string, resultJSON string, confidence float64, confidenceDetailJSON string) (bool, error)
	MarkFailed(ctx context.Context, id string, failureDetail string) (bool, error)
	MarkApplied(ctx context.Context, id string, applied bool, appliedTime int64, applyDetailJSON string) (bool, error)
	MarkAppliedWithTx(ctx context.Context, tx *sql.Tx, id string, applied bool, appliedTime int64, applyDetailJSON string) (bool, error)
}
