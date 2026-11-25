// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_logs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestActionLogsServiceStoreIndexSyncResult(t *testing.T) {
	s, os := newWriteTestService(t)
	os.EXPECT().IndexExists(gomock.Any(), interfaces.ActionExecutionResultsIndex).Return(true, nil)
	os.EXPECT().PutMapping(gomock.Any(), interfaces.ActionExecutionResultsIndex, indexSyncMapping).Return(nil)
	result := interfaces.ObjectExecutionResult{Status: "success", IndexSync: &interfaces.ActionIndexSync{Status: "pending"}}
	os.EXPECT().UpdateData(gomock.Any(), interfaces.ActionExecutionResultsIndex, resultDocumentID("exec", 2), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ string, body any) (string, error) {
		request := body.(map[string]any)
		require.Equal(t, true, request["scripted_upsert"])
		script := request["script"].(map[string]any)
		require.True(t, strings.Contains(script["source"].(string), "ctx._source.index_sync = sync"))
		document := script["params"].(map[string]any)["document"].(storedExecutionResult)
		require.Equal(t, 2, document.Seq)
		require.Equal(t, "kn", document.KNID)
		require.Equal(t, "success", document.Status)
		require.Equal(t, "pending", document.IndexSync.Status)
		return "created", nil
	})
	require.NoError(t, s.storeIndexSyncResult(context.Background(), "kn", "exec", 2, result))
}

func TestActionLogsServiceClaimIndexSync(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		claimed      bool
	}{{"one replica wins", "updated", true}, {"stale snapshot cannot claim", "noop", false}} {
		t.Run(tc.name, func(t *testing.T) {
			s, os := newWriteTestService(t)
			job := &interfaces.ActionIndexSyncJob{ExecutionID: "exec", Seq: 1, Sync: &interfaces.ActionIndexSync{Revision: 7}}
			os.EXPECT().UpdateData(gomock.Any(), interfaces.ActionExecutionResultsIndex, resultDocumentID("exec", 1), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ string, body any) (string, error) {
				script := body.(map[string]any)["script"].(map[string]any)
				require.Equal(t, claimIndexSyncScript, script["source"])
				params := script["params"].(map[string]any)
				require.Equal(t, int64(7), params["revision"])
				require.Equal(t, "token", params["token"])
				require.Contains(t, script["source"], "s.lease_until <= params.now")
				return tc.result, nil
			})
			claimed, err := s.ClaimIndexSync(context.Background(), job, "token", 100, 1000)
			require.NoError(t, err)
			require.Equal(t, tc.claimed, claimed)
			if claimed {
				require.Equal(t, int64(8), job.Sync.Revision)
				require.Equal(t, int64(1000), job.Sync.LeaseUntil)
			}
		})
	}
}

func TestActionLogsServiceSaveIndexSync(t *testing.T) {
	s, os := newWriteTestService(t)
	job := &interfaces.ActionIndexSyncJob{ExecutionID: "exec", Seq: 0, Sync: &interfaces.ActionIndexSync{Revision: 3, LeaseToken: "owner"}}
	os.EXPECT().UpdateData(gomock.Any(), interfaces.ActionExecutionResultsIndex, resultDocumentID("exec", 0), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ string, body any) (string, error) {
		script := body.(map[string]any)["script"].(map[string]any)
		params := script["params"].(map[string]any)
		require.Equal(t, "owner", params["token"])
		require.Equal(t, int64(3), params["revision"])
		require.Equal(t, int64(4), params["state"].(interfaces.ActionIndexSync).Revision)
		return "noop", nil
	})
	require.ErrorContains(t, s.SaveIndexSync(context.Background(), job), "lease was lost")
	require.Equal(t, int64(3), job.Sync.Revision)
}

func TestActionLogsServiceWithoutPendingIndexSync(t *testing.T) {
	t.Run("pending and failed intents hold their parent executions", func(t *testing.T) {
		s, os := newWriteTestService(t)
		s.resultsIndexReady.Store(true)
		os.EXPECT().SearchData(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).DoAndReturn(func(_ context.Context, _ string, query any) ([]interfaces.Hit, error) {
			q := query.(map[string]any)
			require.Equal(t, 2, q["size"])
			require.Equal(t, map[string]any{"field": "execution_id"}, q["collapse"])
			return []interfaces.Hit{{Source: map[string]any{"execution_id": "pending"}}}, nil
		})
		ids, err := s.withoutPendingIndexSync(context.Background(), []string{"pending", "completed"})
		require.NoError(t, err)
		require.Equal(t, []string{"completed"}, ids)
	})
	t.Run("lookup failure prevents retention deletion", func(t *testing.T) {
		s, os := newWriteTestService(t)
		s.resultsIndexReady.Store(true)
		os.EXPECT().SearchData(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).Return(nil, errors.New("search unavailable"))
		ids, err := s.withoutPendingIndexSync(context.Background(), []string{"exec"})
		require.Error(t, err)
		require.Nil(t, ids)
	})
}

func TestActionLogsServiceRetryIndexSync(t *testing.T) {
	s, os := newWriteTestService(t)
	s.syncMappingReady.Store(true)
	os.EXPECT().SearchData(gomock.Any(), interfaces.ActionExecutionResultsIndex, gomock.Any()).DoAndReturn(func(_ context.Context, _ string, query any) ([]interfaces.Hit, error) {
		filters := query.(map[string]any)["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
		require.Equal(t, map[string]any{"term": map[string]any{"kn_id": "kn"}}, filters[0])
		require.Equal(t, map[string]any{"term": map[string]any{"execution_id": "exec"}}, filters[1])
		return []interfaces.Hit{{Source: map[string]any{"kn_id": "kn", "execution_id": "exec", "seq": 2}}}, nil
	})
	os.EXPECT().UpdateData(gomock.Any(), interfaces.ActionExecutionResultsIndex, resultDocumentID("exec", 2), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ string, query any) (string, error) {
		require.Equal(t, retryIndexSyncScript, query.(map[string]any)["script"].(map[string]any)["source"])
		return "updated", nil
	})
	n, err := s.RetryIndexSync(context.Background(), "kn", "exec", 123)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}
