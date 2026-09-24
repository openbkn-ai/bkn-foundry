package evidencepublisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var errInvalidConfig = errors.New("invalid evidence publisher config")

type Sender interface {
	Send(context.Context, Record) error
}

type Publisher struct {
	config  Config
	sender  Sender
	metrics Metrics

	mu         sync.Mutex
	queue      []queuedRecord
	queueBytes int
	sequence   uint64
	closed     bool
	lastAck    DrainResult
}

func New(config Config, sender Sender) (*Publisher, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	if sender == nil {
		return nil, errors.New("evidence publisher sender is required")
	}
	return &Publisher{config: config, sender: sender, metrics: noopMetrics{}}, nil
}

func (p *Publisher) SetMetrics(metrics Metrics) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if metrics == nil {
		p.metrics = noopMetrics{}
		return
	}
	p.metrics = metrics
}

func (p *Publisher) TryPublish(event Event) PublishResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.metrics.Eligible()
	if p.closed {
		p.metrics.Dropped(ReasonPublisherClosing)
		return PublishResult{Disposition: Dropped, Reason: ReasonPublisherClosing}
	}
	if event.EventID == "" || event.EventType == "" || len(event.Envelope) == 0 {
		p.metrics.Dropped(ReasonInvalidEvent)
		return PublishResult{Disposition: Dropped, Reason: ReasonInvalidEvent}
	}
	payloadHash, err := canonicalPayloadHash(event.Envelope)
	if err != nil {
		p.metrics.Dropped(ReasonInvalidEvent)
		return PublishResult{EventID: event.EventID, Disposition: Dropped, Reason: ReasonInvalidEvent}
	}
	sequence := p.sequence + 1
	value := map[string]any{
		"event_id":                 event.EventID,
		"event_type":               event.EventType,
		"bkn.trace.schema.version": "3.0.0",
		"payload_hash":             payloadHash,
		"conversation_id":          event.ConversationID,
		"interaction_id":           event.InteractionID,
		"producer_id":              p.config.ProducerID,
		"producer_stream_id":       p.config.BaseStreamID + ":" + p.config.ProcessBootID,
		"producer_epoch":           uint64(1),
		"producer_sequence":        sequence,
		"started_at":               event.StartedAt,
		"observed_at":              event.ObservedAt,
		"emitted_at":               event.EmittedAt,
		"envelope":                 json.RawMessage(event.Envelope),
	}
	for key, candidate := range map[string]any{
		"operation_id": event.OperationID,
		"attempt":      event.Attempt,
		"request_id":   event.RequestID,
		"trace_id":     event.TraceID,
		"span_id":      event.SpanID,
	} {
		if key == "attempt" && candidate.(int) == 0 {
			continue
		}
		if candidate != "" && candidate != 0 {
			value[key] = candidate
		}
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		p.metrics.Dropped(ReasonSerialization)
		return PublishResult{EventID: event.EventID, Disposition: Dropped, Reason: ReasonSerialization}
	}
	if len(bytes) > p.config.MaxRecordBytes {
		p.metrics.Dropped(ReasonMessageTooLarge)
		return PublishResult{EventID: event.EventID, Disposition: Dropped, Reason: ReasonMessageTooLarge}
	}
	if len(p.queue) >= p.config.QueueMaxRecords || p.queueBytes+len(bytes) > p.config.QueueMaxBytes {
		p.metrics.Dropped(ReasonQueueFull)
		return PublishResult{EventID: event.EventID, Disposition: Dropped, Reason: ReasonQueueFull}
	}
	record := Record{
		Key:   p.config.BaseStreamID + ":" + p.config.ProcessBootID,
		Value: append([]byte(nil), bytes...),
		Headers: []Header{
			{Key: "content-type", Value: "application/json"},
			{Key: "bkn-trace-schema-version", Value: "3.0.0"},
			{Key: "capture_policy_revision", Value: p.config.CapturePolicyRevision},
			{Key: "producer_instance_id", Value: p.config.WorkloadIdentity + "#" + p.config.ProcessBootID},
			{Key: "bkn-evidence-record-class", Value: "live"},
		},
	}
	p.queue = append(p.queue, queuedRecord{record: record, eventID: event.EventID, enqueuedAt: time.Now(), bytes: len(bytes)})
	p.queueBytes += len(bytes)
	p.sequence = sequence
	p.metrics.Accepted()
	return PublishResult{EventID: event.EventID, Disposition: Accepted}
}

func (p *Publisher) SnapshotQueue() []Record {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]Record, len(p.queue))
	for index, item := range p.queue {
		result[index] = item.record
	}
	return result
}

func (p *Publisher) NextSequence() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sequence + 1
}

func (p *Publisher) Flush(ctx context.Context) DrainResult {
	return p.drain(ctx, false)
}

func (p *Publisher) Close(ctx context.Context) DrainResult {
	return p.drain(ctx, true)
}

func (p *Publisher) drain(ctx context.Context, closePublisher bool) DrainResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && p.config.ShutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.config.ShutdownTimeout)
		defer cancel()
	}
	p.mu.Lock()
	if p.closed {
		ack := p.lastAck
		p.mu.Unlock()
		return ack
	}
	if closePublisher {
		p.closed = true
	}
	items := append([]queuedRecord(nil), p.queue...)
	p.queue = nil
	p.queueBytes = 0
	ack := DrainResult{
		ProducerInstanceID:    p.config.WorkloadIdentity + "#" + p.config.ProcessBootID,
		CapturePolicyRevision: p.config.CapturePolicyRevision,
		LastAcceptedSequence:  p.sequence,
		QueueEmpty:            true,
	}
	p.mu.Unlock()

	for index, item := range items {
		if ctx.Err() != nil {
			remaining := uint64(len(items) - index)
			ack.Dropped += remaining
			for dropped := uint64(0); dropped < remaining; dropped++ {
				p.metrics.Dropped(ReasonShutdownTimeout)
			}
			break
		}
		if p.config.MaxAge > 0 && time.Since(item.enqueuedAt) > p.config.MaxAge {
			ack.Dropped++
			p.metrics.Dropped(ReasonRetryExhausted)
			continue
		}
		published := false
		for attempt := 0; attempt < p.config.MaxAttempts; attempt++ {
			if err := p.send(ctx, item.record); err == nil {
				published = true
				break
			} else if attempt+1 < p.config.MaxAttempts {
				if ctx.Err() != nil {
					break
				}
				timer := time.NewTimer(p.config.RetryBackoff)
				select {
				case <-ctx.Done():
					timer.Stop()
					attempt = p.config.MaxAttempts
				case <-timer.C:
				}
			}
		}
		if published {
			ack.Published++
			p.metrics.Published()
		} else {
			ack.Dropped++
			p.metrics.Dropped(ReasonRetryExhausted)
		}
	}
	p.mu.Lock()
	p.lastAck = ack
	p.mu.Unlock()
	return ack
}

func (p *Publisher) send(ctx context.Context, record Record) error {
	if ctx == nil {
		return fmt.Errorf("nil context")
	}
	return p.sender.Send(ctx, record)
}
