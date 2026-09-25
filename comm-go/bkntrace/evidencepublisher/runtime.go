package evidencepublisher

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const publisherHeartbeatInterval = 10 * time.Second

// PublisherRuntime wires the frozen Publisher data plane to the signed
// policy/control-plane inputs. Business calls only consult its verified local
// snapshot; they never wait for policy, heartbeat, Kafka, or ACK I/O.
type PublisherRuntime struct {
	publisher     *Publisher
	policy        *PolicyClient
	configuration *ConfigurationClient
	control       *ControlClient
	now           func() time.Time

	refreshMu   sync.Mutex
	mu          sync.Mutex
	snapshot    PolicySnapshot
	hasPolicy   bool
	admitting   bool
	lastError   error
	ackRevision uint64
	ackSequence uint64
}

type PublisherRuntimeConfig struct {
	Publisher     Config
	Sender        Sender
	Policy        *PolicyClient
	Configuration *ConfigurationClient
	Control       *ControlClient
	Now           func() time.Time
}

func NewPublisherRuntime(ctx context.Context, config PublisherRuntimeConfig) (*PublisherRuntime, error) {
	if config.Policy == nil || config.Configuration == nil || config.Control == nil {
		return nil, errors.New("evidence publisher runtime control clients are required")
	}
	// Runtime records always receive the verified snapshot revision at
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
	if err := runtime.Refresh(ctx); err != nil {
		return nil, err
	}
	return runtime, nil
}

// TryPublish remains the business-facing non-blocking entrypoint. It only
// accepts a fresh, verified enabled snapshot and stamps its exact revision on
// the record before the bounded in-memory queue admission.
func (r *PublisherRuntime) TryPublish(event Event) PublishResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.hasPolicy || !r.admitting || !r.now().Before(r.snapshot.ExpiresAt) {
		if r.hasPolicy && r.snapshot.EvidenceAdmission == "disabled" {
			return PublishResult{Disposition: Dropped, Reason: ReasonPublisherClosing}
		}
		r.admitting = false
		return PublishResult{Disposition: Dropped, Reason: ReasonPublisherUnavailable}
	}
	return r.publisher.TryPublishForPolicyRevision(event, strconv.FormatUint(r.snapshot.Revision, 10))
}

// Refresh obtains and validates a new signed snapshot, sends the per-instance
// heartbeat, and, on disabled policy, first closes admission then accounts the
// bounded queue through the frozen configuration/ACK flow.
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
	r.snapshot = snapshot
	r.hasPolicy = true
	// A newly read revision is not admitted until this instance has completed
	// its matching heartbeat. This avoids records from an unregistered revision.
	r.admitting = false
	r.lastError = nil
	r.mu.Unlock()
	if err := r.control.Heartbeat(ctx, r.publisher.config.WorkloadIdentity, r.publisher.config.ProcessBootID, snapshot.Revision); err != nil {
		r.disableWithError(err)
		return err
	}
	if snapshot.EvidenceAdmission == "disabled" {
		if _, err := r.drainAndAcknowledge(ctx, snapshot.Revision, false); err != nil {
			r.setError(err)
			return err
		}
		return nil
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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_ = r.Refresh(ctx)
		}
	}
}

// Close rejects new events, performs the bounded final disposition, and only
// ACKs if configuration names an active operation at the verified revision.
func (r *PublisherRuntime) Close(ctx context.Context) (DrainResult, error) {
	r.mu.Lock()
	r.admitting = false
	hasPolicy := r.hasPolicy
	revision := r.snapshot.Revision
	r.mu.Unlock()
	if !hasPolicy {
		return r.publisher.Close(ctx), errors.New("publisher runtime has no verified policy")
	}
	return r.drainAndAcknowledge(ctx, revision, true)
}

func (r *PublisherRuntime) LastRefreshError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastError
}

// Flush performs only the bounded Kafka disposition for the current verified
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

func (r *PublisherRuntime) drainAndAcknowledge(ctx context.Context, revision uint64, closePublisher bool) (DrainResult, error) {
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
		if r.hasUnacknowledgedDisposition(revision, drain.LastAcceptedSequence) {
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
