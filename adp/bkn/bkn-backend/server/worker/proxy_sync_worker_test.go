// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

type proxySyncOutboxStub struct {
	completed   *interfaces.KNProxyOutboxEvent
	completeErr error
	retriedID   string
	retryDead   bool
	safeApplied bool
	renew       func(context.Context) (bool, error)
}

func (s *proxySyncOutboxStub) ListPlannedSources(context.Context, string,
	[]interfaces.KNProxyBindingRef) ([]interfaces.ProxyGrantSourceSpec, error) {
	return nil, nil
}
func (s *proxySyncOutboxStub) ListPlannedSnapshot(context.Context, string) ([]interfaces.ProxyGrantSourceSpec, error) {
	return nil, nil
}
func (s *proxySyncOutboxStub) StageDelta(context.Context, *sql.Tx, *interfaces.KNProxyOutboxEvent,
	string, []interfaces.KNProxyBindingRef, []interfaces.ProxyGrantSourceSpec, int64) (int64, error) {
	return 0, nil
}
func (s *proxySyncOutboxStub) ClaimNext(context.Context, string, int64, int64) (*interfaces.KNProxyOutboxEvent, error) {
	return nil, nil
}

func (s *proxySyncOutboxStub) RenewLease(ctx context.Context, _ string, _ string, _ int64, _ int64) (bool, error) {
	if s.renew != nil {
		return s.renew(ctx)
	}
	return true, nil
}
func (s *proxySyncOutboxStub) Complete(_ context.Context, event *interfaces.KNProxyOutboxEvent,
	_ string, _ int64) error {
	s.completed = event
	return s.completeErr
}
func (s *proxySyncOutboxStub) Retry(_ context.Context, eventID, _ string, _ string,
	_, _ int64, dead, safeApplied bool) error {
	s.retriedID, s.retryDead, s.safeApplied = eventID, dead, safeApplied
	return nil
}
func (s *proxySyncOutboxStub) CleanupTerminal(context.Context, int64, int) (int64, error) {
	return 0, nil
}

func TestProxySyncWorkerDefaultsAndUpgradeDisableSwitch(t *testing.T) {
	defaults := NewProxySyncWorker(nil, nil, nil)
	t.Cleanup(defaults.cancel)
	if !defaults.enabled || defaults.workers != 4 || defaults.lease != 60*time.Second {
		t.Fatalf("default worker settings = enabled %t, workers %d, lease %v",
			defaults.enabled, defaults.workers, defaults.lease)
	}
	disabled := NewProxySyncWorker(&common.AppSetting{}, nil, nil)
	t.Cleanup(disabled.cancel)
	if disabled.enabled {
		t.Fatal("zero-valued deployment configuration did not disable the worker")
	}
}

func TestProxySyncWorkerCompletesSuccessfulDelta(t *testing.T) {
	ctrl := gomock.NewController(t)
	proxy := bmock.NewMockManagedProxyAccess(ctrl)
	event := &interfaces.KNProxyOutboxEvent{
		ID: "event-1", KNID: "kn-1", ProxyAccountID: "proxy-1", GrantorID: "editor-1",
		Generation: 2, BaseVersion: "v1", TargetVersion: "v2",
	}
	proxy.EXPECT().SyncGrantDelta(gomock.Any(), "proxy-1", "editor-1", int64(2), "v1", "v2",
		event.Upserts, event.Removals).Return(interfaces.ProxyGrantSyncResult{}, nil)
	outbox := &proxySyncOutboxStub{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := &ProxySyncWorker{outbox: outbox, proxy: proxy, lease: time.Minute, maxAttempts: 12, ctx: ctx}
	w.process("pod-1", event)
	if outbox.completed != event || outbox.retriedID != "" {
		t.Fatalf("worker result = completed %#v, retried %q", outbox.completed, outbox.retriedID)
	}
}

func TestProxySyncWorkerMarksNonRetryableSafeFailureDead(t *testing.T) {
	ctrl := gomock.NewController(t)
	proxy := bmock.NewMockManagedProxyAccess(ctrl)
	event := &interfaces.KNProxyOutboxEvent{
		ID: "event-1", KNID: "kn-1", ProxyAccountID: "proxy-1", GrantorID: "editor-1",
		Generation: 2, BaseVersion: "v1", TargetVersion: "v2", AttemptCount: 1,
	}
	proxy.EXPECT().SyncGrantDelta(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(interfaces.ProxyGrantSyncResult{}, &interfaces.ManagedProxyStatusError{StatusCode: http.StatusBadRequest})
	outbox := &proxySyncOutboxStub{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := &ProxySyncWorker{outbox: outbox, proxy: proxy, lease: time.Minute, maxAttempts: 12, ctx: ctx}
	w.process("pod-1", event)
	if outbox.completed != nil || outbox.retriedID != event.ID || !outbox.retryDead {
		t.Fatalf("worker result = completed %#v, retried %q, dead %t",
			outbox.completed, outbox.retriedID, outbox.retryDead)
	}
}

func TestProxySyncWorkerCompletionFailureDoesNotConsumeSafeAttemptBudget(t *testing.T) {
	ctrl := gomock.NewController(t)
	proxy := bmock.NewMockManagedProxyAccess(ctrl)
	event := &interfaces.KNProxyOutboxEvent{
		ID: "event-1", KNID: "kn-1", ProxyAccountID: "proxy-1", GrantorID: "editor-1",
		Generation: 2, BaseVersion: "v1", TargetVersion: "v2", AttemptCount: 12,
	}
	proxy.EXPECT().SyncGrantDelta(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(interfaces.ProxyGrantSyncResult{}, nil)
	outbox := &proxySyncOutboxStub{completeErr: errors.New("database temporarily unavailable")}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := &ProxySyncWorker{outbox: outbox, proxy: proxy, lease: time.Minute, maxAttempts: 12, ctx: ctx}
	w.process("pod-1", event)
	if outbox.retriedID != event.ID || outbox.retryDead || !outbox.safeApplied {
		t.Fatalf("completion retry = id %q, dead %t, Safe applied %t",
			outbox.retriedID, outbox.retryDead, outbox.safeApplied)
	}
}

func TestProxySyncWorkerCompletingEventSkipsSafeReplay(t *testing.T) {
	ctrl := gomock.NewController(t)
	proxy := bmock.NewMockManagedProxyAccess(ctrl)
	event := &interfaces.KNProxyOutboxEvent{
		ID: "event-1", KNID: "kn-1", ProxyAccountID: "proxy-1", Generation: 2,
		BaseVersion: "v1", TargetVersion: "v2", SafeApplied: true,
	}
	outbox := &proxySyncOutboxStub{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := &ProxySyncWorker{outbox: outbox, proxy: proxy, lease: time.Minute, maxAttempts: 12, ctx: ctx}
	w.process("pod-1", event)
	if outbox.completed != event || outbox.retriedID != "" {
		t.Fatalf("completing event = completed %#v, retried %q", outbox.completed, outbox.retriedID)
	}
}

func TestLeaseRenewalCanceledAfterSafeCompletionIsNotReportedAsLeaseLoss(t *testing.T) {
	started := make(chan struct{})
	outbox := &proxySyncOutboxStub{renew: func(ctx context.Context) (bool, error) {
		close(started)
		<-ctx.Done()
		return false, ctx.Err()
	}}
	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)
	w := &ProxySyncWorker{outbox: outbox, lease: 3 * time.Millisecond, ctx: workerCtx}
	processCtx, processCancel := context.WithCancel(workerCtx)
	done := make(chan struct{})
	lost := make(chan error, 1)
	go w.renewLease(processCtx, processCancel, done, lost, "pod-1", "event-1")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("lease renewal did not start")
	}
	processCancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lease renewal did not stop")
	}
	select {
	case err := <-lost:
		t.Fatalf("normal completion reported lease loss: %v", err)
	default:
	}
}

func TestRetryableProxySyncErrorClassifiesSafeResponses(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{name: "network", err: errors.New("timeout"), retryable: true},
		{name: "request timeout", err: &interfaces.ManagedProxyStatusError{StatusCode: http.StatusRequestTimeout}, retryable: true},
		{name: "unspecified conflict", err: &interfaces.ManagedProxyStatusError{StatusCode: http.StatusConflict}, retryable: true},
		{name: "stale generation", err: &interfaces.ManagedProxyStatusError{StatusCode: http.StatusConflict,
			ErrorCode: interfaces.ManagedProxyErrorStaleSync}, retryable: false},
		{name: "snapshot conflict", err: &interfaces.ManagedProxyStatusError{StatusCode: http.StatusConflict,
			ErrorCode: interfaces.ManagedProxyErrorSnapshotConflict}, retryable: false},
		{name: "rate limited", err: &interfaces.ManagedProxyStatusError{StatusCode: http.StatusTooManyRequests}, retryable: true},
		{name: "safe unavailable", err: &interfaces.ManagedProxyStatusError{StatusCode: http.StatusServiceUnavailable}, retryable: true},
		{name: "invalid payload", err: &interfaces.ManagedProxyStatusError{StatusCode: http.StatusBadRequest}, retryable: false},
		{name: "missing proxy", err: &interfaces.ManagedProxyStatusError{StatusCode: http.StatusNotFound}, retryable: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := retryableProxySyncError(test.err); got != test.retryable {
				t.Fatalf("retryableProxySyncError() = %t, want %t", got, test.retryable)
			}
		})
	}
}

func TestProxySyncErrorSummaryFitsPersistenceColumn(t *testing.T) {
	summary := proxySyncErrorSummary(errors.New(strings.Repeat("错", 1200)))
	if len([]rune(summary)) != 1024 {
		t.Fatalf("summary length = %d, want 1024", len([]rune(summary)))
	}
}

func TestProxySyncRetryDelayIsBoundedByDocumentedBackoff(t *testing.T) {
	for attempt, base := range []time.Duration{5 * time.Second, 5 * time.Second, 15 * time.Second, time.Minute, 5 * time.Minute} {
		delay := proxySyncRetryDelay(attempt)
		if delay < base || delay > base+base/5 {
			t.Fatalf("attempt %d delay = %v, want [%v, %v]", attempt, delay, base, base+base/5)
		}
	}
}
