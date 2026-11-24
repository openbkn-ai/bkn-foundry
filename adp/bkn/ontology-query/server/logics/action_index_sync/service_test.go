// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_index_sync

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/stretchr/testify/require"
)

type testStore struct {
	jobs    []interfaces.ActionIndexSyncJob
	claimed bool
	saves   int
	saveErr error
}

func (s *testStore) ListIndexSync(context.Context, int64, int) ([]interfaces.ActionIndexSyncJob, error) {
	return s.jobs, nil
}
func (s *testStore) ClaimIndexSync(_ context.Context, job *interfaces.ActionIndexSyncJob, token string, now, until int64) (bool, error) {
	if !s.claimed {
		return false, nil
	}
	job.Sync.LeaseToken = token
	job.Sync.LeaseUntil = until
	return true, nil
}
func (s *testStore) SaveIndexSync(context.Context, *interfaces.ActionIndexSyncJob) error {
	s.saves++
	return s.saveErr
}
func (s *testStore) RetryIndexSync(context.Context, string, string, int64) (int, error) {
	return 0, nil
}

type testBackend struct {
	creates   int
	reads     int
	retries   int
	statuses  map[string]string
	createErr error
	readErr   error
	caller    interfaces.AccountInfo
}

func (b *testBackend) CreateKeyBuild(ctx context.Context, _ string, _ []map[string]any) (string, error) {
	b.creates++
	b.caller, _ = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return "task", b.createErr
}
func (b *testBackend) GetKeyBuild(context.Context, string) (string, error) {
	b.reads++
	return b.statuses["task"], b.readErr
}
func (b *testBackend) RetryKeyBuild(context.Context, string) error { b.retries++; return nil }

func testJob() interfaces.ActionIndexSyncJob {
	return interfaces.ActionIndexSyncJob{KNID: "kn", ExecutionID: "exec", Seq: 0, Sync: &interfaces.ActionIndexSync{Status: "pending", Executor: interfaces.AccountInfo{ID: "alice", Type: "user"}, Tasks: []interfaces.ActionIndexSyncTask{{ResourceID: "brands", Keys: []map[string]any{{"id": "662"}}}}}}
}

func TestServiceRunOnce(t *testing.T) {
	for _, tc := range []struct{ name, status, want string }{
		{"accepted is not searchable", "pending", "submitted"},
		{"running is not searchable", "running", "submitted"},
		{"completed is searchable", "completed", "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := testJob()
			store := &testStore{jobs: []interfaces.ActionIndexSyncJob{job}, claimed: true}
			backend := &testBackend{statuses: map[string]string{"task": tc.status}}
			svc := New(store, backend)
			svc.now = func() time.Time { return time.Unix(1000, 0) }
			require.NoError(t, svc.RunOnce(context.Background()))
			require.Equal(t, tc.want, job.Sync.Status)
			require.Equal(t, 1, backend.creates)
			require.Equal(t, job.Sync.Executor, backend.caller)
			require.Equal(t, int64(0), job.Sync.LeaseUntil)
			require.Equal(t, "task", job.Sync.Tasks[0].TaskID)
			require.Equal(t, 2, store.saves)
		})
	}
	t.Run("recovery reuses a persisted task after restart", func(t *testing.T) {
		job := testJob()
		job.Sync.Tasks[0].TaskID = "task"
		store := &testStore{jobs: []interfaces.ActionIndexSyncJob{job}, claimed: true}
		backend := &testBackend{statuses: map[string]string{"task": "completed"}}
		require.NoError(t, New(store, backend).RunOnce(context.Background()))
		require.Equal(t, 0, backend.creates)
		require.Equal(t, "completed", job.Sync.Status)
	})
	t.Run("a second replica cannot dispatch an unclaimed result", func(t *testing.T) {
		job := testJob()
		store := &testStore{jobs: []interfaces.ActionIndexSyncJob{job}, claimed: false}
		backend := &testBackend{}
		require.NoError(t, New(store, backend).RunOnce(context.Background()))
		require.Zero(t, backend.creates)
		require.Zero(t, backend.reads)
	})
	t.Run("resource permission failure preserves business outcome and stops retrying", func(t *testing.T) {
		job := testJob()
		job.Sync.Attempts = 4
		store := &testStore{jobs: []interfaces.ActionIndexSyncJob{job}, claimed: true}
		backend := &testBackend{createErr: errors.New("403 private source information")}
		require.NoError(t, New(store, backend).RunOnce(context.Background()))
		require.Equal(t, "failed", job.Sync.Status)
		require.Equal(t, 5, job.Sync.Attempts)
		require.NotContains(t, job.Sync.Error, "private")
	})
	t.Run("failed Vega task is restarted without a new business action", func(t *testing.T) {
		job := testJob()
		job.Sync.Tasks[0].TaskID = "task"
		store := &testStore{jobs: []interfaces.ActionIndexSyncJob{job}, claimed: true}
		backend := &testBackend{statuses: map[string]string{"task": "failed"}}
		require.NoError(t, New(store, backend).RunOnce(context.Background()))
		require.Equal(t, 1, backend.retries)
		require.Zero(t, backend.creates)
		require.Equal(t, "retrying", job.Sync.Status)
	})
	t.Run("accepted task must be recorded before another submission", func(t *testing.T) {
		job := testJob()
		job.Sync.Tasks = append(job.Sync.Tasks, interfaces.ActionIndexSyncTask{ResourceID: "products", Keys: []map[string]any{{"id": "p"}}})
		store := &testStore{jobs: []interfaces.ActionIndexSyncJob{job}, claimed: true, saveErr: errors.New("store unavailable")}
		backend := &testBackend{}
		require.NoError(t, New(store, backend).RunOnce(context.Background()))
		require.Equal(t, 1, backend.creates)
		require.Zero(t, backend.reads)
	})
	t.Run("cancellation does not dispatch anything", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		store := &testStore{jobs: []interfaces.ActionIndexSyncJob{testJob()}, claimed: true}
		backend := &testBackend{}
		require.ErrorIs(t, New(store, backend).RunOnce(ctx), context.Canceled)
		require.Zero(t, backend.creates)
	})
}

func TestServiceStoppedKeyBuildRequiresExplicitRetry(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			job := testJob()
			job.Sync.Tasks[0].TaskID = "task"
			if explicit {
				job.Sync.Tasks[0].Status = "retry_requested"
			}
			store := &testStore{jobs: []interfaces.ActionIndexSyncJob{job}, claimed: true}
			backend := &testBackend{statuses: map[string]string{"task": "stopped"}}
			require.NoError(t, New(store, backend).RunOnce(context.Background()))
			if explicit {
				require.Equal(t, 1, backend.retries)
				require.Equal(t, "retrying", job.Sync.Status)
			} else {
				require.Zero(t, backend.retries)
				require.Equal(t, "failed", job.Sync.Status)
			}
		})
	}
}
