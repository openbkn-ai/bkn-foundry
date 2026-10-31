// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package projectorsvc

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

type WorkerOptions struct {
	// Production bounds SQL claims and answer work independently. Zero preserves
	// existing embedded callers; non-answer handlers retain their original context.
	LeaseIOTimeout              time.Duration
	AnswerTaskIOTimeout         time.Duration
	AnswerAckIOTimeout          time.Duration
	AnswerArtifactHandler       AnswerArtifactHandler
	RevisionInputHandler        RevisionInputHandler
	Now                         func() time.Time
	BatchSize                   int
	LockDuration                time.Duration
	MaxAttempts                 uint32 // Ordinary/unknown errors; recognized search dependency failures use MaxRetryAge.
	MaxRetryAge                 time.Duration
	FullJitter                  func(max time.Duration) time.Duration
	Metrics                     icoremetrics.Recorder
	HistoricalProvenanceHandler HistoricalProvenanceHandler
}

const historicalProvenanceBuildRequested = "historical_provenance.build_requested"

// HistoricalProvenanceHandler consumes the enterprise-only provenance build
// event in the same leased Core outbox worker. It is deliberately separate
// from the OpenSearch projection sink so one delivery cannot be consumed by
// both destinations.
type HistoricalProvenanceHandler interface {
	HandleHistoricalProvenance(context.Context, iprojectionoutbox.Item) error
}

type AnswerArtifactHandler interface {
	HandleAnswerArtifact(context.Context, iprojectionoutbox.Item) error
	MarkAnswerArtifactDead(context.Context, iprojectionoutbox.Item) error
}

type RevisionInputHandler interface {
	HandleRevisionInput(context.Context, iprojectionoutbox.Item) error
}

type Worker struct {
	store      iprojectionoutbox.Store
	sink       iprojectionoutbox.Sink
	provenance HistoricalProvenanceHandler
	options    WorkerOptions
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
	return &Worker{store: store, sink: sink, provenance: options.HistoricalProvenanceHandler, options: options}
}

func (w *Worker) RunOnce(ctx context.Context) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	leaseCtx, cancelLease := ioContext(ctx, w.options.LeaseIOTimeout)
	items, err := w.store.Lease(leaseCtx, w.options.BatchSize, w.options.LockDuration)
	cancelLease()
	if err != nil {
		return RunResult{}, err
	}
	result := RunResult{Leased: len(items)}
	var oldest time.Time
	for _, item := range items {
		if !item.CreatedAt.IsZero() && (oldest.IsZero() || item.CreatedAt.Before(oldest)) {
			oldest = item.CreatedAt
		}

		itemResult, err := w.runItem(ctx, item)
		result.Delivered += itemResult.Delivered
		result.Retried += itemResult.Retried
		result.Dead += itemResult.Dead
		if err != nil {
			return result, err
		}
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

// runItem cancels the task deadline before creating an independent, short ACK
// context from the still-live worker. An exhausted task may already have stored
// its artifact or SQL state; unchanged lease fencing and idempotency handle that
// uncertain outcome. Parent cancellation leaves the lease for recovery.
func (w *Worker) runItem(ctx context.Context, item iprojectionoutbox.Item) (RunResult, error) {
	result := RunResult{}
	taskCtx, cancelTask := ctx, func() {}
	if item.EventType == "artifact.persist_requested" {
		taskCtx, cancelTask = ioContext(ctx, w.options.AnswerTaskIOTimeout)
	}
	var projectionErr error
	if item.PayloadDeferred && !item.PayloadTooLarge {
		if loader, ok := w.store.(iprojectionoutbox.PayloadLoader); ok {
			item.Payload, projectionErr = loader.LoadPayload(taskCtx, item)
		} else {
			projectionErr = errors.New("deferred outbox payload loader is unavailable")
		}
	}
	if projectionErr == nil {
		projectionErr = taskCtx.Err()
	}
	if projectionErr == nil {
		projectionErr = w.project(taskCtx, item)
	}
	if projectionErr == nil {
		projectionErr = taskCtx.Err()
	}
	cancelTask()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if errors.Is(projectionErr, iprojectionoutbox.ErrLeaseLost) {
		return result, nil
	}
	ackCtx, cancelACK := ctx, func() {}
	if item.EventType == "artifact.persist_requested" {
		ackCtx, cancelACK = ioContext(ctx, w.options.AnswerAckIOTimeout)
	}
	defer cancelACK()
	if projectionErr == nil {
		if err := w.store.MarkDelivered(ackCtx, item); err != nil {
			if errors.Is(err, iprojectionoutbox.ErrLeaseLost) {
				return result, nil
			}
			return result, err
		}
		result.Delivered++
		return result, nil
	}
	w.options.Metrics.Increment(icoremetrics.ProjectionAttemptErrorsTotal)
	retryUntil, recoverable, windowErr := w.recoveryDeadline(ctx, item, projectionErr)
	if windowErr != nil {
		logProjectionFailure(ctx, item, projectionErr, "recovery_window_unavailable")
		return result, windowErr
	}
	retryWindowExpired := !retryUntil.IsZero() && !retryUntil.After(w.options.Now().UTC())
	permanent := iprojectionoutbox.IsPermanent(projectionErr)
	attemptsExhausted := !recoverable && uint64(item.Attempts)+1 >= uint64(w.options.MaxAttempts)
	if permanent || attemptsExhausted || retryWindowExpired {
		failureClass := "retry_attempts_exhausted"
		if permanent {
			failureClass = "permanent_projection_error"
		} else if retryWindowExpired {
			failureClass = "retry_window_expired"
		}
		logProjectionFailure(ctx, item, projectionErr, failureClass)
		if item.EventType == "artifact.persist_requested" && w.options.AnswerArtifactHandler != nil {
			if err := w.options.AnswerArtifactHandler.MarkAnswerArtifactDead(ackCtx, item); err != nil {
				if errors.Is(err, iprojectionoutbox.ErrLeaseLost) {
					return result, nil
				}
				if !iprojectionoutbox.IsPermanent(err) {
					return result, err
				}
			}
		}
		var deadErr error
		failureCode := projectionFailureCode(projectionErr)
		if diagnostic, ok := w.store.(iprojectionoutbox.DiagnosticDLQStore); ok && failureCode != "projection_failed" {
			deadErr = diagnostic.MoveToDLQWithDiagnostic(ackCtx, item, failureClass, failureCode)
		} else {
			deadErr = w.store.MoveToDLQ(ackCtx, item, failureClass)
		}
		if err := deadErr; err != nil {
			if errors.Is(err, iprojectionoutbox.ErrLeaseLost) {
				return result, nil
			}
			return result, err
		}
		w.options.Metrics.Increment(icoremetrics.ProjectionDeadTotal)
		result.Dead++
		return result, nil
	}
	delay := w.options.FullJitter(retryDelay(item.Attempts + 1))
	if recoverable && delay < time.Second {
		delay = time.Second
	}
	retryAt := w.options.Now().UTC().Add(delay)
	if recoverable && retryAt.After(retryUntil) {
		retryAt = retryUntil
	}
	logProjectionFailure(ctx, item, projectionErr, "retry")
	failureKind := projectionFailureCode(projectionErr)
	if err := w.store.MarkRetry(ackCtx, item, failureKind, retryAt); err != nil {
		if errors.Is(err, iprojectionoutbox.ErrLeaseLost) {
			return result, nil
		}
		return result, err
	}
	w.options.Metrics.Increment(icoremetrics.ProjectionRetriesTotal)
	result.Retried++
	return result, nil
}

func ioContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (w *Worker) project(ctx context.Context, item iprojectionoutbox.Item) error {
	if item.PayloadTooLarge {
		return iprojectionoutbox.Permanent(errors.New("outbox item exceeds payload budget"))
	}
	if item.EventType == "artifact.persist_requested" {
		if w.options.AnswerArtifactHandler == nil {
			return errors.New("answer artifact handler is not assembled")
		}
		return w.options.AnswerArtifactHandler.HandleAnswerArtifact(ctx, item)
	}
	if item.EventType == "revision.input.sealed" {
		if w.options.RevisionInputHandler == nil {
			return errors.New("revision input handler is not assembled")
		}
		return w.options.RevisionInputHandler.HandleRevisionInput(ctx, item)
	}
	if item.EventType == historicalProvenanceBuildRequested {
		if w.provenance == nil {
			return errors.New("historical provenance handler is not assembled")
		}
		return w.provenance.HandleHistoricalProvenance(ctx, item)
	}
	return w.sink.Project(ctx, item)
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
