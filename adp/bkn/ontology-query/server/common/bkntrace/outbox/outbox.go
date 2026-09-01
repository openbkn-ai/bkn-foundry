// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

// Package outbox persists BKN Trace evidence before it is sent to Core.
package outbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
)

const (
	StatusPending               = "pending"
	StatusProcessing            = "processing"
	StatusRetry                 = "retry"
	StatusDelivered             = "delivered"
	StatusDLQ                   = "dlq"
	StatusConflict              = "conflict"
	StatusAbandoned             = "abandoned"
	initialProducerEpoch uint64 = 1
)

const (
	tableOutbox = "ontology_query_trace_outbox"
	tableStream = "ontology_query_trace_producer_stream_state"
	tableAudit  = "ontology_query_trace_outbox_action_audit"
)

var (
	ErrDisabled        = errors.New("bkn trace producer outbox is disabled")
	ErrEventIDConflict = errors.New("evidence event id conflicts with existing outbox record")
)

// Owner is the trusted lifecycle identity that Core requires when ingesting an
// immutable event. It is persisted inside the sanitized envelope so a retry
// does not depend on the original HTTP request still being available.
type Owner struct {
	ApplicationPrincipalID string `json:"application_principal_id"`
	EffectiveSubjectType   string `json:"effective_subject_type"`
	EffectiveSubjectID     string `json:"effective_subject_id"`
	DelegationID           string `json:"delegation_id,omitempty"`
}

func (o Owner) Valid() bool {
	return o.ApplicationPrincipalID != "" &&
		o.EffectiveSubjectID != "" && (o.EffectiveSubjectType == "user" || o.EffectiveSubjectType == "service")
}

// Event is the Core 3.0 request body. Envelope deliberately contains only
// sanitized evidence and the trusted owner needed for delayed delivery.
type Event struct {
	EventID          string          `json:"event_id"`
	EventType        string          `json:"event_type"`
	SchemaVersion    string          `json:"bkn.trace.schema.version"`
	PayloadHash      string          `json:"payload_hash"`
	ConversationID   string          `json:"conversation_id"`
	InteractionID    string          `json:"interaction_id"`
	OperationID      string          `json:"operation_id,omitempty"`
	Attempt          uint32          `json:"attempt,omitempty"`
	RequestID        string          `json:"request_id,omitempty"`
	TraceID          string          `json:"trace_id,omitempty"`
	SpanID           string          `json:"span_id,omitempty"`
	ProducerID       string          `json:"producer_id"`
	ProducerStreamID string          `json:"producer_stream_id"`
	ProducerEpoch    uint64          `json:"producer_epoch"`
	ProducerSequence uint64          `json:"producer_sequence"`
	CausationIDs     []string        `json:"causation_event_ids,omitempty"`
	StartedAt        time.Time       `json:"started_at"`
	ObservedAt       time.Time       `json:"observed_at"`
	EmittedAt        time.Time       `json:"emitted_at"`
	Envelope         json.RawMessage `json:"envelope"`
}

type Record struct {
	OutboxID     int64
	Event        Event
	Owner        Owner
	Status       string
	Attempts     uint32
	LeaseToken   string
	StateVersion uint64
}

type Config struct {
	ProducerID         string
	ProducerStreamID   string
	DatabaseType       string
	IngestURL          string
	IngestToken        string
	QueryGatewayToken  string
	CoreRequestTimeout time.Duration
	LeaseDuration      time.Duration
	PollInterval       time.Duration
	// BumpEpochOnStart increments producer_epoch when a delivery worker starts.
	// API-only writers must leave this false and read the current epoch from DB.
	BumpEpochOnStart bool
}

type databaseDialect string

const (
	dialectMariaDB databaseDialect = "mariadb"
	dialectDM8     databaseDialect = "dm8"
)

func parseDatabaseDialect(value string) (databaseDialect, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "DEFAULT", "MYSQL", "MARIADB", "TIDB":
		return dialectMariaDB, nil
	case "DM8":
		return dialectDM8, nil
	default:
		return "", fmt.Errorf("unsupported producer outbox database type %q", value)
	}
}

func (c Config) normalized() Config {
	c.ProducerID = strings.TrimSpace(c.ProducerID)
	c.ProducerStreamID = strings.TrimSpace(c.ProducerStreamID)
	c.IngestURL = strings.TrimSpace(c.IngestURL)
	if c.CoreRequestTimeout <= 0 {
		c.CoreRequestTimeout = 10 * time.Second
	}
	if c.LeaseDuration <= c.CoreRequestTimeout {
		c.LeaseDuration = 30 * time.Second
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 250 * time.Millisecond
	}
	return c
}

type Repository struct {
	db      *sql.DB
	config  Config
	dialect databaseDialect
	epoch   uint64
}

func NewRepository(db *sql.DB, config Config) (*Repository, error) {
	if db == nil {
		return nil, errors.New("outbox database is required")
	}
	config = config.normalized()
	if config.ProducerID == "" || config.ProducerStreamID == "" {
		return nil, errors.New("producer ID and stream ID are required")
	}
	if config.IngestURL == "" || config.QueryGatewayToken == "" {
		return nil, errors.New("ingest URL and query gateway token are required")
	}
	dialect, err := parseDatabaseDialect(config.DatabaseType)
	if err != nil {
		return nil, err
	}
	r := &Repository{db: db, config: config, dialect: dialect}
	now := time.Now().UTC()
	var epoch uint64
	if config.BumpEpochOnStart {
		epoch, err = r.acquireEpoch(context.Background(), now)
	} else {
		epoch, err = r.loadCurrentEpoch(context.Background(), now)
	}
	if err != nil {
		return nil, err
	}
	r.epoch = epoch
	return r, nil
}

func NewCleanupRepository(db *sql.DB, databaseType string) (*Repository, error) {
	if db == nil {
		return nil, errors.New("outbox database is required")
	}
	dialect, err := parseDatabaseDialect(databaseType)
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, dialect: dialect}, nil
}

func (r *Repository) Enqueue(ctx context.Context, event Event, owner Owner) (Event, error) {
	if !owner.Valid() {
		return Event{}, errors.New("trusted evidence owner is incomplete")
	}
	if event.EventID == "" || event.EventType == "" || len(event.Envelope) == 0 {
		return Event{}, errors.New("evidence event is incomplete")
	}
	event.SchemaVersion = "3.0.0"
	event.ProducerID = r.config.ProducerID
	event.ProducerStreamID = r.config.ProducerStreamID
	if event.EmittedAt.IsZero() {
		event.EmittedAt = time.Now().UTC()
	}
	if event.StartedAt.IsZero() {
		event.StartedAt = event.ObservedAt
	}
	if event.ObservedAt.IsZero() || event.EmittedAt.Before(event.ObservedAt) {
		return Event{}, errors.New("invalid evidence timestamps")
	}
	coreEnvelope, err := sonic.ConfigStd.Marshal(struct {
		Event json.RawMessage `json:"event"`
		Owner Owner           `json:"owner"`
	}{Event: event.Envelope, Owner: owner})
	if err != nil {
		return Event{}, err
	}
	event.Envelope = coreEnvelope
	event.PayloadHash = CanonicalHash(event.Envelope)
	if existing, found, err := r.loadExistingEvent(ctx, event.EventID); err != nil {
		return Event{}, err
	} else if found {
		if existing.PayloadHash == event.PayloadHash {
			return existing, nil
		}
		return Event{}, ErrEventIDConflict
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()
	var epoch, next uint64
	if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT current_epoch, next_sequence FROM %s WHERE producer_id = ? AND producer_stream_id = ? FOR UPDATE", tableStream), r.config.ProducerID, r.config.ProducerStreamID).Scan(&epoch, &next); err != nil {
		return Event{}, err
	}
	if epoch == 0 {
		return Event{}, fmt.Errorf("producer stream %q has invalid epoch 0", r.config.ProducerStreamID)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET next_sequence = ?, updated_at = ? WHERE producer_id = ? AND producer_stream_id = ?", tableStream), next+1, time.Now().UTC(), r.config.ProducerID, r.config.ProducerStreamID); err != nil {
		return Event{}, err
	}
	event.ProducerEpoch, event.ProducerSequence = epoch, next
	stored, err := sonic.ConfigStd.Marshal(struct {
		Event Event `json:"event"`
		Owner Owner `json:"owner"`
	}{Event: event, Owner: owner})
	if err != nil {
		return Event{}, err
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (
		event_id, payload_hash, event_type, schema_version, producer_id, producer_stream_id, producer_epoch, producer_sequence,
		envelope, status, state_version, attempts, available_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0, ?, ?, ?)`, tableOutbox),
		event.EventID, event.PayloadHash, event.EventType, event.SchemaVersion, event.ProducerID, event.ProducerStreamID, event.ProducerEpoch, event.ProducerSequence,
		string(stored), StatusPending, now, now, now)
	if err != nil {
		if isDuplicateKeyError(err) {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				return Event{}, rollbackErr
			}
			existing, found, loadErr := r.loadExistingEvent(ctx, event.EventID)
			if loadErr != nil {
				return Event{}, loadErr
			}
			if found && existing.PayloadHash == event.PayloadHash {
				return existing, nil
			}
			if found {
				return Event{}, ErrEventIDConflict
			}
		}
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (r *Repository) loadExistingEvent(ctx context.Context, eventID string) (Event, bool, error) {
	var payloadHash, stored string
	err := r.db.QueryRowContext(ctx, fmt.Sprintf("SELECT payload_hash, envelope FROM %s WHERE event_id = ?", tableOutbox), eventID).Scan(&payloadHash, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, err
	}
	var row struct {
		Event Event `json:"event"`
		Owner Owner `json:"owner"`
	}
	if err := sonic.ConfigStd.Unmarshal([]byte(stored), &row); err != nil {
		return Event{}, false, err
	}
	row.Event.PayloadHash = payloadHash
	return row.Event, true, nil
}

func isDuplicateKeyError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate") || strings.Contains(message, "unique constraint")
}

// ClaimHeadOfLine atomically leases only the oldest incomplete event in this
// producer stream. A delayed retry, DLQ, or conflict intentionally blocks all
// later events to preserve Core's strict epoch/sequence contract.
func (r *Repository) ClaimHeadOfLine(ctx context.Context, now time.Time) (*Record, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, r.claimHeadOfLineSQL(), r.config.ProducerStreamID, StatusDelivered, StatusAbandoned)
	var record Record
	var raw string
	if err := row.Scan(&record.OutboxID, &raw, &record.Status, &record.Attempts, &record.StateVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if record.Status == StatusDLQ || record.Status == StatusConflict || (record.Status == StatusProcessing && !leaseExpired(ctx, tx, record.OutboxID, now)) ||
		((record.Status == StatusPending || record.Status == StatusRetry) && !available(ctx, tx, record.OutboxID, now)) {
		return nil, nil
	}
	var stored struct {
		Event Event `json:"event"`
		Owner Owner `json:"owner"`
	}
	if err := sonic.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, err
	}
	token, err := leaseToken()
	if err != nil {
		return nil, err
	}
	until := now.Add(r.config.LeaseDuration)
	result, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET status = ?, attempts = attempts + 1, lease_token = ?, locked_until = ?, updated_at = ?, state_version = state_version + 1
		WHERE outbox_id = ? AND status NOT IN (?, ?)`, tableOutbox), StatusProcessing, token, until, now, record.OutboxID, StatusDelivered, StatusAbandoned)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	record.Event, record.Owner, record.Status, record.LeaseToken, record.Attempts = stored.Event, stored.Owner, StatusProcessing, token, record.Attempts+1
	record.StateVersion++
	return &record, nil
}

func available(ctx context.Context, tx *sql.Tx, outboxID int64, now time.Time) bool {
	var at time.Time
	return tx.QueryRowContext(ctx, fmt.Sprintf("SELECT available_at FROM %s WHERE outbox_id = ?", tableOutbox), outboxID).Scan(&at) == nil && !at.After(now)
}

func leaseExpired(ctx context.Context, tx *sql.Tx, outboxID int64, now time.Time) bool {
	var until sql.NullTime
	return tx.QueryRowContext(ctx, fmt.Sprintf("SELECT locked_until FROM %s WHERE outbox_id = ?", tableOutbox), outboxID).Scan(&until) == nil && (!until.Valid || !until.Time.After(now))
}

func (r *Repository) Complete(ctx context.Context, record *Record, status, errorCode string, retryAt time.Time) (bool, error) {
	if record == nil || record.LeaseToken == "" {
		return false, errors.New("record lease token is required")
	}
	now := time.Now().UTC()
	var query string
	var args []any
	switch status {
	case StatusDelivered:
		query = fmt.Sprintf(`UPDATE %s SET status = ?, delivered_at = ?, lease_token = NULL, locked_until = NULL, updated_at = ?, state_version = state_version + 1 WHERE outbox_id = ? AND status = ? AND lease_token = ?`, tableOutbox)
		args = []any{status, now, now, record.OutboxID, StatusProcessing, record.LeaseToken}
	case StatusRetry, StatusDLQ, StatusConflict:
		query = fmt.Sprintf(`UPDATE %s SET status = ?, available_at = ?, last_error_code = ?, last_error_message = ?, last_error_fingerprint = ?, lease_token = NULL, locked_until = NULL, updated_at = ?, state_version = state_version + 1 WHERE outbox_id = ? AND status = ? AND lease_token = ?`, tableOutbox)
		if retryAt.IsZero() {
			retryAt = now
		}
		message := safeErrorMessage(errorCode)
		args = []any{status, retryAt, errorCode, message, fingerprint(message), now, record.OutboxID, StatusProcessing, record.LeaseToken}
	default:
		return false, fmt.Errorf("unsupported completion status %q", status)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (r *Repository) loadCurrentEpoch(ctx context.Context, now time.Time) (uint64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	ensureArgs := []any{r.config.ProducerID, r.config.ProducerStreamID, now, now}
	if r.dialect != dialectDM8 {
		ensureArgs = append(ensureArgs, r.config.ProducerID, r.config.ProducerStreamID)
	}
	if _, err := tx.ExecContext(ctx, r.ensureStreamStateSQL(), ensureArgs...); err != nil {
		return 0, err
	}
	var epoch uint64
	if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT current_epoch FROM %s WHERE producer_id = ? AND producer_stream_id = ? FOR UPDATE", tableStream), r.config.ProducerID, r.config.ProducerStreamID).Scan(&epoch); err != nil {
		return 0, err
	}
	if epoch == 0 {
		if err := r.initializeEpochZero(ctx, tx, now); err != nil {
			return 0, err
		}
		epoch = initialProducerEpoch
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return epoch, nil
}

func (r *Repository) acquireEpoch(ctx context.Context, now time.Time) (uint64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	ensureArgs := []any{r.config.ProducerID, r.config.ProducerStreamID, now, now}
	if r.dialect != dialectDM8 {
		ensureArgs = append(ensureArgs, r.config.ProducerID, r.config.ProducerStreamID)
	}
	_, err = tx.ExecContext(ctx, r.ensureStreamStateSQL(), ensureArgs...)
	if err != nil {
		return 0, err
	}
	var epoch uint64
	if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT current_epoch FROM %s WHERE producer_id = ? AND producer_stream_id = ? FOR UPDATE", tableStream), r.config.ProducerID, r.config.ProducerStreamID).Scan(&epoch); err != nil {
		return 0, err
	}
	if epoch == 0 {
		if err := r.initializeEpochZero(ctx, tx, now); err != nil {
			return 0, err
		}
		epoch = initialProducerEpoch
	}
	epoch++
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET current_epoch = ?, next_sequence = 1, updated_at = ? WHERE producer_id = ? AND producer_stream_id = ?", tableStream), epoch, now, r.config.ProducerID, r.config.ProducerStreamID); err != nil {
		return 0, err
	}
	return epoch, tx.Commit()
}

func (r *Repository) initializeEpochZero(ctx context.Context, tx *sql.Tx, now time.Time) error {
	var incomplete int
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE producer_stream_id = ? AND status NOT IN (?, ?)", tableOutbox,
	), r.config.ProducerStreamID, StatusDelivered, StatusAbandoned).Scan(&incomplete); err != nil {
		return err
	}
	if incomplete != 0 {
		return fmt.Errorf("producer stream %q has %d incomplete epoch 0 records; abandon them before enabling delivery", r.config.ProducerStreamID, incomplete)
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(
		"UPDATE %s SET current_epoch = ?, updated_at = ? WHERE producer_id = ? AND producer_stream_id = ? AND current_epoch = 0", tableStream,
	), initialProducerEpoch, now, r.config.ProducerID, r.config.ProducerStreamID)
	return err
}

func CanonicalHash(payload json.RawMessage) string {
	var decoded any
	if sonic.ConfigStd.Unmarshal(payload, &decoded) == nil {
		if canonical, err := sonic.ConfigStd.Marshal(decoded); err == nil {
			payload = canonical
		}
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func leaseToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func safeErrorMessage(code string) string {
	switch code {
	case "core_timeout":
		return "core request timed out"
	case "core_unavailable":
		return "core request unavailable"
	case "core_rejected":
		return "core rejected evidence event"
	case "producer_sequence_conflict":
		return "core rejected producer sequence"
	default:
		return "core delivery failed"
	}
}

func coreDeliveryOutcome(statusCode int) (status string, errorCode string, retry bool) {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return StatusDelivered, "", false
	case statusCode == http.StatusConflict:
		return StatusConflict, "producer_sequence_conflict", false
	case statusCode >= 500 || statusCode == http.StatusTooManyRequests:
		return StatusRetry, "core_unavailable", true
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden ||
		statusCode == http.StatusNotFound || statusCode == http.StatusRequestTimeout:
		return StatusRetry, "core_unavailable", true
	case statusCode == http.StatusBadRequest:
		return StatusDLQ, "core_rejected", false
	case statusCode >= 400 && statusCode < 500:
		return StatusRetry, "core_unavailable", true
	default:
		return StatusDLQ, "core_rejected", false
	}
}

type Worker struct {
	repository *Repository
	client     *http.Client
	stop       chan struct{}
	done       chan struct{}
	once       sync.Once
}

func NewWorker(repository *Repository) *Worker {
	return &Worker{repository: repository, client: &http.Client{Timeout: repository.config.CoreRequestTimeout}, stop: make(chan struct{}), done: make(chan struct{})}
}

func (w *Worker) Start() { go w.run() }

func (w *Worker) Stop(ctx context.Context) error {
	w.once.Do(func() { close(w.stop) })
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.repository.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			record, err := w.repository.ClaimHeadOfLine(context.Background(), time.Now().UTC())
			if err != nil || record == nil {
				continue
			}
			w.deliver(record)
		}
	}
}

func (w *Worker) deliver(record *Record) {
	ctx, cancel := context.WithTimeout(context.Background(), w.repository.config.CoreRequestTimeout)
	defer cancel()
	body, err := sonic.ConfigStd.Marshal(record.Event)
	if err != nil {
		_, _ = w.repository.Complete(ctx, record, StatusDLQ, "core_rejected", time.Time{})
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.repository.config.IngestURL, bytes.NewReader(body))
	if err != nil {
		_, _ = w.repository.Complete(ctx, record, StatusDLQ, "core_rejected", time.Time{})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BKN-Trace-Query-Token", w.repository.config.QueryGatewayToken)
	req.Header.Set("X-BKN-Trace-Ingest-Token", w.repository.config.IngestToken)
	req.Header.Set("X-BKN-Application-Principal-ID", record.Owner.ApplicationPrincipalID)
	req.Header.Set("X-BKN-Effective-Subject-Type", record.Owner.EffectiveSubjectType)
	req.Header.Set("X-BKN-Effective-Subject-ID", record.Owner.EffectiveSubjectID)
	req.Header.Set("X-BKN-Delegation-ID", record.Owner.DelegationID)
	req.Header.Set("x-account-id", record.Owner.EffectiveSubjectID)
	req.Header.Set("x-account-type", record.Owner.EffectiveSubjectType)
	resp, err := w.client.Do(req)
	if err != nil {
		_, _ = w.repository.Complete(context.Background(), record, StatusRetry, "core_timeout", retryAt(record.Attempts))
		return
	}
	defer resp.Body.Close()
	status, errorCode, shouldRetry := coreDeliveryOutcome(resp.StatusCode)
	if status == StatusDelivered {
		var ack struct {
			Durable bool `json:"durable_ack"`
		}
		if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&ack); err != nil || !ack.Durable {
			_, _ = w.repository.Complete(context.Background(), record, StatusRetry, "core_unavailable", retryAt(record.Attempts))
			return
		}
		_, _ = w.repository.Complete(context.Background(), record, StatusDelivered, "", time.Time{})
		return
	}
	if shouldRetry {
		_, _ = w.repository.Complete(context.Background(), record, StatusRetry, errorCode, retryAt(record.Attempts))
		return
	}
	_, _ = w.repository.Complete(context.Background(), record, status, errorCode, time.Time{})
}

func retryAt(attempt uint32) time.Time {
	delay := time.Second
	for i := uint32(1); i < attempt && delay < time.Minute; i++ {
		delay *= 2
	}
	if delay > time.Minute {
		delay = time.Minute
	}
	return time.Now().UTC().Add(delay)
}

type ListOptions struct {
	Statuses         []string
	ProducerStreamID string
	EventID          string
	Page             int
	PageSize         int
}

type Summary struct {
	OutboxID         int64     `json:"outbox_id"`
	EventID          string    `json:"event_id"`
	ProducerStreamID string    `json:"producer_stream_id"`
	ProducerEpoch    uint64    `json:"producer_epoch"`
	ProducerSequence uint64    `json:"producer_sequence"`
	Status           string    `json:"status"`
	Attempts         uint32    `json:"attempts"`
	ErrorCode        string    `json:"last_error_code,omitempty"`
	ErrorFingerprint string    `json:"last_error_fingerprint,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	StateVersion     uint64    `json:"state_version"`
}

type Detail struct {
	Summary
	Envelope json.RawMessage `json:"envelope"`
}

type ActionRequest struct {
	ExpectedStateVersion uint64
	ReasonCode           string
	ReasonNote           string
	IdempotencyKey       string
	OperatorID           string
	OperatorType         string
}

type ActionResult struct {
	Status       string    `json:"status"`
	StateVersion uint64    `json:"state_version"`
	At           time.Time `json:"at"`
}

type CleanupResult struct {
	Delivered int64
	Abandoned int64
	Audits    int64
}

var (
	ErrNotFound             = errors.New("outbox record was not found")
	ErrStateConflict        = errors.New("outbox state version conflict")
	ErrActionNotAllowed     = errors.New("outbox action is not allowed for current state")
	ErrIdempotencyKeyReused = errors.New("idempotency key was reused with another request")
	ErrInvalidActionRequest = errors.New("invalid outbox action request")
)

func (r *Repository) List(ctx context.Context, options ListOptions) ([]Summary, error) {
	if options.Page < 1 {
		options.Page = 1
	}
	if options.PageSize < 1 || options.PageSize > 100 {
		options.PageSize = 50
	}
	columns := "outbox_id, event_id, producer_stream_id, producer_epoch, producer_sequence, status, attempts, COALESCE(last_error_code, ''), COALESCE(last_error_fingerprint, ''), created_at, updated_at, state_version"
	where, args := outboxListFilters(options)
	query := fmt.Sprintf("SELECT %s FROM %s WHERE 1=1%s", columns, tableOutbox, where)
	if r.dialect == dialectDM8 {
		query = fmt.Sprintf("SELECT %s FROM (SELECT %s, ROW_NUMBER() OVER (ORDER BY updated_at DESC, outbox_id DESC) AS row_number_ FROM %s WHERE 1=1%s) WHERE row_number_ BETWEEN ? AND ?", columns, columns, tableOutbox, where)
		args = append(args, (options.Page-1)*options.PageSize+1, options.Page*options.PageSize)
	} else {
		query += " ORDER BY updated_at DESC, outbox_id DESC LIMIT ? OFFSET ?"
		args = append(args, options.PageSize, (options.Page-1)*options.PageSize)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Summary, 0)
	for rows.Next() {
		var item Summary
		if err := rows.Scan(&item.OutboxID, &item.EventID, &item.ProducerStreamID, &item.ProducerEpoch, &item.ProducerSequence, &item.Status, &item.Attempts, &item.ErrorCode, &item.ErrorFingerprint, &item.CreatedAt, &item.UpdatedAt, &item.StateVersion); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) Count(ctx context.Context, options ListOptions) (int64, error) {
	where, args := outboxListFilters(options)
	var total int64
	err := r.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE 1=1%s", tableOutbox, where), args...).Scan(&total)
	return total, err
}

func outboxListFilters(options ListOptions) (string, []any) {
	where := ""
	args := make([]any, 0, 8)
	if eventID := strings.TrimSpace(options.EventID); eventID != "" {
		where += " AND event_id = ?"
		args = append(args, eventID)
	}
	if stream := strings.TrimSpace(options.ProducerStreamID); stream != "" {
		where += " AND producer_stream_id = ?"
		args = append(args, stream)
	}
	if len(options.Statuses) > 0 {
		placeholders := make([]string, 0, len(options.Statuses))
		for _, status := range options.Statuses {
			if status = strings.TrimSpace(status); status != "" {
				placeholders = append(placeholders, "?")
				args = append(args, status)
			}
		}
		if len(placeholders) > 0 {
			where += " AND status IN (" + strings.Join(placeholders, ",") + ")"
		}
	}
	return where, args
}

func (r *Repository) Get(ctx context.Context, outboxID int64) (Detail, error) {
	var detail Detail
	var raw string
	err := r.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT outbox_id, event_id, producer_stream_id, producer_epoch, producer_sequence, status, attempts,
		COALESCE(last_error_code, ''), COALESCE(last_error_fingerprint, ''), created_at, updated_at, state_version, envelope
		FROM %s WHERE outbox_id = ?`, tableOutbox), outboxID).Scan(
		&detail.OutboxID, &detail.EventID, &detail.ProducerStreamID, &detail.ProducerEpoch, &detail.ProducerSequence, &detail.Status, &detail.Attempts,
		&detail.ErrorCode, &detail.ErrorFingerprint, &detail.CreatedAt, &detail.UpdatedAt, &detail.StateVersion, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	var stored struct {
		Event Event `json:"event"`
	}
	if err := sonic.Unmarshal([]byte(raw), &stored); err != nil {
		return Detail{}, err
	}
	detail.Envelope = stored.Event.Envelope
	return detail, nil
}

func (r *Repository) Retry(ctx context.Context, outboxID int64, request ActionRequest) (ActionResult, error) {
	return r.act(ctx, outboxID, "retry", request)
}

func (r *Repository) Abandon(ctx context.Context, outboxID int64, request ActionRequest) (ActionResult, error) {
	return r.act(ctx, outboxID, "abandon", request)
}

func (r *Repository) act(ctx context.Context, outboxID int64, action string, request ActionRequest) (ActionResult, error) {
	if outboxID < 1 || request.ExpectedStateVersion == 0 || strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.OperatorID) == "" || len(request.ReasonNote) > 256 {
		return ActionResult{}, ErrInvalidActionRequest
	}
	request.ReasonCode, request.ReasonNote = strings.TrimSpace(request.ReasonCode), strings.TrimSpace(request.ReasonNote)
	if request.ReasonCode == "" {
		return ActionResult{}, ErrInvalidActionRequest
	}
	requestHash := actionRequestHash(outboxID, action, request)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ActionResult{}, err
	}
	defer tx.Rollback()
	var previousHash, previousStatus string
	var previousVersion uint64
	var previousAt time.Time
	err = tx.QueryRowContext(ctx, fmt.Sprintf("SELECT request_hash, result_status, result_state_version, result_at FROM %s WHERE idempotency_key = ?", tableAudit), request.IdempotencyKey).Scan(&previousHash, &previousStatus, &previousVersion, &previousAt)
	if err == nil {
		if previousHash != requestHash {
			return ActionResult{}, ErrIdempotencyKeyReused
		}
		return ActionResult{Status: previousStatus, StateVersion: previousVersion, At: previousAt}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ActionResult{}, err
	}
	var status, eventID string
	var version uint64
	err = tx.QueryRowContext(ctx, fmt.Sprintf("SELECT status, state_version, event_id FROM %s WHERE outbox_id = ? FOR UPDATE", tableOutbox), outboxID).Scan(&status, &version, &eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionResult{}, ErrNotFound
	}
	if err != nil {
		return ActionResult{}, err
	}
	if version != request.ExpectedStateVersion {
		return ActionResult{}, ErrStateConflict
	}
	if (action == "retry" && status != StatusDLQ) || (action == "abandon" && status != StatusDLQ && status != StatusConflict) {
		return ActionResult{}, ErrActionNotAllowed
	}
	now := time.Now().UTC()
	toStatus := StatusRetry
	var update string
	if action == "retry" {
		update = fmt.Sprintf("UPDATE %s SET status = ?, available_at = ?, lease_token = NULL, locked_until = NULL, updated_at = ?, state_version = state_version + 1 WHERE outbox_id = ? AND state_version = ?", tableOutbox)
		_, err = tx.ExecContext(ctx, update, toStatus, now, now, outboxID, version)
	} else {
		toStatus = StatusAbandoned
		update = fmt.Sprintf("UPDATE %s SET status = ?, abandoned_at = ?, abandoned_by = ?, abandon_reason_code = ?, abandon_note = ?, updated_at = ?, state_version = state_version + 1 WHERE outbox_id = ? AND state_version = ?", tableOutbox)
		_, err = tx.ExecContext(ctx, update, toStatus, now, request.OperatorID, request.ReasonCode, request.ReasonNote, now, outboxID, version)
	}
	if err != nil {
		return ActionResult{}, err
	}
	result := ActionResult{Status: toStatus, StateVersion: version + 1, At: now}
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (outbox_id, event_id, action_type, from_status, to_status, reason_code, reason_note, operator_id, operator_type, idempotency_key, request_hash, expected_state_version, result_status, result_state_version, result_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, tableAudit), outboxID, eventID, action, status, toStatus, request.ReasonCode, request.ReasonNote, request.OperatorID, request.OperatorType, request.IdempotencyKey, requestHash, version, result.Status, result.StateVersion, result.At, now)
	if err != nil {
		return ActionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ActionResult{}, err
	}
	return result, nil
}

func actionRequestHash(outboxID int64, action string, request ActionRequest) string {
	payload, _ := sonic.ConfigStd.Marshal(struct {
		OutboxID   int64  `json:"outbox_id"`
		Action     string `json:"action"`
		Version    uint64 `json:"expected_state_version"`
		ReasonCode string `json:"reason_code"`
		ReasonNote string `json:"reason_note"`
	}{outboxID, action, request.ExpectedStateVersion, request.ReasonCode, request.ReasonNote})
	return CanonicalHash(payload)
}

// Cleanup removes only completed records. DLQ and conflict records are never
// eligible because they intentionally block their producer stream.
func (r *Repository) Cleanup(ctx context.Context, deliveredBefore, abandonedBefore, auditBefore time.Time, batchSize int) (CleanupResult, error) {
	if batchSize < 1 || batchSize > 10000 {
		batchSize = 1000
	}
	var result CleanupResult
	var err error
	if result.Delivered, err = r.deleteOutboxBefore(ctx, StatusDelivered, "delivered_at", deliveredBefore, batchSize); err != nil {
		return result, err
	}
	if result.Abandoned, err = r.deleteOutboxBefore(ctx, StatusAbandoned, "abandoned_at", abandonedBefore, batchSize); err != nil {
		return result, err
	}
	result.Audits, err = r.deleteExpiredAudits(ctx, auditBefore, batchSize)
	return result, err
}

func (r *Repository) deleteOutboxBefore(ctx context.Context, status, timestampColumn string, before time.Time, batchSize int) (int64, error) {
	var total int64
	for {
		query := r.deleteOutboxSQL(timestampColumn)
		deleted, err := r.db.ExecContext(ctx, query, status, before, batchSize)
		if err != nil {
			return total, err
		}
		count, err := deleted.RowsAffected()
		if err != nil {
			return total, err
		}
		total += count
		if count < int64(batchSize) {
			return total, nil
		}
	}
}

func (r *Repository) deleteExpiredAudits(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	var total int64
	for {
		query := r.deleteExpiredAuditsSQL()
		deleted, err := r.db.ExecContext(ctx, query, before, StatusDelivered, StatusAbandoned, batchSize)
		if err != nil {
			return total, err
		}
		count, err := deleted.RowsAffected()
		if err != nil {
			return total, err
		}
		total += count
		if count < int64(batchSize) {
			return total, nil
		}
	}
}

func (r *Repository) claimHeadOfLineSQL() string {
	if r.dialect == dialectDM8 {
		return fmt.Sprintf(`SELECT outbox_id, envelope, status, attempts, state_version FROM %s
			WHERE outbox_id = (SELECT outbox_id FROM (SELECT outbox_id FROM %s
				WHERE producer_stream_id = ? AND status NOT IN (?, ?)
				ORDER BY producer_epoch, producer_sequence, outbox_id) WHERE ROWNUM = 1) FOR UPDATE`, tableOutbox, tableOutbox)
	}
	return fmt.Sprintf(`SELECT outbox_id, envelope, status, attempts, state_version
		FROM %s WHERE producer_stream_id = ? AND status NOT IN (?, ?)
		ORDER BY producer_epoch, producer_sequence, outbox_id LIMIT 1 FOR UPDATE`, tableOutbox)
}

func (r *Repository) ensureStreamStateSQL() string {
	if r.dialect == dialectDM8 {
		return fmt.Sprintf(`MERGE INTO %s target USING (SELECT ? AS producer_id, ? AS producer_stream_id FROM DUAL) source
			ON (target.producer_id = source.producer_id AND target.producer_stream_id = source.producer_stream_id)
			WHEN NOT MATCHED THEN INSERT (producer_id, producer_stream_id, current_epoch, next_sequence, created_at, updated_at)
			VALUES (source.producer_id, source.producer_stream_id, 1, 1, ?, ?)`, tableStream)
	}
	return fmt.Sprintf(`INSERT INTO %s (producer_id, producer_stream_id, current_epoch, next_sequence, created_at, updated_at)
		SELECT ?, ?, 1, 1, ?, ? WHERE NOT EXISTS (SELECT 1 FROM %s WHERE producer_id = ? AND producer_stream_id = ?)`, tableStream, tableStream)
}

func (r *Repository) deleteOutboxSQL(timestampColumn string) string {
	if r.dialect == dialectDM8 {
		return fmt.Sprintf("DELETE FROM %s WHERE outbox_id IN (SELECT outbox_id FROM (SELECT outbox_id FROM %s WHERE status = ? AND %s < ? ORDER BY outbox_id) WHERE ROWNUM <= ?)", tableOutbox, tableOutbox, timestampColumn)
	}
	return fmt.Sprintf("DELETE FROM %s WHERE status = ? AND %s < ? LIMIT ?", tableOutbox, timestampColumn)
}

func (r *Repository) deleteExpiredAuditsSQL() string {
	if r.dialect == dialectDM8 {
		return fmt.Sprintf(`DELETE FROM %s WHERE action_id IN (SELECT action_id FROM (
			SELECT action_id FROM %s WHERE created_at < ? AND NOT EXISTS (
				SELECT 1 FROM %s WHERE %s.outbox_id = %s.outbox_id AND status NOT IN (?, ?)
			) ORDER BY action_id) WHERE ROWNUM <= ?)`, tableAudit, tableAudit, tableOutbox, tableOutbox, tableAudit)
	}
	return fmt.Sprintf(`DELETE FROM %s WHERE created_at < ? AND NOT EXISTS (
		SELECT 1 FROM %s WHERE %s.outbox_id = %s.outbox_id AND status NOT IN (?, ?)
	) LIMIT ?`, tableAudit, tableOutbox, tableOutbox, tableAudit)
}
