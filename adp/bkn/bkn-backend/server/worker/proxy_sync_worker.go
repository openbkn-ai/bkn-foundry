// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

const (
	defaultProxySyncWorkers         = 4
	defaultProxySyncPollInterval    = time.Second
	defaultProxySyncLease           = 60 * time.Second
	defaultProxySyncMaxAttempts     = 12
	defaultProxyOutboxRetention     = 7 * 24 * time.Hour
	defaultProxyOutboxCleanupPeriod = time.Hour
	proxyOutboxCleanupBatch         = 500
	proxyOutboxCleanupRunLimit      = 5000
)

// ProxySyncWorker drains transactional grant deltas with a fixed-size pool.
// Database queue-head fencing ensures that one knowledge network never has two
// generations in flight, including across several BKN instances.
type ProxySyncWorker struct {
	outbox interfaces.KNProxyOutboxAccess
	proxy  interfaces.ManagedProxyAccess
	owner  string

	enabled       bool
	workers       int
	pollInterval  time.Duration
	lease         time.Duration
	maxAttempts   int
	cleanupPeriod time.Duration
	retention     time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
	stop          chan struct{}
	startOnce     sync.Once
	stopOnce      sync.Once
	wg            sync.WaitGroup
}

// NewProxySyncWorker creates one per-process worker pool.
func NewProxySyncWorker(appSetting *common.AppSetting, outbox interfaces.KNProxyOutboxAccess,
	proxy interfaces.ManagedProxyAccess) *ProxySyncWorker {
	hostname, _ := os.Hostname()
	if len(hostname) > 64 {
		hostname = hostname[:64]
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	w := &ProxySyncWorker{
		outbox: outbox, proxy: proxy, owner: fmt.Sprintf("%s-%d", hostname, os.Getpid()),
		enabled: true, workers: defaultProxySyncWorkers, pollInterval: defaultProxySyncPollInterval,
		lease: defaultProxySyncLease, maxAttempts: defaultProxySyncMaxAttempts,
		cleanupPeriod: defaultProxyOutboxCleanupPeriod, retention: defaultProxyOutboxRetention,
		ctx: workerCtx, cancel: cancel, stop: make(chan struct{}),
	}
	if appSetting != nil {
		setting := appSetting.ServerSetting
		w.enabled = setting.ProxySyncWorkerEnabled
		if setting.ProxySyncWorkerCount > 0 {
			w.workers = setting.ProxySyncWorkerCount
		}
		if setting.ProxySyncPollInterval > 0 {
			w.pollInterval = time.Duration(setting.ProxySyncPollInterval) * time.Second
		}
		if setting.ProxySyncLeaseSeconds > 0 {
			w.lease = time.Duration(setting.ProxySyncLeaseSeconds) * time.Second
		}
		if setting.ProxySyncMaxAttempts > 0 {
			w.maxAttempts = setting.ProxySyncMaxAttempts
		}
		if setting.ProxyOutboxCleanupInterval > 0 {
			w.cleanupPeriod = time.Duration(setting.ProxyOutboxCleanupInterval) * time.Second
		}
	}
	return w
}

// Start runs the fixed worker pool and the independent cleanup scheduler.
func (w *ProxySyncWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(w.start)
}

func (w *ProxySyncWorker) start() {
	if w == nil {
		return
	}
	if !w.enabled {
		logger.Info("ProxySyncWorker is disabled by configuration")
		return
	}
	if w.outbox == nil || w.proxy == nil {
		logger.Warn("ProxySyncWorker is disabled because a dependency is unavailable")
		return
	}
	logger.Infof("ProxySyncWorker starting with owner %s and %d slots", w.owner, w.workers)
	for i := 0; i < w.workers; i++ {
		w.wg.Add(1)
		go w.runSlot(i)
	}
	w.wg.Add(1)
	go w.runCleanup()
}

// Stop waits for polling loops and active Safe calls to finish or observe cancellation.
func (w *ProxySyncWorker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		w.cancel()
		close(w.stop)
	})
	w.wg.Wait()
}

func (w *ProxySyncWorker) runSlot(slot int) {
	defer w.wg.Done()
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		worked := w.claimAndProcess(slot)
		if worked {
			continue
		}
		select {
		case <-ticker.C:
		case <-w.stop:
			return
		}
	}
}

func (w *ProxySyncWorker) claimAndProcess(slot int) bool {
	now := time.Now()
	owner := fmt.Sprintf("%s-%d", w.owner, slot)
	event, err := w.outbox.ClaimNext(w.ctx, owner, now.UnixMilli(), now.Add(w.lease).UnixMilli())
	if err != nil {
		logger.Errorf("ProxySyncWorker slot %d failed to claim an event: %v", slot, err)
		return false
	}
	if event == nil {
		return false
	}
	w.process(owner, event)
	return true
}

func (w *ProxySyncWorker) process(owner string, event *interfaces.KNProxyOutboxEvent) {
	ctx, cancel := context.WithCancel(w.ctx)
	defer cancel()
	renewDone := make(chan struct{})
	leaseLost := make(chan error, 1)
	go w.renewLease(ctx, cancel, renewDone, leaseLost, owner, event.ID)
	var syncErr error
	if !event.SafeApplied {
		_, syncErr = w.proxy.SyncGrantDelta(ctx, event.ProxyAccountID, event.GrantorID, event.Generation,
			event.BaseVersion, event.TargetVersion, event.Upserts, event.Removals)
	}
	cancel()
	<-renewDone
	select {
	case err := <-leaseLost:
		logger.Warnf("ProxySyncWorker lost event %s for kn %s proxy %s generation %d lease: %v",
			event.ID, event.KNID, event.ProxyAccountID, event.Generation, err)
		return
	default:
	}
	if syncErr == nil {
		persistCtx, persistCancel := w.persistenceContext()
		completeErr := w.outbox.Complete(persistCtx, event, owner, time.Now().UnixMilli())
		persistCancel()
		if completeErr == nil {
			logger.Infof("ProxySyncWorker completed event %s for kn %s proxy %s generation %d",
				event.ID, event.KNID, event.ProxyAccountID, event.Generation)
			return
		}
		// Safe already accepted this exact fenced delta. Persist a separate
		// completing state so later claims skip Safe and local database failures
		// cannot consume the downstream delivery budget or mark an actually
		// synchronized mapping failed.
		now := time.Now()
		persistCtx, persistCancel = w.persistenceContext()
		defer persistCancel()
		if err := w.outbox.Retry(persistCtx, event.ID, owner, proxySyncErrorSummary(completeErr),
			now.Add(proxySyncRetryDelay(0)).UnixMilli(), now.UnixMilli(), false, true); err != nil {
			logger.Errorf("ProxySyncWorker failed to persist event %s completion retry: %v", event.ID, err)
			return
		}
		logger.Warnf("ProxySyncWorker will retry local completion for event %s after Safe accepted generation %d: %v",
			event.ID, event.Generation, completeErr)
		return
	}
	dead := event.AttemptCount >= w.maxAttempts || !retryableProxySyncError(syncErr)
	now := time.Now()
	next := now.Add(proxySyncRetryDelay(event.AttemptCount))
	if dead {
		next = now
	}
	persistCtx, persistCancel := w.persistenceContext()
	defer persistCancel()
	if err := w.outbox.Retry(persistCtx, event.ID, owner, proxySyncErrorSummary(syncErr),
		next.UnixMilli(), now.UnixMilli(), dead, false); err != nil {
		logger.Errorf("ProxySyncWorker failed to persist event %s failure: %v", event.ID, err)
		return
	}
	if dead {
		logger.Errorf("ProxySyncWorker marked event %s for kn %s proxy %s generation %d dead after %d attempts: %v",
			event.ID, event.KNID, event.ProxyAccountID, event.Generation, event.AttemptCount, syncErr)
		return
	}
	logger.Warnf("ProxySyncWorker will retry event %s for kn %s proxy %s generation %d after attempt %d: %v",
		event.ID, event.KNID, event.ProxyAccountID, event.Generation, event.AttemptCount, syncErr)
}

func (w *ProxySyncWorker) renewLease(ctx context.Context, cancel context.CancelFunc, done chan<- struct{},
	leaseLost chan<- error, owner, eventID string) {
	defer close(done)
	interval := w.lease / 3
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			renewCtx, renewCancel := context.WithTimeout(ctx, min(interval, 10*time.Second))
			ok, err := w.outbox.RenewLease(renewCtx, eventID, owner,
				now.UnixMilli(), now.Add(w.lease).UnixMilli())
			renewCancel()
			// The Safe call has finished (or the process is stopping). Its caller
			// intentionally cancels ctx to join this goroutine; a renewal aborted by
			// that cancellation is not evidence that another worker owns the event.
			if ctx.Err() != nil {
				return
			}
			if err != nil || !ok {
				if err == nil {
					err = errors.New("lease owner changed or lease expired")
				}
				leaseLost <- err
				cancel()
				return
			}
		}
	}
}

func (w *ProxySyncWorker) persistenceContext() (context.Context, context.CancelFunc) {
	timeout := 10 * time.Second
	if w.lease > 0 && w.lease/3 < timeout {
		timeout = w.lease / 3
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	// Completion and retry records get a short shutdown grace period after a
	// Safe call returns. The timeout prevents Stop from hanging on a database
	// outage while avoiding an unnecessary full lease wait before another pod
	// can recover the event.
	return context.WithTimeout(context.Background(), timeout)
}

func (w *ProxySyncWorker) runCleanup() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.cleanupPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.cleanup()
		case <-w.stop:
			return
		}
	}
}

func (w *ProxySyncWorker) cleanup() {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
	defer cancel()
	remaining := proxyOutboxCleanupRunLimit
	deleted := int64(0)
	for remaining > 0 {
		batch := min(proxyOutboxCleanupBatch, remaining)
		count, err := w.outbox.CleanupTerminal(ctx, time.Now().Add(-w.retention).UnixMilli(), batch)
		if err != nil {
			logger.Errorf("ProxySyncWorker outbox cleanup failed after deleting %d rows: %v", deleted, err)
			return
		}
		deleted += count
		remaining -= int(count)
		if count < int64(batch) {
			break
		}
	}
	if deleted > 0 {
		logger.Infof("ProxySyncWorker outbox cleanup deleted %d retained terminal events in %s",
			deleted, time.Since(startedAt))
	}
}

func retryableProxySyncError(err error) bool {
	var statusErr *interfaces.ManagedProxyStatusError
	if errors.As(err, &statusErr) {
		if statusErr.ErrorCode == interfaces.ManagedProxyErrorStaleSync ||
			statusErr.ErrorCode == interfaces.ManagedProxyErrorSnapshotConflict {
			return false
		}
		return statusErr.StatusCode == 408 || statusErr.StatusCode == 409 ||
			statusErr.StatusCode == 429 || statusErr.StatusCode >= 500
	}
	return true
}

func proxySyncErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	const maxCharacters = 1024
	characters := []rune(err.Error())
	if len(characters) <= maxCharacters {
		return string(characters)
	}
	return string(characters[:maxCharacters])
}

func proxySyncRetryDelay(attempt int) time.Duration {
	var base time.Duration
	switch attempt {
	case 0, 1:
		base = 5 * time.Second
	case 2:
		base = 15 * time.Second
	case 3:
		base = time.Minute
	default:
		base = 5 * time.Minute
	}
	// A small positive jitter prevents every BKN replica from retrying at once.
	return base + time.Duration(rand.Int64N(int64(base/5)+1))
}
