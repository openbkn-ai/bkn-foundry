// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestRestHandlerRetryActionIndexSync(t *testing.T) {
	t.Run("knowledge network permission is required before result lookup", func(t *testing.T) {
		denied := httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)
		handler := &restHandler{actionReadPermissions: &actionLogReadPermissionStub{err: denied}}
		ctx, w := actionLogReadContext("/knowledge-networks/kn-1/action-executions/exec-1/index-sync/retry")
		handler.retryActionIndexSync(ctx, hydra.Visitor{ID: "alice", Type: hydra.VisitorType_User})
		require.Equal(t, http.StatusForbidden, w.Code)
	})
	t.Run("accepted is not a claim of completed indexing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		scheduler := omock.NewMockActionSchedulerService(ctrl)
		scheduler.EXPECT().RetryActionIndexSync(gomock.Any(), "kn-1", "exec-1").DoAndReturn(func(ctx context.Context, _ string, _ string) (int, error) {
			require.Equal(t, "alice", ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo).ID)
			return 2, nil
		})
		handler := &restHandler{ass: scheduler, actionReadPermissions: &actionLogReadPermissionStub{}}
		ctx, w := actionLogReadContext("/knowledge-networks/kn-1/action-executions/exec-1/index-sync/retry")
		handler.retryActionIndexSync(ctx, hydra.Visitor{ID: "alice", Type: hydra.VisitorType_User})
		require.Equal(t, http.StatusAccepted, w.Code)
		require.Contains(t, w.Body.String(), `"retried_results":2`)
	})
}
