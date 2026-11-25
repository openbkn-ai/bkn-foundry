// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestActionSchedulerServiceFlushResultsRecordsCommittedChanges(t *testing.T) {
	ctrl := gomock.NewController(t)
	logs := omock.NewMockActionLogsService(ctrl)
	actor := interfaces.AccountInfo{ID: "alice", Type: "user"}
	execution := &interfaces.ActionExecution{KNID: "kn", ID: "exec", Executor: actor}
	result := interfaces.ObjectExecutionResult{Status: "success", Result: map[string]any{"data_changes": map[string]any{"committed": true, "affected_resources": []any{map[string]any{"resource_id": "brands", "keys": []any{map[string]any{"id": "662"}}}}}}}
	logs.EXPECT().AppendResults(gomock.Any(), "kn", "exec", 0, gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ string, _ int, results []interfaces.ObjectExecutionResult) error {
		require.Equal(t, "success", results[0].Status)
		require.Equal(t, "pending", results[0].IndexSync.Status)
		require.Equal(t, actor, results[0].IndexSync.Executor)
		return nil
	})
	svc := &actionSchedulerService{logsService: logs}
	buffer := &resultBuffer{pending: []interfaces.ObjectExecutionResult{result}}
	svc.flushResults(context.Background(), execution, buffer)
	require.Equal(t, 1, buffer.stored)
	require.Empty(t, buffer.pending)
}

func TestActionSchedulerServiceRetryActionIndexSyncRejectsAnotherInitiator(t *testing.T) {
	ctrl := gomock.NewController(t)
	logs := omock.NewMockActionLogsService(ctrl)
	logs.EXPECT().GetExecution(gomock.Any(), gomock.Any()).Return(&interfaces.ActionExecution{KNID: "kn", Executor: interfaces.AccountInfo{ID: "alice", Type: "user"}}, nil)
	svc := &actionSchedulerService{logsService: logs}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "bob", Type: "user"})
	count, err := svc.RetryActionIndexSync(ctx, "kn", "exec")
	require.Error(t, err)
	require.Zero(t, count)
}

type retryIndexStoreStub struct {
	interfaces.ActionIndexSyncStore
	called bool
}

func (s *retryIndexStoreStub) RetryIndexSync(_ context.Context, knID, execID string, _ int64) (int, error) {
	s.called = knID == "kn" && execID == "exec"
	return 2, nil
}

func TestActionSchedulerServiceRetryActionIndexSyncUsesSavedResultsOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	logs := omock.NewMockActionLogsService(ctrl)
	actor := interfaces.AccountInfo{ID: "alice", Type: "user"}
	logs.EXPECT().GetExecution(gomock.Any(), gomock.Any()).Return(&interfaces.ActionExecution{KNID: "kn", Executor: actor}, nil)
	store := &retryIndexStoreStub{}
	svc := &actionSchedulerService{logsService: logs, indexSyncStore: store}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, actor)
	count, err := svc.RetryActionIndexSync(ctx, "kn", "exec")
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.True(t, store.called)
}
