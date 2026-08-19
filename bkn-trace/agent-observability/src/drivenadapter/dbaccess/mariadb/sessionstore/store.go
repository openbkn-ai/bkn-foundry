package sessionstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

const transactionRetries = 4

const schemaMigrationLedgerDDL = `
CREATE TABLE IF NOT EXISTS bkn_trace_schema_migrations (
    migration_version VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    checksum CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    applied_at DATETIME(6) NOT NULL,
    PRIMARY KEY (migration_version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`

const listInteractionsOrderBy = " ORDER BY ordinal_no ASC, interaction_id ASC"
const listInteractionPageOrderBy = " ORDER BY ordinal_no DESC, interaction_id ASC"

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Database is intentionally exposed only to adjacent Trace Core adapters that
// share the same migration boundary (for example manual archive jobs).
func (s *Store) Database() *sql.DB { return s.db }

func (s *Store) Migrate(ctx context.Context) error {
	return s.EnsureSchema(ctx, true)
}

// EnsureSchema validates the database against the embedded image manifest. When
// allowMigrate is false it performs no DDL and refuses a database behind the
// image instead of deferring the failure to a lifecycle write.
func (s *Store) EnsureSchema(ctx context.Context, allowMigrate bool) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open BKN Trace schema migration connection: %w", err)
	}
	defer conn.Close()

	var databaseName string
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&databaseName); err != nil {
		return fmt.Errorf("read BKN Trace schema database name: %w", err)
	}
	lockName := schemaMigrationLockName(databaseName)
	var acquired int
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 30)", lockName).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire BKN Trace schema migration lock: %w", err)
	}
	if acquired != 1 {
		return errors.New("acquire BKN Trace schema migration lock: timed out")
	}
	defer func() {
		var released int
		_ = conn.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName).Scan(&released)
	}()

	ledgerExists, err := schemaMigrationLedgerExists(ctx, conn)
	if err != nil {
		return err
	}
	if !ledgerExists {
		legacyTables, err := legacyTraceTablesExist(ctx, conn)
		if err != nil {
			return err
		}
		if legacyTables {
			return errors.New("BKN Trace schema migration ledger is missing from a non-empty bkn_trace database; back up and clean legacy Trace data before initializing 0.1.4 schema")
		}
		if !allowMigrate {
			return errors.New("BKN Trace schema migration ledger is missing; set BKN_TRACE_CORE_AUTO_MIGRATE=true or initialize a clean bkn_trace database")
		}
		if _, err := conn.ExecContext(ctx, schemaMigrationLedgerDDL); err != nil {
			return fmt.Errorf("create BKN Trace schema migration ledger: %w", err)
		}
	}

	applied, err := loadAppliedSchemaMigrations(ctx, conn)
	if err != nil {
		return err
	}
	plan, err := migrationPlan(Migrations(), applied)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		return nil
	}
	if !allowMigrate {
		return fmt.Errorf("BKN Trace schema is behind this image (missing migration %s); set BKN_TRACE_CORE_AUTO_MIGRATE=true before startup", plan[0].Version)
	}
	for _, migration := range plan {
		if err := applySchemaMigration(ctx, conn, migration); err != nil {
			return err
		}
	}
	return nil
}

func schemaMigrationLockName(databaseName string) string {
	sum := sha256.Sum256([]byte(databaseName))
	return "bkn_trace_schema_" + hex.EncodeToString(sum[:16])
}

func schemaMigrationLedgerExists(ctx context.Context, conn *sql.Conn) (bool, error) {
	var count int
	err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = 'bkn_trace_schema_migrations'`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check BKN Trace schema migration ledger: %w", err)
	}
	return count == 1, nil
}

func legacyTraceTablesExist(ctx context.Context, conn *sql.Conn) (bool, error) {
	var count int
	err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_name LIKE CONCAT('bkn', CHAR(95), 'trace', CHAR(95), '%')
		  AND table_name <> 'bkn_trace_schema_migrations'`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check legacy BKN Trace tables: %w", err)
	}
	return count > 0, nil
}

func loadAppliedSchemaMigrations(ctx context.Context, conn *sql.Conn) (map[string]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT migration_version, checksum FROM bkn_trace_schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read BKN Trace schema migration ledger: %w", err)
	}
	defer rows.Close()
	applied := make(map[string]string)
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("scan BKN Trace schema migration ledger: %w", err)
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate BKN Trace schema migration ledger: %w", err)
	}
	return applied, nil
}

func migrationPlan(manifest []Migration, applied map[string]string) ([]Migration, error) {
	expected := make(map[string]Migration, len(manifest))
	for index, migration := range manifest {
		if migration.Version == "" || migration.Checksum == "" || migration.SQL == "" {
			return nil, fmt.Errorf("BKN Trace migration manifest entry %d is incomplete", index)
		}
		if index > 0 && manifest[index-1].Version >= migration.Version {
			return nil, errors.New("BKN Trace migration manifest versions must be unique and strictly ordered")
		}
		expected[migration.Version] = migration
	}
	for version, checksum := range applied {
		migration, found := expected[version]
		if !found {
			return nil, fmt.Errorf("BKN Trace schema migration %s is newer than this BKN Trace image", version)
		}
		if checksum != migration.Checksum {
			return nil, fmt.Errorf("BKN Trace schema migration %s checksum mismatch", version)
		}
	}
	appliedCount := 0
	for _, migration := range manifest {
		if _, found := applied[migration.Version]; !found {
			break
		}
		appliedCount++
	}
	if appliedCount != len(applied) {
		return nil, errors.New("BKN Trace schema migration ledger is not a contiguous prefix of the image manifest")
	}
	plan := make([]Migration, 0, len(manifest))
	for _, migration := range manifest {
		if _, found := applied[migration.Version]; !found {
			plan = append(plan, migration)
		}
	}
	return plan, nil
}

func applySchemaMigration(ctx context.Context, conn *sql.Conn, migration Migration) error {
	statements, err := splitSQLStatements(migration.SQL)
	if err != nil {
		return fmt.Errorf("parse BKN Trace migration %s: %w", migration.Version, err)
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply BKN Trace migration %s: %w", migration.Version, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO bkn_trace_schema_migrations (migration_version, checksum, applied_at)
		VALUES (?, ?, UTC_TIMESTAMP(6))`, migration.Version, migration.Checksum); err != nil {
		return fmt.Errorf("record BKN Trace migration %s: %w", migration.Version, err)
	}
	return nil
}

func splitSQLStatements(sqlText string) ([]string, error) {
	var statements []string
	start := 0
	var quote byte
	lineComment := false
	blockComment := false
	for index := 0; index < len(sqlText); index++ {
		current := sqlText[index]
		if lineComment {
			if current == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if current == '*' && index+1 < len(sqlText) && sqlText[index+1] == '/' {
				index++
				blockComment = false
			}
			continue
		}
		if quote != 0 {
			if current == '\\' && index+1 < len(sqlText) {
				index++
				continue
			}
			if current == quote {
				if index+1 < len(sqlText) && sqlText[index+1] == quote {
					index++
					continue
				}
				quote = 0
			}
			continue
		}
		if current == '#' || (current == '-' && index+1 < len(sqlText) && sqlText[index+1] == '-' && (index+2 == len(sqlText) || sqlText[index+2] == ' ' || sqlText[index+2] == '\t')) {
			lineComment = true
			continue
		}
		if current == '/' && index+1 < len(sqlText) && sqlText[index+1] == '*' {
			blockComment = true
			index++
			continue
		}
		switch current {
		case '\'', '"', '`':
			quote = current
		case ';':
			if statement := strings.TrimSpace(sqlText[start:index]); statement != "" {
				statements = append(statements, statement)
			}
			start = index + 1
		}
	}
	if quote != 0 || blockComment {
		return nil, errors.New("unterminated quoted SQL literal")
	}
	if statement := strings.TrimSpace(sqlText[start:]); statement != "" {
		statements = append(statements, statement)
	}
	return statements, nil
}

func (s *Store) WithinTransaction(ctx context.Context, fn func(isessionstore.Transaction) error) error {
	var lastErr error
	for attempt := 0; attempt < transactionRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return err
		}
		adapter := &transaction{ctx: ctx, tx: tx}
		if err := adapter.loadServerTime(); err != nil {
			_ = tx.Rollback()
			return err
		}
		callbackErr := fn(adapter)
		if callbackErr == nil {
			callbackErr = adapter.err
		}
		if callbackErr != nil {
			_ = tx.Rollback()
			if retryableTransactionError(callbackErr) {
				lastErr = callbackErr
				if err := waitForTransactionRetry(ctx, attempt); err != nil {
					return err
				}
				continue
			}
			return callbackErr
		}
		if err := tx.Commit(); err != nil {
			if retryableTransactionError(err) {
				lastErr = err
				if waitErr := waitForTransactionRetry(ctx, attempt); waitErr != nil {
					return waitErr
				}
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("transaction retry budget exhausted: %w", lastErr)
}

func waitForTransactionRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(transactionRetryDelay(attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func transactionRetryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= transactionRetries {
		attempt = transactionRetries - 1
	}
	maximum := 5 * time.Millisecond * time.Duration(1<<attempt)
	return time.Duration(rand.Int64N(int64(maximum) + 1))
}

func retryableTransactionError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	switch mysqlErr.Number {
	case 1062, 1205, 1213:
		return true
	default:
		return false
	}
}

type transaction struct {
	ctx context.Context
	tx  *sql.Tx
	now time.Time
	err error
}

func (t *transaction) loadServerTime() error {
	if err := t.tx.QueryRowContext(t.ctx, "SELECT UTC_TIMESTAMP(6)").Scan(&t.now); err != nil {
		return fmt.Errorf("read MariaDB server time: %w", err)
	}
	t.now = t.now.UTC()
	return nil
}

func (t *transaction) Now() time.Time {
	return t.now
}

func (t *transaction) FindCurrentConversation(owner sessionvo.Owner, externalKey string) (sessionvo.Conversation, bool) {
	if t.err != nil {
		return sessionvo.Conversation{}, false
	}
	row := t.tx.QueryRowContext(t.ctx, conversationSelect+`
		WHERE tenant_id=? AND business_domain_id=? AND application_principal_id=?
		  AND effective_subject_type=? AND effective_subject_id=? AND delegation_id=?
		  AND external_conversation_key=?
		ORDER BY generation DESC LIMIT 1 FOR UPDATE`,
		owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, externalKey,
	)
	return t.scanConversation(row)
}

func (t *transaction) FindConversation(conversationID string) (sessionvo.Conversation, bool) {
	if t.err != nil {
		return sessionvo.Conversation{}, false
	}
	return t.scanConversation(t.tx.QueryRowContext(t.ctx, conversationSelect+`
		WHERE conversation_id=? FOR UPDATE`, conversationID))
}

func (t *transaction) PeekConversation(conversationID string) (sessionvo.Conversation, bool) {
	if t.err != nil {
		return sessionvo.Conversation{}, false
	}
	return t.scanConversation(t.tx.QueryRowContext(t.ctx, conversationSelect+`
		WHERE conversation_id=?`, conversationID))
}

func (t *transaction) ListConversationsByIDs(conversationIDs []string) map[string]sessionvo.Conversation {
	result := make(map[string]sessionvo.Conversation, len(conversationIDs))
	if t.err != nil || len(conversationIDs) == 0 {
		return result
	}
	uniqueIDs := make([]string, 0, len(conversationIDs))
	seen := make(map[string]struct{}, len(conversationIDs))
	for _, conversationID := range conversationIDs {
		if conversationID == "" {
			continue
		}
		if _, found := seen[conversationID]; found {
			continue
		}
		seen[conversationID] = struct{}{}
		uniqueIDs = append(uniqueIDs, conversationID)
	}
	if len(uniqueIDs) == 0 {
		return result
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(uniqueIDs)), ",")
	args := make([]any, len(uniqueIDs))
	for index, conversationID := range uniqueIDs {
		args[index] = conversationID
	}
	rows, err := t.tx.QueryContext(t.ctx, conversationSelect+` WHERE conversation_id IN (`+placeholders+`)`, args...)
	if err != nil {
		t.err = err
		return result
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		conversation, scanErr := scanConversationRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return result
		}
		result[conversation.ID] = conversation
	}
	if err := rows.Err(); err != nil {
		t.err = err
	}
	return result
}

func (t *transaction) FindIdempotency(
	scope string,
	owner sessionvo.Owner,
	externalKey string,
	idempotencyKey string,
) (sessionvo.IdempotencyRecord, bool) {
	if t.err != nil {
		return sessionvo.IdempotencyRecord{}, false
	}
	var record sessionvo.IdempotencyRecord
	record.Scope = scope
	record.Owner = owner
	record.ExternalConversationKey = externalKey
	record.IdempotencyKey = idempotencyKey
	err := t.tx.QueryRowContext(t.ctx, `
		SELECT request_hash, resource_type, resource_id, created_at
		FROM bkn_trace_idempotency_records
		WHERE scope=? AND tenant_id=? AND business_domain_id=?
		  AND application_principal_id=? AND effective_subject_type=?
		  AND effective_subject_id=? AND delegation_id=? AND external_conversation_key=?
		  AND idempotency_key=? FOR UPDATE`,
		scope, owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID,
		externalKey, idempotencyKey,
	).Scan(&record.RequestHash, &record.ResourceType, &record.ResourceID, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.IdempotencyRecord{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.IdempotencyRecord{}, false
	}
	return record, true
}

func (t *transaction) SaveIdempotency(record sessionvo.IdempotencyRecord) {
	if t.err != nil {
		return
	}
	_, t.err = t.tx.ExecContext(t.ctx, `
		INSERT INTO bkn_trace_idempotency_records (
			scope, tenant_id, business_domain_id, application_principal_id,
			effective_subject_type, effective_subject_id, delegation_id, external_conversation_key,
			idempotency_key, request_hash, resource_type, resource_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.Scope, record.Owner.TenantID, record.Owner.BusinessDomainID,
		record.Owner.ApplicationPrincipalID, record.Owner.EffectiveSubjectType,
		record.Owner.EffectiveSubjectID, record.Owner.DelegationID, record.ExternalConversationKey,
		record.IdempotencyKey, record.RequestHash, record.ResourceType,
		record.ResourceID, record.CreatedAt,
	)
}

func (t *transaction) ListConversations(owner sessionvo.Owner, limit int) []sessionvo.Conversation {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, conversationSelect+`
		WHERE tenant_id=? AND business_domain_id=? AND application_principal_id=?
		  AND effective_subject_type=? AND effective_subject_id=? AND delegation_id=?
		ORDER BY updated_at DESC, conversation_id DESC LIMIT ?`,
		owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, limit,
	)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Conversation
	for rows.Next() {
		value, scanErr := scanConversationRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) SaveConversation(conversation sessionvo.Conversation) {
	if t.err != nil {
		return
	}
	var exists int
	err := t.tx.QueryRowContext(t.ctx,
		"SELECT 1 FROM bkn_trace_conversations WHERE conversation_id=?",
		conversation.ID,
	).Scan(&exists)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, t.err = t.tx.ExecContext(t.ctx, `
			INSERT INTO bkn_trace_conversations (
				conversation_id, tenant_id, business_domain_id, application_principal_id,
				agent_name, actor_name_snapshot, creation_auth_method,
				effective_subject_type, effective_subject_id, delegation_id,
				external_conversation_key, generation, status, one_shot, row_version,
				created_at, updated_at, closed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			conversation.ID, conversation.Owner.TenantID, conversation.Owner.BusinessDomainID,
			conversation.Owner.ApplicationPrincipalID, conversation.AgentName,
			conversation.ActorNameSnapshot, conversation.CreationAuthMethod, conversation.Owner.EffectiveSubjectType,
			conversation.Owner.EffectiveSubjectID, conversation.Owner.DelegationID,
			conversation.ExternalConversationKey, conversation.Generation, conversation.Status,
			conversation.OneShot, conversation.RowVersion, conversation.CreatedAt,
			conversation.UpdatedAt, nullableTime(conversation.ClosedAt),
		)
	case err != nil:
		t.err = err
	default:
		_, t.err = t.tx.ExecContext(t.ctx, `
			UPDATE bkn_trace_conversations SET agent_name=?, status=?, one_shot=?, row_version=?,
				updated_at=?, closed_at=? WHERE conversation_id=?`,
			conversation.AgentName, conversation.Status, conversation.OneShot, conversation.RowVersion,
			conversation.UpdatedAt, nullableTime(conversation.ClosedAt), conversation.ID,
		)
	}
}

func (t *transaction) FindActiveInteraction(conversationID string) (sessionvo.Interaction, bool) {
	if t.err != nil {
		return sessionvo.Interaction{}, false
	}
	return t.scanInteraction(t.tx.QueryRowContext(t.ctx, interactionSelect+`
		WHERE conversation_id=? AND execution_status='active' LIMIT 1 FOR UPDATE`, conversationID))
}

func (t *transaction) FindInteractionByStartKey(
	conversationID string,
	idempotencyKey string,
) (sessionvo.Interaction, bool) {
	if t.err != nil {
		return sessionvo.Interaction{}, false
	}
	return t.scanInteraction(t.tx.QueryRowContext(t.ctx, interactionSelect+`
		WHERE conversation_id=? AND start_idempotency_key=? FOR UPDATE`,
		conversationID, idempotencyKey,
	))
}

func (t *transaction) ListInteractions(conversationID string) []sessionvo.Interaction {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, interactionSelect+`
		WHERE conversation_id=?`+listInteractionsOrderBy, conversationID)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	result := make([]sessionvo.Interaction, 0)
	for rows.Next() {
		value, scanErr := scanInteractionRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListInteractionPage(query isessionstore.InteractionPageQuery) isessionstore.InteractionPage {
	page := isessionstore.InteractionPage{}
	if t.err != nil || query.Limit <= 0 {
		return page
	}
	if err := t.tx.QueryRowContext(t.ctx,
		"SELECT COUNT(*) FROM bkn_trace_interactions WHERE conversation_id=?", query.ConversationID,
	).Scan(&page.Total); err != nil {
		t.err = err
		return page
	}
	where := " WHERE conversation_id=?"
	args := []any{query.ConversationID}
	if query.AfterOrdinal != 0 {
		where += " AND ordinal_no<?"
		args = append(args, query.AfterOrdinal)
	}
	statement := interactionSelect + where + listInteractionPageOrderBy + " LIMIT ? OFFSET ?"
	args = append(args, query.Limit+1, query.Offset)
	rows, err := t.tx.QueryContext(t.ctx, statement, args...)
	if err != nil {
		t.err = err
		return page
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		value, scanErr := scanInteractionRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return page
		}
		page.Entries = append(page.Entries, value)
	}
	t.err = rows.Err()
	if len(page.Entries) > query.Limit {
		page.Entries = page.Entries[:query.Limit]
		page.HasMore = true
	}
	return page
}

func (t *transaction) FindInteraction(interactionID string) (sessionvo.Interaction, bool) {
	if t.err != nil {
		return sessionvo.Interaction{}, false
	}
	return t.scanInteraction(t.tx.QueryRowContext(t.ctx, interactionSelect+`
		WHERE interaction_id=? FOR UPDATE`, interactionID))
}

func (t *transaction) PeekInteraction(interactionID string) (sessionvo.Interaction, bool) {
	if t.err != nil {
		return sessionvo.Interaction{}, false
	}
	return t.scanInteraction(t.tx.QueryRowContext(t.ctx, interactionSelect+`
		WHERE interaction_id=?`, interactionID))
}

func (t *transaction) ListInteractionsByIDs(interactionIDs []string) map[string]sessionvo.Interaction {
	result := make(map[string]sessionvo.Interaction, len(interactionIDs))
	if t.err != nil {
		return result
	}
	ids := make([]string, 0, len(interactionIDs))
	seen := make(map[string]struct{}, len(interactionIDs))
	for _, id := range interactionIDs {
		if id != "" {
			if _, found := seen[id]; !found {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return result
	}
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := t.tx.QueryContext(t.ctx, interactionSelect+` WHERE interaction_id IN (`+placeholders+`)`, args...)
	if err != nil {
		t.err = err
		return result
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		value, scanErr := scanInteractionRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return result
		}
		result[value.ID] = value
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) NextInteractionOrdinal(conversationID string) uint64 {
	if t.err != nil {
		return 0
	}
	var ordinal uint64
	t.err = t.tx.QueryRowContext(t.ctx,
		"SELECT COALESCE(MAX(ordinal_no), 0) + 1 FROM bkn_trace_interactions WHERE conversation_id=? FOR UPDATE",
		conversationID,
	).Scan(&ordinal)
	return ordinal
}

func (t *transaction) SaveInteraction(interaction sessionvo.Interaction) {
	if t.err != nil {
		return
	}
	manifest := marshalJSON(interaction.ClosureManifest)
	var assemblerDeadline any
	if interaction.ClosureManifest != nil {
		assemblerDeadline = nullableTime(interaction.ClosureManifest.AssemblerDeadline)
	}
	var exists int
	err := t.tx.QueryRowContext(t.ctx,
		"SELECT 1 FROM bkn_trace_interactions WHERE interaction_id=?",
		interaction.ID,
	).Scan(&exists)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, t.err = t.tx.ExecContext(t.ctx, `
			INSERT INTO bkn_trace_interactions (
				interaction_id, conversation_id, ordinal_no, execution_status, evidence_status,
				start_idempotency_key, terminal_idempotency_key, terminal_payload_hash,
				closure_manifest, assembler_deadline, lease_token, lease_epoch, lease_version, lease_expires_at,
				row_version, created_at, updated_at, terminal_at
			) VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?,
				?, ?, ?, ?, ?, ?, ?, ?)`,
			interaction.ID, interaction.ConversationID, interaction.Ordinal,
			interaction.ExecutionStatus, interaction.EvidenceStatus, interaction.StartIdempotencyKey,
			interaction.TerminalIdempotencyKey, interaction.TerminalPayloadHash, manifest,
			assemblerDeadline, interaction.LeaseToken, interaction.LeaseEpoch, interaction.LeaseVersion,
			interaction.LeaseExpiresAt, interaction.RowVersion, interaction.CreatedAt,
			interaction.UpdatedAt, nullableTime(interaction.TerminalAt),
		)
	case err != nil:
		t.err = err
	default:
		_, t.err = t.tx.ExecContext(t.ctx, `
			UPDATE bkn_trace_interactions SET execution_status=?, evidence_status=?,
				terminal_idempotency_key=NULLIF(?, ''), terminal_payload_hash=NULLIF(?, ''),
				closure_manifest=NULLIF(?, ''), assembler_deadline=?, lease_token=?, lease_epoch=?, lease_version=?,
				lease_expires_at=?, row_version=?, updated_at=?, terminal_at=?
			WHERE interaction_id=?`,
			interaction.ExecutionStatus, interaction.EvidenceStatus,
			interaction.TerminalIdempotencyKey, interaction.TerminalPayloadHash, manifest,
			assemblerDeadline, interaction.LeaseToken, interaction.LeaseEpoch, interaction.LeaseVersion,
			interaction.LeaseExpiresAt, interaction.RowVersion, interaction.UpdatedAt,
			nullableTime(interaction.TerminalAt), interaction.ID,
		)
	}
}

func (t *transaction) FindOperationByKey(interactionID, operationKey string) (sessionvo.Operation, bool) {
	if t.err != nil {
		return sessionvo.Operation{}, false
	}
	return t.scanOperation(t.tx.QueryRowContext(t.ctx, operationSelect+`
		WHERE interaction_id=? AND operation_key=? FOR UPDATE`, interactionID, operationKey))
}

func (t *transaction) FindOperation(operationID string) (sessionvo.Operation, bool) {
	if t.err != nil {
		return sessionvo.Operation{}, false
	}
	return t.scanOperation(t.tx.QueryRowContext(t.ctx, operationSelect+`
		WHERE operation_id=? FOR UPDATE`, operationID))
}

func (t *transaction) PeekOperation(operationID string) (sessionvo.Operation, bool) {
	if t.err != nil {
		return sessionvo.Operation{}, false
	}
	return t.scanOperation(t.tx.QueryRowContext(t.ctx, operationSelect+`
		WHERE operation_id=?`, operationID))
}

func (t *transaction) ListOperationsByIDs(operationIDs []string) map[string]sessionvo.Operation {
	result := make(map[string]sessionvo.Operation, len(operationIDs))
	if t.err != nil {
		return result
	}
	ids := uniqueStoreIDs(operationIDs)
	if len(ids) == 0 {
		return result
	}
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	rows, err := t.tx.QueryContext(t.ctx, operationSelect+` WHERE operation_id IN (`+storePlaceholders(len(ids))+`)`, args...)
	if err != nil {
		t.err = err
		return result
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		value, scanErr := scanOperationRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return result
		}
		result[value.ID] = value
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListOperations(interactionID string) []sessionvo.Operation {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, operationSelect+`
		WHERE interaction_id=? ORDER BY operation_id`, interactionID)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Operation
	for rows.Next() {
		value, scanErr := scanOperationRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) SaveOperation(operation sessionvo.Operation) {
	if t.err != nil {
		return
	}
	causation := marshalJSON(operation.CausationEventIDs)
	var exists int
	err := t.tx.QueryRowContext(t.ctx,
		"SELECT 1 FROM bkn_trace_operations WHERE operation_id=?",
		operation.ID,
	).Scan(&exists)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, t.err = t.tx.ExecContext(t.ctx, `
			INSERT INTO bkn_trace_operations (
				operation_id, conversation_id, interaction_id, operation_key, tool_name,
				parent_operation_id, causation_event_ids,
				attempt_no, attempt_status, retryable, row_version, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?)`,
			operation.ID, operation.ConversationID, operation.InteractionID, operation.OperationKey,
			operation.ToolName, operation.ParentOperationID,
			causation, operation.Attempt, operation.AttemptStatus, operation.Retryable,
			operation.RowVersion, operation.CreatedAt, operation.UpdatedAt,
		)
	case err != nil:
		t.err = err
	default:
		_, t.err = t.tx.ExecContext(t.ctx, `
			UPDATE bkn_trace_operations SET attempt_no=?, attempt_status=?, retryable=?,
				row_version=?, updated_at=? WHERE operation_id=?`,
			operation.Attempt, operation.AttemptStatus, operation.Retryable,
			operation.RowVersion, operation.UpdatedAt, operation.ID,
		)
	}
}

func (t *transaction) FindOperationCallFact(
	operationID string,
	attempt uint32,
) (sessionvo.OperationCallFact, bool) {
	if t.err != nil {
		return sessionvo.OperationCallFact{}, false
	}
	return t.scanOperationCallFact(t.tx.QueryRowContext(t.ctx, operationCallFactSelect+`
		WHERE operation_id=? AND attempt_no=? FOR UPDATE`, operationID, attempt))
}

func (t *transaction) ListOperationCallFacts(interactionID string) []sessionvo.OperationCallFact {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, operationCallFactSelect+`
		WHERE interaction_id=? ORDER BY started_at, operation_id, attempt_no`, interactionID)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	result := make([]sessionvo.OperationCallFact, 0)
	for rows.Next() {
		value, found := t.scanOperationCallFact(rows)
		if !found {
			return nil
		}
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListOperationCallFactsByTraceID(traceID string) []sessionvo.OperationCallFact {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, operationCallFactSelect+`
		WHERE trace_id=? ORDER BY started_at, operation_id, attempt_no`, traceID)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	result := make([]sessionvo.OperationCallFact, 0)
	for rows.Next() {
		value, found := t.scanOperationCallFact(rows)
		if !found {
			return nil
		}
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListFirstOperationSourceModulesByTraceIDs(traceIDs []string) map[string]string {
	if t.err != nil {
		return nil
	}
	uniqueTraceIDs := make([]string, 0, len(traceIDs))
	seen := make(map[string]struct{}, len(traceIDs))
	for _, traceID := range traceIDs {
		if traceID == "" {
			continue
		}
		if _, found := seen[traceID]; found {
			continue
		}
		seen[traceID] = struct{}{}
		uniqueTraceIDs = append(uniqueTraceIDs, traceID)
	}
	if len(uniqueTraceIDs) == 0 {
		return map[string]string{}
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(uniqueTraceIDs)), ",")
	args := make([]any, len(uniqueTraceIDs))
	for index, traceID := range uniqueTraceIDs {
		args[index] = traceID
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT trace_id, source_module
		FROM bkn_trace_operation_call_facts
		WHERE trace_id IN (`+placeholders+`) AND source_module <> ''
		ORDER BY trace_id, started_at, operation_id, attempt_no`, args...)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]string, len(uniqueTraceIDs))
	for rows.Next() {
		var traceID, sourceModule string
		if err := rows.Scan(&traceID, &sourceModule); err != nil {
			t.err = err
			return nil
		}
		if _, found := result[traceID]; !found {
			result[traceID] = sourceModule
		}
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) SaveOperationCallFact(fact sessionvo.OperationCallFact) {
	if t.err != nil {
		return
	}
	var exists int
	err := t.tx.QueryRowContext(t.ctx,
		"SELECT 1 FROM bkn_trace_operation_call_facts WHERE operation_id=? AND attempt_no=?",
		fact.OperationID, fact.Attempt,
	).Scan(&exists)
	input := marshalJSON(fact.Input)
	output := marshalOptionalPayload(fact.Output)
	errorPayload := marshalOptionalPayload(fact.Error)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, t.err = t.tx.ExecContext(t.ctx, `
			INSERT INTO bkn_trace_operation_call_facts (
				operation_id, attempt_no, conversation_id, interaction_id, receipt_id,
				tool_name, protocol, source_module, parent_operation_id,
				input_payload, output_payload, error_payload,
				request_id, trace_id, span_id, started_at, finished_at, status, retryable
			) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''),
				NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?)`,
			fact.OperationID, fact.Attempt, fact.ConversationID, fact.InteractionID,
			fact.ReceiptID, fact.ToolName, fact.Protocol, fact.SourceModule,
			fact.ParentOperationID, input, output, errorPayload,
			fact.RequestID, fact.TraceID, fact.SpanID, fact.StartedAt,
			nullableTime(fact.FinishedAt), fact.Status, fact.Retryable,
		)
	case err != nil:
		t.err = err
	default:
		_, t.err = t.tx.ExecContext(t.ctx, `
			UPDATE bkn_trace_operation_call_facts SET receipt_id=NULLIF(?, ''),
				output_payload=NULLIF(?, ''), error_payload=NULLIF(?, ''),
				request_id=NULLIF(?, ''), trace_id=NULLIF(?, ''), span_id=NULLIF(?, ''), finished_at=?,
				status=?, retryable=?
			WHERE operation_id=? AND attempt_no=?`,
			fact.ReceiptID, output, errorPayload, fact.RequestID, fact.TraceID, fact.SpanID,
			nullableTime(fact.FinishedAt), fact.Status, fact.Retryable,
			fact.OperationID, fact.Attempt,
		)
	}
}

func (t *transaction) FindReceipt(receiptID string) (sessionvo.Receipt, bool) {
	if t.err != nil {
		return sessionvo.Receipt{}, false
	}
	return t.scanReceipt(t.tx.QueryRowContext(t.ctx, receiptSelect+`
		WHERE receipt_id=? FOR UPDATE`, receiptID))
}

func (t *transaction) PeekReceipt(receiptID string) (sessionvo.Receipt, bool) {
	if t.err != nil {
		return sessionvo.Receipt{}, false
	}
	return t.scanReceipt(t.tx.QueryRowContext(t.ctx, receiptSelect+`
		WHERE receipt_id=?`, receiptID))
}

func (t *transaction) FindReceiptByOperationAttempt(operationID string, attempt uint32) (sessionvo.Receipt, bool) {
	if t.err != nil {
		return sessionvo.Receipt{}, false
	}
	return t.scanReceipt(t.tx.QueryRowContext(t.ctx, receiptSelect+`
		WHERE operation_id=? AND attempt_no=? FOR UPDATE`, operationID, attempt))
}

func (t *transaction) ListReceipts(interactionID string) []sessionvo.Receipt {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, receiptSelect+`
		WHERE interaction_id=? ORDER BY receipt_id`, interactionID)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Receipt
	for rows.Next() {
		value, scanErr := scanReceiptRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) SaveReceipt(receipt sessionvo.Receipt) {
	if t.err != nil {
		return
	}
	var exists int
	err := t.tx.QueryRowContext(t.ctx,
		"SELECT 1 FROM bkn_trace_receipts WHERE receipt_id=?",
		receipt.ID,
	).Scan(&exists)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, t.err = t.tx.ExecContext(t.ctx, `
			INSERT INTO bkn_trace_receipts (
				receipt_id, schema_version, tenant_id, business_domain_id,
				application_principal_id, effective_subject_type, effective_subject_id,
				delegation_id, conversation_id, interaction_id, operation_id, attempt_no,
				operation_key, tool_name, receipt_status,
				evidence_durability, required_receipt, request_id, trace_id,
				causation_event_ids, observed_evidence_refs, business_refs, artifact_refs,
				partial_reasons, row_version, issued_at, terminal_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
				NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
				NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)`,
			receipt.ID, receipt.SchemaVersion, receipt.Owner.TenantID, receipt.Owner.BusinessDomainID,
			receipt.Owner.ApplicationPrincipalID, receipt.Owner.EffectiveSubjectType,
			receipt.Owner.EffectiveSubjectID, receipt.Owner.DelegationID,
			receipt.ConversationID, receipt.InteractionID, receipt.OperationID, receipt.Attempt,
			receipt.OperationKey, receipt.ToolName, receipt.Status,
			receipt.EvidenceDurability, receipt.Required, receipt.RequestID,
			receipt.TraceID, marshalJSON(receipt.CausationEventIDs),
			marshalJSON(receipt.ObservedEvidenceRefs), marshalJSON(receipt.BusinessRefs),
			marshalJSON(receipt.ArtifactRefs), marshalJSON(receipt.PartialReasons),
			receipt.RowVersion, receipt.IssuedAt, nullableTime(receipt.TerminalAt),
		)
	case err != nil:
		t.err = err
	default:
		_, t.err = t.tx.ExecContext(t.ctx, `
			UPDATE bkn_trace_receipts SET receipt_status=?, evidence_durability=?,
				request_id=NULLIF(?, ''), trace_id=NULLIF(?, ''), observed_evidence_refs=NULLIF(?, ''),
				business_refs=NULLIF(?, ''), artifact_refs=NULLIF(?, ''),
				partial_reasons=NULLIF(?, ''), row_version=?, terminal_at=?
			WHERE receipt_id=?`,
			receipt.Status, receipt.EvidenceDurability, receipt.RequestID, receipt.TraceID,
			marshalJSON(receipt.ObservedEvidenceRefs), marshalJSON(receipt.BusinessRefs),
			marshalJSON(receipt.ArtifactRefs), marshalJSON(receipt.PartialReasons),
			receipt.RowVersion, nullableTime(receipt.TerminalAt), receipt.ID,
		)
	}
}

func (t *transaction) ListRequests(owner sessionvo.Owner, limit int) []sessionvo.RequestSummary {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, `
		SELECT request_id, MIN(conversation_id), MIN(interaction_id),
			COUNT(DISTINCT operation_id), COUNT(*),
			MAX(COALESCE(terminal_at, issued_at))
		FROM bkn_trace_receipts
		WHERE tenant_id=? AND business_domain_id=? AND application_principal_id=?
		  AND effective_subject_type=? AND effective_subject_id=? AND delegation_id=?
		  AND request_id IS NOT NULL AND request_id<>''
		GROUP BY request_id
		ORDER BY MAX(COALESCE(terminal_at, issued_at)) DESC, request_id DESC
		LIMIT ?`,
		owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, limit,
	)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.RequestSummary
	for rows.Next() {
		var value sessionvo.RequestSummary
		if err := rows.Scan(
			&value.RequestID, &value.ConversationID, &value.InteractionID,
			&value.OperationCount, &value.ReceiptCount, &value.UpdatedAt,
		); err != nil {
			t.err = err
			return nil
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		t.err = err
		return nil
	}
	if err := rows.Close(); err != nil {
		t.err = err
		return nil
	}
	for index := range result {
		result[index].TraceIDs = t.listRequestTraceIDs(owner, result[index].RequestID)
	}
	return result
}

func (t *transaction) FindRequest(owner sessionvo.Owner, requestID string) (sessionvo.RequestSummary, bool) {
	if t.err != nil {
		return sessionvo.RequestSummary{}, false
	}
	var value sessionvo.RequestSummary
	err := t.tx.QueryRowContext(t.ctx, `
		SELECT request_id, MIN(conversation_id), MIN(interaction_id),
			COUNT(DISTINCT operation_id), COUNT(*),
			MAX(COALESCE(terminal_at, issued_at))
		FROM bkn_trace_receipts
		WHERE tenant_id=? AND business_domain_id=? AND application_principal_id=?
		  AND effective_subject_type=? AND effective_subject_id=? AND delegation_id=?
		  AND request_id=?
		GROUP BY request_id`,
		owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, requestID,
	).Scan(
		&value.RequestID, &value.ConversationID, &value.InteractionID,
		&value.OperationCount, &value.ReceiptCount, &value.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.RequestSummary{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.RequestSummary{}, false
	}
	value.TraceIDs = t.listRequestTraceIDs(owner, requestID)
	return value, true
}

func (t *transaction) listRequestTraceIDs(owner sessionvo.Owner, requestID string) []string {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, `
		SELECT DISTINCT trace_id
		FROM bkn_trace_receipts
		WHERE tenant_id=? AND business_domain_id=? AND application_principal_id=?
		  AND effective_subject_type=? AND effective_subject_id=? AND delegation_id=?
		  AND request_id=? AND trace_id IS NOT NULL AND trace_id<>''
		ORDER BY trace_id`,
		owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, requestID,
	)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []string
	for rows.Next() {
		var traceID string
		if err := rows.Scan(&traceID); err != nil {
			t.err = err
			return nil
		}
		result = append(result, traceID)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListExpiredActiveInteractions(limit int) []sessionvo.Interaction {
	if t.err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := t.tx.QueryContext(t.ctx, interactionSelect+`
		WHERE execution_status='active' AND lease_expires_at<=UTC_TIMESTAMP(6)
		ORDER BY lease_expires_at, interaction_id LIMIT ?`, limit)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Interaction
	for rows.Next() {
		interaction, scanErr := scanInteractionRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, interaction)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListIdleOneShotConversations(
	cutoff time.Time,
	limit int,
) []sessionvo.Conversation {
	if t.err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := t.tx.QueryContext(t.ctx, conversationSelect+`
		WHERE one_shot=TRUE AND status='active' AND updated_at<=?
		  AND NOT EXISTS (
			SELECT 1 FROM bkn_trace_interactions
			WHERE bkn_trace_interactions.conversation_id=bkn_trace_conversations.conversation_id
		  )
		ORDER BY updated_at, conversation_id LIMIT ? FOR UPDATE`, cutoff, limit)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Conversation
	for rows.Next() {
		conversation, scanErr := scanConversationRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, conversation)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListAssemblyDueInteractions(limit int) []sessionvo.Interaction {
	if t.err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := t.tx.QueryContext(t.ctx, interactionSelect+`
		WHERE evidence_status='assembling' AND assembler_deadline<=UTC_TIMESTAMP(6)
		ORDER BY assembler_deadline, interaction_id LIMIT ?`, limit)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Interaction
	for rows.Next() {
		interaction, scanErr := scanInteractionRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, interaction)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) AppendProjection(mutation sessionvo.ProjectionMutation) {
	if t.err != nil {
		return
	}
	_, t.err = t.tx.ExecContext(t.ctx, `
		INSERT INTO bkn_trace_projection_outbox (
			aggregate_type, aggregate_id, aggregate_version, event_type, event_id, payload,
			status, attempts, available_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
		mutation.AggregateType, mutation.AggregateID, mutation.AggregateVersion,
		mutation.EventType,
		mutation.EventID, mutation.Payload, t.now, t.now,
	)
}

func (t *transaction) ListAssemblyRevisions(interactionID string) []sessionvo.AssemblyRevision {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, `
		SELECT revision_id, revision_no, COALESCE(parent_revision_id, ''), interaction_id,
			completion_manifest_version, included_receipt_ids, included_event_ids,
			artifact_manifest_hash, assembly_completeness, COALESCE(partial_reasons, ''),
			trigger_type, created_at
		FROM bkn_trace_assembly_revisions
		WHERE interaction_id=? ORDER BY revision_no`, interactionID)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.AssemblyRevision
	for rows.Next() {
		var value sessionvo.AssemblyRevision
		var receipts, events, reasons string
		if err := rows.Scan(
			&value.ID, &value.RevisionNo, &value.ParentRevisionID, &value.InteractionID,
			&value.CompletionManifestVersion, &receipts, &events, &value.ArtifactManifestHash,
			&value.Completeness, &reasons, &value.Trigger, &value.CreatedAt,
		); err != nil {
			t.err = err
			return nil
		}
		unmarshalJSON(receipts, &value.IncludedReceiptIDs)
		unmarshalJSON(events, &value.IncludedEventIDs)
		unmarshalJSON(reasons, &value.PartialReasons)
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListAssemblyRevisionsByInteractionIDs(interactionIDs []string) map[string][]sessionvo.AssemblyRevision {
	result := make(map[string][]sessionvo.AssemblyRevision, len(interactionIDs))
	if t.err != nil {
		return result
	}
	ids := uniqueStoreIDs(interactionIDs)
	if len(ids) == 0 {
		return result
	}
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	rows, err := t.tx.QueryContext(t.ctx, `
		SELECT revision_id, revision_no, COALESCE(parent_revision_id, ''), interaction_id,
			completion_manifest_version, included_receipt_ids, included_event_ids,
			artifact_manifest_hash, assembly_completeness, COALESCE(partial_reasons, ''),
			trigger_type, created_at
		FROM bkn_trace_assembly_revisions
		WHERE interaction_id IN (`+storePlaceholders(len(ids))+`) ORDER BY interaction_id, revision_no`, args...)
	if err != nil {
		t.err = err
		return result
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var value sessionvo.AssemblyRevision
		var receipts, events, reasons string
		if scanErr := rows.Scan(&value.ID, &value.RevisionNo, &value.ParentRevisionID, &value.InteractionID, &value.CompletionManifestVersion, &receipts, &events, &value.ArtifactManifestHash, &value.Completeness, &reasons, &value.Trigger, &value.CreatedAt); scanErr != nil {
			t.err = scanErr
			return result
		}
		unmarshalJSON(receipts, &value.IncludedReceiptIDs)
		unmarshalJSON(events, &value.IncludedEventIDs)
		unmarshalJSON(reasons, &value.PartialReasons)
		result[value.InteractionID] = append(result[value.InteractionID], value)
	}
	t.err = rows.Err()
	return result
}

func uniqueStoreIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func storePlaceholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func (t *transaction) SaveAssemblyRevision(revision sessionvo.AssemblyRevision) {
	if t.err != nil {
		return
	}
	_, t.err = t.tx.ExecContext(t.ctx, `
		INSERT INTO bkn_trace_assembly_revisions (
			revision_id, interaction_id, revision_no, parent_revision_id,
			completion_manifest_version, included_receipt_ids, included_event_ids,
			artifact_manifest_hash, assembly_completeness, partial_reasons,
			trigger_type, created_at
		) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
		revision.ID, revision.InteractionID, revision.RevisionNo, revision.ParentRevisionID,
		revision.CompletionManifestVersion, marshalJSON(revision.IncludedReceiptIDs),
		marshalJSON(revision.IncludedEventIDs), revision.ArtifactManifestHash,
		revision.Completeness, marshalJSON(revision.PartialReasons),
		revision.Trigger, revision.CreatedAt,
	)
}

const conversationSelect = `SELECT conversation_id, tenant_id, business_domain_id,
	application_principal_id, agent_name, actor_name_snapshot, creation_auth_method,
	effective_subject_type, effective_subject_id,
	COALESCE(delegation_id, ''), external_conversation_key, generation, status,
	one_shot, row_version, created_at, updated_at, closed_at
	FROM bkn_trace_conversations`

type rowScanner interface {
	Scan(dest ...any) error
}

func (t *transaction) scanConversation(row rowScanner) (sessionvo.Conversation, bool) {
	value, err := scanConversationRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.Conversation{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.Conversation{}, false
	}
	return value, true
}

func scanConversationRows(row rowScanner) (sessionvo.Conversation, error) {
	var value sessionvo.Conversation
	var closedAt sql.NullTime
	err := row.Scan(
		&value.ID, &value.Owner.TenantID, &value.Owner.BusinessDomainID,
		&value.Owner.ApplicationPrincipalID, &value.AgentName,
		&value.ActorNameSnapshot, &value.CreationAuthMethod, &value.Owner.EffectiveSubjectType,
		&value.Owner.EffectiveSubjectID, &value.Owner.DelegationID,
		&value.ExternalConversationKey, &value.Generation, &value.Status,
		&value.OneShot, &value.RowVersion, &value.CreatedAt, &value.UpdatedAt, &closedAt,
	)
	if err != nil {
		return sessionvo.Conversation{}, err
	}
	if closedAt.Valid {
		value.ClosedAt = &closedAt.Time
	}
	return value, nil
}

const interactionSelect = `SELECT interaction_id, conversation_id, ordinal_no,
	execution_status, evidence_status, start_idempotency_key,
	COALESCE(terminal_idempotency_key, ''), COALESCE(terminal_payload_hash, ''),
	COALESCE(closure_manifest, ''), lease_token, lease_epoch, lease_version,
	lease_expires_at, row_version, created_at, updated_at, terminal_at
	FROM bkn_trace_interactions`

func (t *transaction) scanInteraction(row rowScanner) (sessionvo.Interaction, bool) {
	value, err := scanInteractionRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.Interaction{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.Interaction{}, false
	}
	return value, true
}

func scanInteractionRows(row rowScanner) (sessionvo.Interaction, error) {
	var value sessionvo.Interaction
	var manifest string
	var terminalAt sql.NullTime
	err := row.Scan(
		&value.ID, &value.ConversationID, &value.Ordinal, &value.ExecutionStatus,
		&value.EvidenceStatus, &value.StartIdempotencyKey,
		&value.TerminalIdempotencyKey, &value.TerminalPayloadHash, &manifest,
		&value.LeaseToken, &value.LeaseEpoch, &value.LeaseVersion,
		&value.LeaseExpiresAt, &value.RowVersion, &value.CreatedAt, &value.UpdatedAt, &terminalAt,
	)
	if err != nil {
		return sessionvo.Interaction{}, err
	}
	if manifest != "" {
		var decoded sessionvo.ClosureManifest
		if err := json.Unmarshal([]byte(manifest), &decoded); err != nil {
			return sessionvo.Interaction{}, err
		}
		value.ClosureManifest = &decoded
	}
	if terminalAt.Valid {
		value.TerminalAt = &terminalAt.Time
	}
	return value, nil
}

const operationSelect = `SELECT operation_id, conversation_id, interaction_id,
	operation_key, tool_name, COALESCE(parent_operation_id, ''),
	COALESCE(causation_event_ids, ''), attempt_no, attempt_status, retryable,
	row_version, created_at, updated_at FROM bkn_trace_operations`

func (t *transaction) scanOperation(row rowScanner) (sessionvo.Operation, bool) {
	value, err := scanOperationRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.Operation{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.Operation{}, false
	}
	return value, true
}

func scanOperationRows(row rowScanner) (sessionvo.Operation, error) {
	var value sessionvo.Operation
	var causation string
	err := row.Scan(
		&value.ID, &value.ConversationID, &value.InteractionID, &value.OperationKey,
		&value.ToolName, &value.ParentOperationID, &causation,
		&value.Attempt, &value.AttemptStatus, &value.Retryable, &value.RowVersion,
		&value.CreatedAt, &value.UpdatedAt,
	)
	if err != nil {
		return sessionvo.Operation{}, err
	}
	unmarshalJSON(causation, &value.CausationEventIDs)
	return value, nil
}

const operationCallFactSelect = `SELECT operation_id, attempt_no, conversation_id,
	interaction_id, COALESCE(receipt_id, ''), tool_name, protocol, source_module,
	COALESCE(parent_operation_id, ''), input_payload,
	COALESCE(output_payload, ''), COALESCE(error_payload, ''),
	COALESCE(request_id, ''), COALESCE(trace_id, ''), COALESCE(span_id, ''), started_at, finished_at,
	status, retryable FROM bkn_trace_operation_call_facts `

func (t *transaction) scanOperationCallFact(row rowScanner) (sessionvo.OperationCallFact, bool) {
	var value sessionvo.OperationCallFact
	var input, output, errorPayload string
	var finishedAt sql.NullTime
	err := row.Scan(
		&value.OperationID, &value.Attempt, &value.ConversationID, &value.InteractionID,
		&value.ReceiptID, &value.ToolName, &value.Protocol, &value.SourceModule,
		&value.ParentOperationID, &input, &output, &errorPayload,
		&value.RequestID, &value.TraceID, &value.SpanID, &value.StartedAt, &finishedAt,
		&value.Status, &value.Retryable,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.OperationCallFact{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.OperationCallFact{}, false
	}
	if err := json.Unmarshal([]byte(input), &value.Input); err != nil {
		t.err = err
		return sessionvo.OperationCallFact{}, false
	}
	if output != "" {
		value.Output = &sessionvo.PayloadEnvelope{}
		if err := json.Unmarshal([]byte(output), value.Output); err != nil {
			t.err = err
			return sessionvo.OperationCallFact{}, false
		}
	}
	if errorPayload != "" {
		value.Error = &sessionvo.PayloadEnvelope{}
		if err := json.Unmarshal([]byte(errorPayload), value.Error); err != nil {
			t.err = err
			return sessionvo.OperationCallFact{}, false
		}
	}
	if finishedAt.Valid {
		value.FinishedAt = &finishedAt.Time
	}
	return value, true
}

func marshalOptionalPayload(payload *sessionvo.PayloadEnvelope) string {
	if payload == nil {
		return ""
	}
	return marshalJSON(*payload)
}

const receiptSelect = `SELECT receipt_id, schema_version, tenant_id, business_domain_id,
	application_principal_id, effective_subject_type, effective_subject_id,
	COALESCE(delegation_id, ''), conversation_id, interaction_id, operation_id,
	attempt_no, operation_key, tool_name, receipt_status,
	evidence_durability, required_receipt, COALESCE(request_id, ''), COALESCE(trace_id, ''),
	COALESCE(causation_event_ids, ''), COALESCE(observed_evidence_refs, ''),
	COALESCE(business_refs, ''), COALESCE(artifact_refs, ''), COALESCE(partial_reasons, ''),
	row_version, issued_at, terminal_at FROM bkn_trace_receipts`

func (t *transaction) scanReceipt(row rowScanner) (sessionvo.Receipt, bool) {
	value, err := scanReceiptRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.Receipt{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.Receipt{}, false
	}
	return value, true
}

func scanReceiptRows(row rowScanner) (sessionvo.Receipt, error) {
	var value sessionvo.Receipt
	var causation, evidence, business, artifacts, reasons string
	var terminalAt sql.NullTime
	err := row.Scan(
		&value.ID, &value.SchemaVersion, &value.Owner.TenantID, &value.Owner.BusinessDomainID,
		&value.Owner.ApplicationPrincipalID, &value.Owner.EffectiveSubjectType,
		&value.Owner.EffectiveSubjectID, &value.Owner.DelegationID,
		&value.ConversationID, &value.InteractionID, &value.OperationID, &value.Attempt,
		&value.OperationKey, &value.ToolName,
		&value.Status, &value.EvidenceDurability, &value.Required,
		&value.RequestID, &value.TraceID, &causation, &evidence, &business, &artifacts,
		&reasons, &value.RowVersion, &value.IssuedAt, &terminalAt,
	)
	if err != nil {
		return sessionvo.Receipt{}, err
	}
	unmarshalJSON(causation, &value.CausationEventIDs)
	unmarshalJSON(evidence, &value.ObservedEvidenceRefs)
	unmarshalJSON(business, &value.BusinessRefs)
	unmarshalJSON(artifacts, &value.ArtifactRefs)
	unmarshalJSON(reasons, &value.PartialReasons)
	if terminalAt.Valid {
		value.TerminalAt = &terminalAt.Time
	}
	return value, nil
}

func marshalJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" {
		return ""
	}
	return string(data)
}

func unmarshalJSON(data string, target any) {
	if data != "" {
		_ = json.Unmarshal([]byte(data), target)
	}
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}
