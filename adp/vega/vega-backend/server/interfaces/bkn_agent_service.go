// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/bkn_agent_service.go -destination ../interfaces/mock/mock_bkn_agent_service.go

type BknAgentService interface {
	Run(ctx context.Context, task *SemanticUnderstandingTask) (string, error)
	WaitResult(ctx context.Context, agentTaskID string) (*BknAgentTask, error)
}
