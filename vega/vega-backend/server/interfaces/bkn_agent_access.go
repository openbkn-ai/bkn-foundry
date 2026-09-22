// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/bkn_agent_access.go -destination ../interfaces/mock/mock_bkn_agent_access.go

type BknAgentAccess interface {
	Run(ctx context.Context, req *BknAgentRunRequest) (*BknAgentRunResponse, error)
	GetTask(ctx context.Context, taskID string) (*BknAgentTask, error)
}
