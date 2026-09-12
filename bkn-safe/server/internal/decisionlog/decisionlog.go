// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package decisionlog records authorization decisions (#334): for every
// evaluation the HTTP layer asks the enforcer for, one row saying who asked for
// what on which resource, what the answer was and on what basis. It exists so
// that "why was this allowed", "what did this account touch" and "what would
// removing this role affect" can be answered from data rather than guessed.
//
// The decision path is hot (every list page calls /resource-filter, every
// admin request passes a permission point), so writes never block a request:
// Record hands the row to a bounded queue and returns; one writer goroutine
// drains it in batches. When the queue is full the row is dropped and counted
// — losing a decision row is preferable to slowing down or failing the
// decision itself. Allow decisions may be sampled; deny decisions never are.
package decisionlog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// Decision values (mirror authz.Decision; kept as strings so this package does
// not depend on the enforcer).
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionNone  = "none"
)

// Entry is one decision to record. The timestamp and id are assigned here.
type Entry struct {
	AccessorID        string
	ResourceType      string
	ResourceID        string
	Operation         string
	Scope             string
	Decision          string
	Basis             string
	DeniedRequirement string
	Source            string
	RequestID         string
	TraceID           string
	ClientIP          string
	Detail            string
}

// Options tune the store. The zero value is a disabled store.
type Options struct {
	// Enabled turns recording on. A disabled store accepts Record calls and
	// discards them, so call sites need no nil checks.
	Enabled bool
	// AllowSampleRate in [0,1] is the fraction of allow decisions kept; deny
	// and none are always kept. 0 keeps no allow rows, 1 keeps all.
	AllowSampleRate float64
	// QueueSize bounds the in-flight rows; beyond it Record drops. Default 4096.
	QueueSize int
	// BatchSize is the largest insert batch. Default 200.
	BatchSize int
	// FlushInterval is how long a partial batch waits before it is written.
	// Default 250ms.
	FlushInterval time.Duration
	// Synchronous writes every row inline instead of through the queue. It is
	// for tests and single-request tooling where ordering must be observable
	// immediately; production keeps it false.
	Synchronous bool
}

// Store records and queries authorization decisions.
type Store struct {
	db      *gorm.DB
	opts    Options
	queue   chan model.AuthzDecision
	flushCh chan chan struct{}
	stop    chan struct{}
	stopped chan struct{}
	once    sync.Once
	dropped atomic.Uint64
	lastErr atomic.Int64 // unix seconds of the last logged write error
}

// New builds a store and, when enabled and not synchronous, starts its writer.
func New(db *gorm.DB, opts Options) *Store {
	s := newStore(db, opts)
	if s.opts.Enabled && !s.opts.Synchronous {
		go s.run()
	}
	return s
}

func newStore(db *gorm.DB, opts Options) *Store {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 4096
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 200
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 250 * time.Millisecond
	}
	if opts.AllowSampleRate < 0 {
		opts.AllowSampleRate = 0
	}
	if opts.AllowSampleRate > 1 {
		opts.AllowSampleRate = 1
	}
	return &Store{
		db:      db,
		opts:    opts,
		queue:   make(chan model.AuthzDecision, opts.QueueSize),
		flushCh: make(chan chan struct{}),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

// Enabled reports whether the store records anything.
func (s *Store) Enabled() bool { return s != nil && s.opts.Enabled }

// Dropped is the number of rows discarded because the queue was full.
func (s *Store) Dropped() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}

// Record queues one decision. It never blocks and never returns an error: a
// full queue drops the row and bumps Dropped. Safe on a nil or disabled store.
func (s *Store) Record(e Entry) {
	if s == nil || !s.opts.Enabled || s.db == nil {
		return
	}
	if e.Decision == DecisionAllow && !s.sampled() {
		return
	}
	// Clip every string to its column width. The resource and operation
	// names come straight from unvalidated /authz request bodies; one
	// over-long value would fail the whole insert batch under strict mode
	// and take up to BatchSize unrelated decisions with it.
	row := model.AuthzDecision{
		ID:                newID(),
		AccessorID:        clip(e.AccessorID, 64),
		ResourceType:      clip(e.ResourceType, 64),
		ResourceID:        clip(e.ResourceID, 128),
		Operation:         clip(e.Operation, 128),
		Scope:             clip(e.Scope, 16),
		Decision:          clip(e.Decision, 16),
		Basis:             clip(e.Basis, 32),
		DeniedRequirement: clip(e.DeniedRequirement, 64),
		Source:            clip(e.Source, 32),
		RequestID:         clip(e.RequestID, 128),
		TraceID:           clip(e.TraceID, 64),
		ClientIP:          clip(e.ClientIP, 64),
		Detail:            clip(e.Detail, 1024),
		CreatedAt:         time.Now().UTC().Truncate(time.Millisecond),
	}
	if s.opts.Synchronous {
		s.write([]model.AuthzDecision{row})
		return
	}
	select {
	case s.queue <- row:
	default:
		if s.dropped.Add(1)%1000 == 1 {
			slog.Warn("authz decision log queue full, dropping decisions", "dropped_total", s.dropped.Load())
		}
	}
}

func (s *Store) sampled() bool {
	rate := s.opts.AllowSampleRate
	if rate >= 1 {
		return true
	}
	if rate <= 0 {
		return false
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return true
	}
	return float64(n.Int64()) < rate*1_000_000
}

// Flush writes everything queued so far and returns when it is persisted (or
// ctx ends). No-op on a synchronous or disabled store.
func (s *Store) Flush(ctx context.Context) {
	if s == nil || !s.opts.Enabled || s.opts.Synchronous {
		return
	}
	done := make(chan struct{})
	select {
	case s.flushCh <- done:
	case <-s.stopped:
		return
	case <-ctx.Done():
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Close stops the writer after draining the queue. Idempotent.
func (s *Store) Close() {
	if s == nil || !s.opts.Enabled || s.opts.Synchronous {
		return
	}
	s.once.Do(func() { close(s.stop) })
	<-s.stopped
}

func (s *Store) run() {
	defer close(s.stopped)
	ticker := time.NewTicker(s.opts.FlushInterval)
	defer ticker.Stop()
	batch := make([]model.AuthzDecision, 0, s.opts.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.write(batch)
		batch = batch[:0]
	}
	drain := func() {
		for {
			select {
			case row := <-s.queue:
				batch = append(batch, row)
				if len(batch) >= s.opts.BatchSize {
					flush()
				}
			default:
				flush()
				return
			}
		}
	}
	for {
		select {
		case row := <-s.queue:
			batch = append(batch, row)
			if len(batch) >= s.opts.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case done := <-s.flushCh:
			drain()
			close(done)
		case <-s.stop:
			drain()
			return
		}
	}
}

func (s *Store) write(rows []model.AuthzDecision) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.db.WithContext(ctx).CreateInBatches(rows, 100).Error; err != nil {
		// One warning per 30s at most: a database outage must not turn into a
		// log storm on top of the outage itself.
		now := time.Now().Unix()
		if last := s.lastErr.Load(); now-last >= 30 && s.lastErr.CompareAndSwap(last, now) {
			slog.Warn("authz decision log write failed, decisions lost", "rows", len(rows), "error", err)
		}
	}
}

// Filter narrows List. Zero-value fields are not applied. From/To bound
// CreatedAt (inclusive lower, exclusive upper).
type Filter struct {
	AccessorID   string
	ResourceType string
	ResourceID   string
	Operation    string
	Decision     string
	Source       string
	From         time.Time
	To           time.Time
	Offset       int
	Limit        int
}

// List returns a page of decisions (newest first) plus the total match count.
// Limit<=0 defaults to 50 and is capped at 500.
func (s *Store) List(ctx context.Context, f Filter) ([]model.AuthzDecision, int64, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	q := s.db.WithContext(ctx).Model(&model.AuthzDecision{})
	if f.AccessorID != "" {
		q = q.Where("accessor_id = ?", f.AccessorID)
	}
	if f.ResourceType != "" {
		q = q.Where("resource_type = ?", f.ResourceType)
	}
	if f.ResourceID != "" {
		q = q.Where("resource_id = ?", f.ResourceID)
	}
	if f.Operation != "" {
		q = q.Where("operation = ?", f.Operation)
	}
	if f.Decision != "" {
		q = q.Where("decision = ?", f.Decision)
	}
	if f.Source != "" {
		q = q.Where("source = ?", f.Source)
	}
	if !f.From.IsZero() {
		q = q.Where("created_at >= ?", f.From)
	}
	if !f.To.IsZero() {
		q = q.Where("created_at < ?", f.To)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// No capacity hint from limit: it is caller-supplied (bounded above, but
	// CodeQL flags the pattern) and GORM sizes the slice from the result anyway.
	var rows []model.AuthzDecision
	if err := q.Order("created_at DESC").Order("id ASC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// Purge deletes decisions older than cutoff and returns how many went.
func (s *Store) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&model.AuthzDecision{})
	return res.RowsAffected, res.Error
}

// RunRetention purges rows older than days once now and then every interval,
// until ctx ends. days<=0 keeps everything and returns at once.
func (s *Store) RunRetention(ctx context.Context, days int, interval time.Duration) {
	if s == nil || !s.opts.Enabled || days <= 0 {
		return
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	purge := func() {
		cutoff := time.Now().UTC().AddDate(0, 0, -days)
		n, err := s.Purge(ctx, cutoff)
		if err != nil {
			slog.Warn("authz decision log retention purge failed", "error", err)
			return
		}
		if n > 0 {
			slog.Info("authz decision log retention purge", "deleted", n, "older_than", cutoff.Format(time.RFC3339))
		}
	}
	purge()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purge()
		}
	}
}

// clip truncates s to at most n characters (runes, matching how varchar(n)
// counts), never splitting a multi-byte character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
