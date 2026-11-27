// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

type cursorSessionManager struct {
	mu                    sync.Mutex
	sessions              map[string]*interfaces.CursorSession
	reclaimRequested      chan struct{}
	maxSessions           int
	maxSessionsPerAccount int
}

const defaultCursorMaxSessions = 1000
const defaultCursorMaxSessionsPerAccount = 100

var errCursorSessionLimitReached = errors.New("cursor session limit reached")
var errCursorAccountLimitReached = errors.New("account cursor session limit reached")
var errCursorSessionBusy = errors.New("cursor session is busy")
var errCursorSessionNotFound = errors.New("cursor session not found")
var errCursorSessionForbidden = errors.New("cursor session belongs to another account")

var rawQueryCursorSessions = newCursorSessionManager(defaultCursorMaxSessions, defaultCursorMaxSessionsPerAccount)

// ConfigureCursorSessions applies the shared Raw Query and resource-data limits.
func ConfigureCursorSessions(maxSessions, maxSessionsPerAccount int) {
	rawQueryCursorSessions.configure(maxSessions, maxSessionsPerAccount)
}

func init() {
	go rawQueryCursorSessions.reclaimExpired(context.Background())
}

func newCursorSessionManager(maxSessions, maxSessionsPerAccount int) *cursorSessionManager {
	m := &cursorSessionManager{
		sessions:         make(map[string]*interfaces.CursorSession),
		reclaimRequested: make(chan struct{}, 1),
	}
	m.configure(maxSessions, maxSessionsPerAccount)
	return m
}

func (m *cursorSessionManager) configure(maxSessions, perAccount int) {
	if maxSessions <= 0 {
		maxSessions = defaultCursorMaxSessions
	}
	accountLimit := defaultCursorMaxSessionsPerAccount
	if perAccount > 0 {
		accountLimit = perAccount
	}
	if maxSessions > 1 && accountLimit >= maxSessions {
		accountLimit = maxSessions - 1
	}
	m.mu.Lock()
	m.maxSessions = maxSessions
	m.maxSessionsPerAccount = accountLimit
	m.mu.Unlock()
}

func (m *cursorSessionManager) register(session *interfaces.CursorSession) (int, error) {
	const reclaimThreshold = 0.8

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) >= m.maxSessions {
		m.requestReclaim()
		return 0, errCursorSessionLimitReached
	}
	accountCount := 0
	for _, existing := range m.sessions {
		if existing.QuotaOwnerID == session.QuotaOwnerID {
			accountCount++
		}
	}
	if accountCount >= m.maxSessionsPerAccount {
		m.requestReclaim()
		return 0, errCursorAccountLimitReached
	}

	m.sessions[session.ID] = session
	globalThreshold := int(math.Ceil(float64(m.maxSessions) * reclaimThreshold))
	accountThreshold := int(math.Ceil(float64(m.maxSessionsPerAccount) * reclaimThreshold))
	if len(m.sessions) >= globalThreshold || accountCount+1 >= accountThreshold {
		m.requestReclaim()
	}
	return len(m.sessions), nil
}

func (m *cursorSessionManager) create(accountID, catalogID string, resourceIDs []string, limit, keepAliveSec, queryTimeoutSec int) (*interfaces.CursorSession, error) {
	if keepAliveSec == 0 {
		keepAliveSec = interfaces.DefaultCursorKeepAliveSec
	}
	now := time.Now().Unix()
	session := &interfaces.CursorSession{
		ID:              uuid.NewV7().String(),
		AccountID:       accountID,
		QuotaOwnerID:    accountID,
		CatalogID:       catalogID,
		ResourceIDs:     append([]string(nil), resourceIDs...),
		QueryFormat:     interfaces.QueryFormatSQL,
		PageLimit:       limit,
		KeepAliveSec:    keepAliveSec,
		QueryTimeoutSec: queryTimeoutSec,
		CreatedAtSec:    now,
		ExpiresAtSec:    now + int64(keepAliveSec),
	}
	activeSessionsLen, err := m.register(session)
	if err != nil {
		return nil, err
	}
	logger.Debugf("Cursor session created: catalog_id=%s, query_format=%s, active_sessions_len=%d", session.CatalogID, session.QueryFormat, activeSessionsLen)
	return session, nil
}

func (m *cursorSessionManager) createResourceData(accountID, quotaOwnerID string, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.CursorSession, error) {
	keepAliveSec := params.Paging.KeepAliveSec
	if keepAliveSec == 0 {
		keepAliveSec = interfaces.DefaultCursorKeepAliveSec
	}
	now := time.Now().Unix()
	session := &interfaces.CursorSession{
		ID:                     uuid.NewV7().String(),
		AccountID:              accountID,
		QuotaOwnerID:           quotaOwnerID,
		CatalogID:              resource.CatalogID,
		ResourceIDs:            []string{resource.ID},
		ResourceDataResourceID: resource.ID,
		ResourceDataUpdateTime: resource.UpdateTime,
		ResourceDataParams:     cloneResourceDataQueryParams(params),
		ResourceDataCategory:   resource.Category,
		PageLimit:              params.Paging.Limit,
		KeepAliveSec:           keepAliveSec,
		CreatedAtSec:           now,
		ExpiresAtSec:           now + int64(keepAliveSec),
	}
	activeSessionsLen, err := m.register(session)
	if err != nil {
		return nil, err
	}
	logger.Debugf("Cursor session created: catalog_id=%s, query_format=resource_data, active_sessions_len=%d", session.CatalogID, activeSessionsLen)
	return session, nil
}

func cloneResourceDataQueryParams(params *interfaces.ResourceDataQueryParams) *interfaces.ResourceDataQueryParams {
	copy := *params
	copy.Sort = append([]*interfaces.SortField(nil), params.Sort...)
	copy.OutputFields = append([]string(nil), params.OutputFields...)
	copy.GroupBy = append([]*interfaces.GroupByItem(nil), params.GroupBy...)
	return &copy
}

// reclaimExpired runs a single sweep after a wake-up or periodic tick.
func (m *cursorSessionManager) reclaimExpired(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.reclaimRequested:
		}
		m.closeExpiredSessions(time.Now().Unix())
	}
}

func (m *cursorSessionManager) requestReclaim() {
	select {
	case m.reclaimRequested <- struct{}{}:
	default:
	}
}

// acquire returns a session only when this request obtains exclusive ownership
// immediately. The session remains registered so it still counts toward the
// global limit and remains visible to the expiry reclaimer while in use.
func (m *cursorSessionManager) acquire(cursor string) (*interfaces.CursorSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[cursor]
	if err := m.lockActiveSession(session); err != nil {
		return nil, false
	}
	return session, true
}

func (m *cursorSessionManager) acquireResourceData(cursor, accountID, resourceID string) (*interfaces.CursorSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[cursor]
	err := m.lockActiveSession(session)
	if errors.Is(err, errCursorSessionBusy) {
		// 只向匹配账号和资源的调用方报告游标正在使用。
		if session.ResourceDataParams == nil || session.AccountID != accountID || session.ResourceDataResourceID != resourceID ||
			time.Now().Unix() >= atomic.LoadInt64(&session.ExpiresAtSec) {
			err = errCursorSessionNotFound
		}
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

// 调用方持有管理器锁。成功时由调用方释放会话锁。
func (m *cursorSessionManager) lockActiveSession(session *interfaces.CursorSession) error {
	if session == nil {
		return errCursorSessionNotFound
	}
	if !session.TryLock() {
		return errCursorSessionBusy
	}
	if time.Now().Unix() >= atomic.LoadInt64(&session.ExpiresAtSec) {
		m.requestReclaim()
		session.Unlock()
		return errCursorSessionNotFound
	}
	return nil
}

func (m *cursorSessionManager) release(session *interfaces.CursorSession) {
	session.Unlock()
}

func (m *cursorSessionManager) remove(cursor string) {
	m.mu.Lock()
	session, ok := m.removeLocked(cursor)
	m.mu.Unlock()
	if ok {
		closeSQLCursorResources(session)
	}
}

func (m *cursorSessionManager) closeSession(cursor string) {
	m.mu.Lock()
	session, ok := m.removeLocked(cursor)
	activeSessions := len(m.sessions)
	m.mu.Unlock()
	if ok {
		closeSQLCursorResources(session)
		logger.Debugf("Cursor session closed at final page: catalog_id=%s, active_sessions=%d", session.CatalogID, activeSessions)
	}
}

func (m *cursorSessionManager) closeForAccount(accountID, cursor string) error {
	m.mu.Lock()
	session, ok := m.sessions[cursor]
	if !ok {
		m.mu.Unlock()
		return errCursorSessionNotFound
	}
	if session.AccountID != accountID {
		m.mu.Unlock()
		return errCursorSessionForbidden
	}
	if !session.TryLock() {
		m.mu.Unlock()
		return errCursorSessionBusy
	}
	if time.Now().Unix() >= atomic.LoadInt64(&session.ExpiresAtSec) {
		m.requestReclaim()
		session.Unlock()
		m.mu.Unlock()
		return errCursorSessionNotFound
	}
	defer session.Unlock()
	m.removeLocked(cursor)
	m.mu.Unlock()
	closeSQLCursorResources(session)
	return nil
}

// closeSQLCursorResources 在移除会话后调用；网络资源清理不持有会话管理器的全局锁。
func closeSQLCursorResources(session *interfaces.CursorSession) {
	if session.ReadSession != nil {
		if err := session.ReadSession.Close(); err != nil {
			logger.Warnf("Failed to close source query page session")
		}
	}
	if session.ReadConnector != nil {
		_ = session.ReadConnector.Close(context.Background())
	}
}

// CloseCursorSession releases a cursor owned by the calling account.
func CloseCursorSession(ctx context.Context, accountID, cursor string) error {
	err := rawQueryCursorSessions.closeForAccount(accountID, cursor)
	switch {
	case errors.Is(err, errCursorSessionNotFound):
		return cursorNotFoundError(ctx)
	case errors.Is(err, errCursorSessionForbidden):
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor does not belong to the current account")
	case errors.Is(err, errCursorSessionBusy):
		return cursorInUseError(ctx)
	default:
		return err
	}
}

func cursorInUseError(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_CursorInUse).
		WithErrorDetails("cursor is in use, please retry later")
}

func (m *cursorSessionManager) markPageSuccess(session *interfaces.CursorSession) {
	now := time.Now().Unix()
	session.LastSuccessfulPageAtSec = now
	atomic.StoreInt64(&session.ExpiresAtSec, now+int64(session.KeepAliveSec))
}

// 调用方持有会话锁。换发游标使已经消费的页不能用旧游标再次推进。
func (m *cursorSessionManager) renewResourceDataCursor(session *interfaces.CursorSession) {
	nextCursor := uuid.NewV7().String()
	m.mu.Lock()
	delete(m.sessions, session.ID)
	session.ID = nextCursor
	m.sessions[nextCursor] = session
	m.mu.Unlock()
	m.markPageSuccess(session)
}

func (m *cursorSessionManager) removeLocked(cursor string) (*interfaces.CursorSession, bool) {
	session, ok := m.sessions[cursor]
	if !ok {
		return nil, false
	}
	delete(m.sessions, cursor)
	return session, true
}

func (m *cursorSessionManager) closeExpiredSessions(nowSec int64) {
	var expired []*interfaces.CursorSession
	m.mu.Lock()
	for id, session := range m.sessions {
		// A page execution holds the session lock. Do not remove a session until that
		// execution finishes, otherwise it can return an unresolvable cursor.
		if !session.TryLock() {
			continue
		}
		if nowSec >= atomic.LoadInt64(&session.ExpiresAtSec) {
			delete(m.sessions, id)
			expired = append(expired, session)
			logger.Debugf("Cursor session expired: catalog_id=%s, active_sessions=%d", session.CatalogID, len(m.sessions))
		}
		session.Unlock()
	}
	m.mu.Unlock()

	for _, session := range expired {
		closeSQLCursorResources(session)
	}
}

func cursorPagingResponse(session *interfaces.CursorSession) *interfaces.PagingResponse {
	nextCursor := session.ID
	expiresAt := atomic.LoadInt64(&session.ExpiresAtSec)
	return &interfaces.PagingResponse{NextCursor: &nextCursor, ExpiresAtSec: &expiresAt}
}
