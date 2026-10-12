package evidencepublisher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"
)

const publisherHeartbeatInterval = 10 * time.Second

// PublisherRuntime wires the frozen Publisher data plane to the internal
// policy/control-plane inputs. Business calls only consult its validated local
// snapshot; they never wait for policy, heartbeat, Kafka, or ACK I/O.
type PublisherRuntime struct {
	publisher     *Publisher
	policy        *PolicyClient
	configuration *ConfigurationClient
	control       *ControlClient
	now           func() time.Time

	refreshMu              sync.Mutex
	mu                     sync.Mutex
	snapshot               PolicySnapshot
	hasPolicy              bool
	admitting              bool
	lastError              error
	ackRevision            uint64
	ackSequence            uint64
	ackNotExpectedRevision uint64
}

type PublisherRuntimeConfig struct {
	Publisher     Config
	Sender        Sender
	Policy        *PolicyClient
	Configuration *ConfigurationClient
	Control       *ControlClient
	Now           func() time.Time
}

func NewPublisherRuntime(_ context.Context, config PublisherRuntimeConfig) (*PublisherRuntime, error) {
	if config.Policy == nil || config.Configuration == nil || config.Control == nil {
		return nil, errors.New("evidence publisher runtime control clients are required")
	}
	// Runtime records always receive the validated snapshot revision at
	// TryPublish/Flush time. Keep the legacy Publisher constructor's required
	// value internal so a workload has no static policy-revision setting.
	if config.Publisher.CapturePolicyRevision == "" {
		config.Publisher.CapturePolicyRevision = "1"
	}
	publisher, err := New(config.Publisher, config.Sender)
	if err != nil {
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	runtime := &PublisherRuntime{publisher: publisher, policy: config.Policy, configuration: config.Configuration, control: config.Control, now: now}
	return runtime, nil
}

// CaptureDisabled reports an intentional, validated policy disable without I/O.
// A disabled snapshot remains closed until a newer validated snapshot enables it;
// expiry or transport failure must never silently re-enable capture.
func (r *PublisherRuntime) CaptureDisabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hasPolicy && r.snapshot.EvidenceAdmission == "disabled"
}

// TryPublish remains the business-facing non-blocking entrypoint. It only
// accepts a fresh, validated enabled snapshot and stamps its exact revision on
// the record before the bounded in-memory queue admission.
func (r *PublisherRuntime) TryPublish(event Event) PublishResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.hasPolicy || !r.admitting || !r.now().Before(r.snapshot.ExpiresAt) {
		if previous, ok := r.publisher.replayAdmission(event); ok {
			return previous
		}
		if r.hasPolicy && r.snapshot.EvidenceAdmission == "disabled" {
			return PublishResult{Disposition: Dropped, Reason: ReasonPublisherClosing}
		}
		r.admitting = false
		return PublishResult{Disposition: Dropped, Reason: ReasonPublisherUnavailable}
	}
	return r.publisher.TryPublishForPolicyRevision(event, strconv.FormatUint(r.snapshot.Revision, 10))
}

// Refresh obtains and validates a new internal snapshot, sends the per-instance
// heartbeat, then accounts the bounded queue through the frozen
// configuration/ACK flow before opening enabled admission. A failed ACK
// lookup keeps Evidence closed without blocking business work.
func (r *PublisherRuntime) Refresh(ctx context.Context) error {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	snapshot, err := r.policy.Read(ctx)
	if err != nil {
		r.disableWithError(err)
		r.flushCachedQueue(ctx)
		return err
	}
	r.mu.Lock()
	keepAdmission := r.hasPolicy && r.admitting && r.snapshot.Revision == snapshot.Revision && r.snapshot.EvidenceAdmission == "enabled" && snapshot.EvidenceAdmission == "enabled"
	r.snapshot = snapshot
	r.hasPolicy = true
	// A new revision stays closed through heartbeat and ACK. A healthy enabled
	// revision need not close on every repeated configuration read.
	r.admitting = keepAdmission
	r.lastError = nil
	r.mu.Unlock()
	if err := r.control.Heartbeat(ctx, r.publisher.config.WorkloadIdentity, r.publisher.config.ProcessBootID, snapshot.Revision); err != nil {
		r.disableWithError(err)
		return err
	}
	if snapshot.EvidenceAdmission == "disabled" {
		if _, err := r.drainAndAcknowledge(ctx, snapshot.Revision, false, true); err != nil {
			r.setError(err)
			return err
		}
		return nil
	}
	r.mu.Lock()
	ackNotExpected := r.ackNotExpectedRevision == snapshot.Revision
	alreadyAcknowledged := r.ackRevision == snapshot.Revision
	if ackNotExpected || alreadyAcknowledged {
		r.admitting = true
	}
	r.mu.Unlock()
	if ackNotExpected || alreadyAcknowledged {
		_ = r.publisher.FlushForPolicyRevision(ctx, strconv.FormatUint(snapshot.Revision, 10))
		return nil
	}
	if _, err := r.drainAndAcknowledge(ctx, snapshot.Revision, false, false); err != nil {
		r.setError(err)
		if errors.Is(err, errPublisherAcknowledgementNotExpected) {
			// A newly joined instance may be outside the operation's frozen ACK
			// set. It cannot converge the operation, but its internal policy and
			// successful heartbeat still permit live Evidence admission.
			r.mu.Lock()
			r.ackNotExpectedRevision = snapshot.Revision
			r.admitting = true
			r.mu.Unlock()
		}
		return err
	}
	r.mu.Lock()
	r.admitting = true
	r.mu.Unlock()
	return nil
}

// Run keeps refreshing after transient control-plane failures. Such failures
// fail evidence admission closed, but do not stop or block business work.
func (r *PublisherRuntime) Run(ctx context.Context) error {
	ticker := time.NewTicker(publisherHeartbeatInterval)
	defer ticker.Stop()
	return r.run(ctx, ticker.C)
}

func (r *PublisherRuntime) run(ctx context.Context, ticks <-chan time.Time) error {
	lastFailure := ""
	refresh := func() {
		err := r.Refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if failure := err.Error(); failure != lastFailure {
				log.Printf("BKN Trace evidence publisher producer_id=%s refresh failed: %s", r.publisher.config.ProducerID, failure)
				lastFailure = failure
			}
		} else if lastFailure != "" {
			log.Printf("BKN Trace evidence publisher producer_id=%s refresh recovered", r.publisher.config.ProducerID)
			lastFailure = ""
		}
	}
	// Do not make service construction depend on the control plane. The first
	// attempt happens as soon as the lifecycle goroutine starts; transient
	// failures leave admission closed and are retried on the normal heartbeat.
	refresh()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks:
			refresh()
		}
	}
}

// Close rejects new events, performs the bounded final disposition, and only
// ACKs if configuration names an active operation at the validated revision.
func (r *PublisherRuntime) Close(ctx context.Context) (DrainResult, error) {
	r.mu.Lock()
	r.admitting = false
	hasPolicy := r.hasPolicy
	revision := r.snapshot.Revision
	r.mu.Unlock()
	if !hasPolicy {
		return r.publisher.Close(ctx), errors.New("publisher runtime has no validated policy")
	}
	return r.drainAndAcknowledge(ctx, revision, true, true)
}

func (r *PublisherRuntime) LastRefreshError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastError
}

// Flush performs only the bounded Kafka disposition for the current validated
// revision. It is safe for a background transport loop and never reads the
// control plane or sends an operation acknowledgement.
func (r *PublisherRuntime) Flush(ctx context.Context) DrainResult {
	r.mu.Lock()
	hasPolicy := r.hasPolicy
	revision := r.snapshot.Revision
	r.mu.Unlock()
	if !hasPolicy {
		return DrainResult{}
	}
	return r.publisher.FlushForPolicyRevision(ctx, strconv.FormatUint(revision, 10))
}

// UnderlyingPublisher exposes only the bounded transport owner for service
// lifecycle integration; business callers must use PublisherRuntime.TryPublish.
func (r *PublisherRuntime) UnderlyingPublisher() *Publisher { return r.publisher }

func (r *PublisherRuntime) disableWithError(err error) {
	r.mu.Lock()
	r.admitting = false
	r.lastError = err
	r.mu.Unlock()
}

func (r *PublisherRuntime) setError(err error) {
	r.mu.Lock()
	r.lastError = err
	r.mu.Unlock()
}

func (r *PublisherRuntime) flushCachedQueue(ctx context.Context) {
	r.mu.Lock()
	hasPolicy := r.hasPolicy
	revision := r.snapshot.Revision
	r.mu.Unlock()
	if hasPolicy {
		_ = r.publisher.FlushForPolicyRevision(ctx, strconv.FormatUint(revision, 10))
	}
}

func (r *PublisherRuntime) drainAndAcknowledge(ctx context.Context, revision uint64, closePublisher, requireCandidate bool) (DrainResult, error) {
	revisionText := strconv.FormatUint(revision, 10)
	var drain DrainResult
	if closePublisher {
		drain = r.publisher.CloseForPolicyRevision(ctx, revisionText)
	} else {
		drain = r.publisher.FlushForPolicyRevision(ctx, revisionText)
	}
	operation, allowed, err := r.configuration.OperationForRevision(ctx, revision)
	if err != nil {
		return drain, fmt.Errorf("read publisher acknowledgement candidate: %w", err)
	}
	if !allowed {
		if requireCandidate && r.hasUnacknowledgedDisposition(revision, drain.LastAcceptedSequence) {
			return drain, errors.New("no active publisher acknowledgement candidate for unacknowledged queue disposition")
		}
		return drain, nil
	}
	if r.alreadyAcknowledged(revision, drain.LastAcceptedSequence) {
		return drain, nil
	}
	if err := r.control.Acknowledge(ctx, operation, drain, r.now()); err != nil {
		return drain, fmt.Errorf("acknowledge publisher queue disposition: %w", err)
	}
	r.mu.Lock()
	r.ackRevision = revision
	r.ackSequence = drain.LastAcceptedSequence
	r.mu.Unlock()
	return drain, nil
}

func (r *PublisherRuntime) hasUnacknowledgedDisposition(revision, sequence uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sequence > 0 && (r.ackRevision != revision || r.ackSequence != sequence)
}

func (r *PublisherRuntime) alreadyAcknowledged(revision, sequence uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ackRevision == revision && r.ackSequence == sequence
}
