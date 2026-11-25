// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

func (s *actionSchedulerService) RetryActionIndexSync(ctx context.Context, knID, executionID string) (int, error) {
	caller, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok || caller.ID == "" || caller.Type == "" {
		return 0, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
	}
	execution, err := s.logsService.GetExecution(ctx, &interfaces.ActionLogDetailQuery{KNID: knID, LogID: executionID, ResultsLimit: 1})
	if err != nil {
		return 0, err
	}
	if execution == nil {
		return 0, fmt.Errorf("action execution is unavailable")
	}
	if execution.KNID != knID || execution.Executor.ID != caller.ID || execution.Executor.Type != caller.Type {
		return 0, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
	}
	if s.indexSyncStore == nil {
		return 0, fmt.Errorf("action index synchronization is unavailable")
	}
	return s.indexSyncStore.RetryIndexSync(ctx, knID, executionID, time.Now().UnixMilli())
}
