package projectorsvc

import (
	"context"
	"crypto/rand"
	"math/big"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

type WorkerOptions struct {
	Now          func() time.Time
	BatchSize    int
	LockDuration time.Duration
	MaxAttempts  uint32
	MaxRetryAge  time.Duration
	FullJitter   func(max time.Duration) time.Duration
	Metrics      icoremetrics.Recorder
}

type Worker struct {
	store   iprojectionoutbox.Store
	sink    iprojectionoutbox.Sink
	options WorkerOptions
}

type RunResult struct {
	Leased    int
	Delivered int
	Retried   int
	Dead      int
}

func NewWorker(store iprojectionoutbox.Store, sink iprojectionoutbox.Sink, options WorkerOptions) *Worker {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.BatchSize <= 0 {
		options.BatchSize = 100
	}
	if options.LockDuration <= 0 {
		options.LockDuration = 30 * time.Second
	}
	if options.MaxAttempts == 0 {
		options.MaxAttempts = 10
	}
	if options.MaxRetryAge <= 0 {
		options.MaxRetryAge = 24 * time.Hour
	}
	if options.FullJitter == nil {
		options.FullJitter = fullJitter
	}
	if options.Metrics == nil {
		options.Metrics = icoremetrics.Noop{}
	}
	return &Worker{store: store, sink: sink, options: options}
}

func (w *Worker) RunOnce(ctx context.Context) (RunResult, error) {
	items, err := w.store.Lease(ctx, w.options.BatchSize, w.options.LockDuration)
	if err != nil {
		return RunResult{}, err
	}
	result := RunResult{Leased: len(items)}
	var oldest time.Time
	for _, item := range items {
		if !item.CreatedAt.IsZero() && (oldest.IsZero() || item.CreatedAt.Before(oldest)) {
			oldest = item.CreatedAt
		}
		projectionErr := w.sink.Project(ctx, item)
		if projectionErr == nil {
			if err := w.store.MarkDelivered(ctx, item); err != nil {
				return result, err
			}
			result.Delivered++
			continue
		}
		retryWindowExpired := !item.CreatedAt.IsZero() &&
			!item.CreatedAt.Add(w.options.MaxRetryAge).After(w.options.Now().UTC())
		permanent := iprojectionoutbox.IsPermanent(projectionErr)
		attemptsExhausted := item.Attempts+1 >= w.options.MaxAttempts
		if permanent || attemptsExhausted || retryWindowExpired {
			failureClass := "retry_attempts_exhausted"
			if permanent {
				failureClass = "permanent_projection_error"
			} else if retryWindowExpired {
				failureClass = "retry_window_expired"
			}
			if err := w.store.MoveToDLQ(ctx, item, failureClass); err != nil {
				return result, err
			}
			result.Dead++
			continue
		}
		delay := w.options.FullJitter(retryDelay(item.Attempts + 1))
		if err := w.store.MarkRetry(ctx, item, "projection_failed", w.options.Now().UTC().Add(delay)); err != nil {
			return result, err
		}
		result.Retried++
	}
	if !oldest.IsZero() {
		lag := w.options.Now().UTC().Sub(oldest).Seconds()
		if lag < 0 {
			lag = 0
		}
		w.options.Metrics.Set(icoremetrics.ProjectionLagSeconds, lag)
	} else {
		w.options.Metrics.Set(icoremetrics.ProjectionLagSeconds, 0)
	}
	return result, nil
}

func (w *Worker) Drain(ctx context.Context) (RunResult, error) {
	var total RunResult
	for {
		result, err := w.RunOnce(ctx)
		total.Leased += result.Leased
		total.Delivered += result.Delivered
		total.Retried += result.Retried
		total.Dead += result.Dead
		if err != nil {
			return total, err
		}
		if result.Leased == 0 {
			return total, nil
		}
	}
}

func fullJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return max / 2
	}
	return time.Duration(value.Int64())
}

func retryDelay(attempt uint32) time.Duration {
	if attempt > 8 {
		attempt = 8
	}
	return time.Second * time.Duration(1<<attempt)
}
