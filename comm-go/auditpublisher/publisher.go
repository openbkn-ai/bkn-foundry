// Package auditpublisher provides the bounded, fail-open producer boundary for
// OpenBKN Audit v1 records.  It deliberately has no database or replay store.
package auditpublisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	jsoncanonicalizer "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

const (
	Topic                = "openbkn.audit.v1"
	SchemaVersion        = "1.0"
	SchemaVersionHeader  = "bkn-audit-schema-version"
	LogAppendTime        = "LogAppendTime"
	MaxValueBytes        = 32 * 1024
	QueueMaxRecords      = 1000
	QueueMaxBytes        = 4 * 1024 * 1024
	WorkerCount          = 2
	BatchMaxRecords      = 100
	Linger               = 25 * time.Millisecond
	MaxAttempts          = 3
	MaxRetryElapsed      = 5 * time.Second
	ShutdownDrainTimeout = 5 * time.Second
)

var secretPattern = regexp.MustCompile(`(?i)(?:bearer\s+[a-z0-9._~-]{8,}|bkn_[a-z0-9._~-]{8,})`)

// Header is the only wire header currently permitted on an Audit v1 record.
type Header struct {
	Key   string
	Value []byte
}

// Record is the immutable producer representation handed to an async writer.
// Value, Key, and Headers must not be mutated after BuildRecord returns.
type Record struct {
	Topic         string
	Key           []byte
	Value         []byte
	Headers       []Header
	TimestampType string
	ContentHash   string
}

// BuildRecord performs the local value checks and constructs the frozen Kafka
// identity.  It does no network I/O and never waits for queue capacity.
func BuildRecord(value []byte) (Record, error) {
	if len(value) == 0 {
		return Record{}, errors.New("audit value is empty")
	}
	if len(value) > MaxValueBytes {
		return Record{}, fmt.Errorf("audit value exceeds %d bytes", MaxValueBytes)
	}

	var payload map[string]any
	if err := json.Unmarshal(value, &payload); err != nil {
		return Record{}, fmt.Errorf("audit value is not JSON: %w", err)
	}
	if hasSecret(payload) {
		return Record{}, errors.New("audit value matched secret detection rule")
	}
	if payload["schema_version"] != SchemaVersion {
		return Record{}, errors.New("audit schema_version must be 1.0")
	}
	sourceID, ok := payload["source_id"].(string)
	if !ok || sourceID == "" {
		return Record{}, errors.New("audit source_id is required")
	}
	target, ok := payload["target"].(map[string]any)
	if !ok {
		return Record{}, errors.New("audit target is required")
	}
	targetType, okType := target["type"].(string)
	targetID, okID := target["id"].(string)
	if !okType || !okID || targetType == "" || targetID == "" {
		return Record{}, errors.New("audit target.type and target.id are required")
	}

	canonical, err := jsoncanonicalizer.Transform(value)
	if err != nil {
		return Record{}, fmt.Errorf("canonicalize audit value: %w", err)
	}
	if len(canonical) > MaxValueBytes {
		return Record{}, fmt.Errorf("canonical audit value exceeds %d bytes", MaxValueBytes)
	}
	digest := sha256.Sum256(canonical)
	return Record{
		Topic:         Topic,
		Key:           []byte(sourceID + "\x1f" + targetType + "\x1f" + targetID),
		Value:         append([]byte(nil), canonical...),
		Headers:       []Header{{Key: SchemaVersionHeader, Value: []byte(SchemaVersion)}},
		TimestampType: LogAppendTime,
		ContentHash:   "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func hasSecret(value any) bool {
	switch current := value.(type) {
	case string:
		return secretPattern.MatchString(current)
	case map[string]any:
		for _, child := range current {
			if hasSecret(child) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if hasSecret(child) {
				return true
			}
		}
	}
	return false
}

// WireBytes reports the queue byte accounting mandated by C1: key + headers +
// immutable serialized value.  Record-count accounting is intentionally separate.
func (r Record) WireBytes() int {
	total := len(r.Key) + len(r.Value)
	for _, header := range r.Headers {
		total += len(header.Key) + len(header.Value)
	}
	return total
}

// HeadersEqual reports whether the record has exactly the one frozen header.
func (r Record) HeadersEqual() bool {
	return len(r.Headers) == 1 && r.Headers[0].Key == SchemaVersionHeader && bytes.Equal(r.Headers[0].Value, []byte(SchemaVersion))
}

// Disposition is the synchronous result of TryPublish.  Once accepted, the
// caller owns no retry responsibility; asynchronous delivery is observed via
// DeliveryObserver.
type Disposition string

const (
	Accepted         Disposition = "accepted"
	DroppedInvalid   Disposition = "dropped_invalid"
	DroppedQueueFull Disposition = "dropped_queue_full"
	DroppedClosed    Disposition = "dropped_closed"
)

type DeliveryOutcome string

const (
	Delivered       DeliveryOutcome = "delivered"
	RetryExhausted  DeliveryOutcome = "retry_exhausted"
	ShutdownTimeout DeliveryOutcome = "shutdown_timeout"
)

type Delivery struct {
	Record   Record
	Outcome  DeliveryOutcome
	Attempts int
	Err      error
}

type Sender interface {
	Send(context.Context, Record) error
}

type DeliveryObserver interface {
	ObserveDelivery(Delivery)
}

type publisherConfig struct {
	QueueMaxRecords int
	QueueMaxBytes   int
	Workers         int
	BatchMaxRecords int
	Linger          time.Duration
	MaxAttempts     int
	MaxElapsed      time.Duration
	ShutdownTimeout time.Duration
}

func defaultPublisherConfig() publisherConfig {
	return publisherConfig{QueueMaxRecords: QueueMaxRecords, QueueMaxBytes: QueueMaxBytes, Workers: WorkerCount,
		BatchMaxRecords: BatchMaxRecords, Linger: Linger, MaxAttempts: MaxAttempts,
		MaxElapsed: MaxRetryElapsed, ShutdownTimeout: ShutdownDrainTimeout}
}

type queuedRecord struct {
	record Record
	bytes  int
}

type Publisher struct {
	sender   Sender
	observer DeliveryObserver
	cfg      publisherConfig
	notify   chan struct{}
	done     chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	queue    []queuedRecord
	bytes    int
	inFlight int
	closed   bool
}

// New constructs the production-boundary publisher with the C2 fixed limits.
// The sender is deliberately a narrow adapter so ordinary consumers never
// need Kafka principal details.
func New(sender Sender, observer DeliveryObserver) (*Publisher, error) {
	if sender == nil {
		return nil, errors.New("audit publisher sender is required")
	}
	return newPublisher(sender, observer, defaultPublisherConfig())
}

func newPublisher(sender Sender, observer DeliveryObserver, cfg publisherConfig) (*Publisher, error) {
	if sender == nil || cfg.QueueMaxRecords <= 0 || cfg.QueueMaxBytes <= 0 || cfg.Workers <= 0 || cfg.BatchMaxRecords <= 0 || cfg.MaxAttempts <= 0 || cfg.Linger < 0 || cfg.MaxElapsed <= 0 || cfg.ShutdownTimeout <= 0 {
		return nil, errors.New("invalid audit publisher configuration")
	}
	p := &Publisher{sender: sender, observer: observer, cfg: cfg, notify: make(chan struct{}, 1), done: make(chan struct{})}
	p.wg.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		go p.worker()
	}
	return p, nil
}

func (p *Publisher) TryPublish(value []byte) Disposition {
	record, err := BuildRecord(value)
	if err != nil {
		return DroppedInvalid
	}
	item := queuedRecord{record: record, bytes: record.WireBytes()}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return DroppedClosed
	}
	if len(p.queue)+p.inFlight >= p.cfg.QueueMaxRecords || p.bytes+item.bytes > p.cfg.QueueMaxBytes {
		return DroppedQueueFull
	}
	p.queue = append(p.queue, item)
	p.bytes += item.bytes
	select {
	case p.notify <- struct{}{}:
	default:
	}
	return Accepted
}

func (p *Publisher) worker() {
	defer p.wg.Done()
	for {
		item, ok := p.next()
		if !ok {
			return
		}
		p.deliver(item)
	}
}

func (p *Publisher) next() (queuedRecord, bool) {
	for {
		p.mu.Lock()
		if len(p.queue) > 0 {
			item := p.queue[0]
			p.queue = p.queue[1:]
			p.inFlight++
			p.mu.Unlock()
			return item, true
		}
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return queuedRecord{}, false
		}
		select {
		case <-p.notify:
		case <-p.done:
		}
	}
}

func (p *Publisher) deliver(item queuedRecord) {
	defer func() {
		p.mu.Lock()
		p.inFlight--
		p.bytes -= item.bytes
		p.mu.Unlock()
	}()
	started := time.Now()
	var err error
	attempts := 0
	for attempts < p.cfg.MaxAttempts && time.Since(started) <= p.cfg.MaxElapsed {
		attempts++
		ctx, cancel := context.WithTimeout(context.Background(), p.cfg.MaxElapsed-time.Since(started))
		err = p.sender.Send(ctx, item.record)
		cancel()
		if err == nil {
			p.observe(Delivery{Record: item.record, Outcome: Delivered, Attempts: attempts})
			return
		}
		if attempts < p.cfg.MaxAttempts {
			time.Sleep(time.Millisecond)
		}
	}
	p.observe(Delivery{Record: item.record, Outcome: RetryExhausted, Attempts: attempts, Err: err})
}

func (p *Publisher) observe(delivery Delivery) {
	if p.observer != nil {
		p.observer.ObserveDelivery(delivery)
	}
}

// Close rejects new records, lets workers drain already accepted records, and
// reports shutdown_timeout if a sender prevents bounded shutdown. It never
// persists an outbox or dead-letter record.
func (p *Publisher) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.done)
	p.mu.Unlock()

	finished := make(chan struct{})
	go func() { p.wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(p.cfg.ShutdownTimeout):
		p.mu.Lock()
		pending := append([]queuedRecord(nil), p.queue...)
		p.queue = nil
		p.bytes = 0
		p.mu.Unlock()
		for _, item := range pending {
			p.observe(Delivery{Record: item.record, Outcome: ShutdownTimeout})
		}
	}
}
