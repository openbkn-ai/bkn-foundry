// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

type cursorSessionManager struct {
	mu                    sync.Mutex
	sessions              map[string]*interfaces.CursorSession
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
	go rawQueryCursorSessions.reclaimExpired()
}

func newCursorSessionManager(maxSessions, maxSessionsPerAccount int) *cursorSessionManager {
	m := &cursorSessionManager{sessions: make(map[string]*interfaces.CursorSession)}
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

func (m *cursorSessionManager) capacityErrorLocked(quotaOwnerID string) error {
	if len(m.sessions) >= m.maxSessions {
		return errCursorSessionLimitReached
	}
	count := 0
	for _, session := range m.sessions {
		if session.QuotaOwnerID == quotaOwnerID {
			count++
		}
	}
	if count >= m.maxSessionsPerAccount {
		return errCursorAccountLimitReached
	}
	return nil
}

func (m *cursorSessionManager) create(accountID, catalogID string, resourceIDs []string, limit, keepAliveSec, queryTimeoutSec int) (*interfaces.CursorSession, error) {
	if keepAliveSec == 0 {
		keepAliveSec = interfaces.DefaultCursorKeepAliveSec
	}
	now := time.Now().Unix()
	generatedID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate cursor session UUIDv7: %w", err)
	}
	session := &interfaces.CursorSession{
		ID:              generatedID.String(),
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
	m.mu.Lock()
	expired := m.removeExpiredLocked(time.Now().Unix())
	if err := m.capacityErrorLocked(accountID); err != nil {
		m.mu.Unlock()
		closeSQLCursorResources(expired...)
		return nil, err
	}
	m.sessions[session.ID] = session
	activeSessionsLen := len(m.sessions)
	m.mu.Unlock()
	closeSQLCursorResources(expired...)
	logger.Debugf("Cursor session created: catalog_id=%s, query_format=%s, active_sessions_len=%d", session.CatalogID, session.QueryFormat, activeSessionsLen)
	return session, nil
}

func (m *cursorSessionManager) createResourceData(accountID, quotaOwnerID, proxyCallerID string, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.CursorSession, error) {
	keepAliveSec := params.Paging.KeepAliveSec
	if keepAliveSec == 0 {
		keepAliveSec = interfaces.DefaultCursorKeepAliveSec
	}
	now := time.Now().Unix()
	generatedID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate cursor session UUIDv7: %w", err)
	}
	session := &interfaces.CursorSession{
		ID:                        generatedID.String(),
		AccountID:                 accountID,
		QuotaOwnerID:              quotaOwnerID,
		ResourceDataProxyCallerID: proxyCallerID,
		CatalogID:                 resource.CatalogID,
		ResourceIDs:               []string{resource.ID},
		ResourceDataResourceID:    resource.ID,
		ResourceDataUpdateTime:    resource.UpdateTime,
		ResourceDataParams:        cloneResourceDataQueryParams(params),
		ResourceDataCategory:      resource.Category,
		PageLimit:                 params.Paging.Limit,
		KeepAliveSec:              keepAliveSec,
		CreatedAtSec:              now,
		ExpiresAtSec:              now + int64(keepAliveSec),
	}
	m.mu.Lock()
	expired := m.removeExpiredLocked(now)
	if err := m.capacityErrorLocked(quotaOwnerID); err != nil {
		m.mu.Unlock()
		closeSQLCursorResources(expired...)
		return nil, err
	}
	m.sessions[session.ID] = session
	activeSessionsLen := len(m.sessions)
	m.mu.Unlock()
	closeSQLCursorResources(expired...)
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

// reclaimExpired bounds memory usage for cursors that are never continued.
// get also checks expiry, so the periodic reclaim is only best-effort cleanup.
func (m *cursorSessionManager) reclaimExpired() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for now := range ticker.C {
		m.mu.Lock()
		expired := m.removeExpiredLocked(now.Unix())
		m.mu.Unlock()
		closeSQLCursorResources(expired...)
	}
}

// acquire returns a session only when this request obtains exclusive ownership
// immediately. The session remains registered so it still counts toward the
// global limit and remains visible to the expiry reclaimer while in use.
func (m *cursorSessionManager) acquire(cursor string) (*interfaces.CursorSession, bool) {
	m.mu.Lock()
	session, ok := m.sessions[cursor]
	if !ok || !session.TryLock() {
		m.mu.Unlock()
		return nil, false
	}
	if time.Now().Unix() >= atomic.LoadInt64(&session.ExpiresAtSec) {
		m.removeLocked(cursor)
		activeSessions := len(m.sessions)
		m.mu.Unlock()
		closeSQLCursorResources(session)
		session.Unlock()
		logger.Debugf("Cursor session expired: catalog_id=%s, active_sessions=%d", session.CatalogID, activeSessions)
		return nil, false
	}
	m.mu.Unlock()
	return session, true
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
	return m.closeForAccountResource(accountID, "", "", cursor)
}

func (m *cursorSessionManager) closeForAccountResource(accountID, callerID, resourceID, cursor string) error {
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
	// A proxied cursor must be closed through the proxy endpoint bound to its original caller and resource.
	if resourceID == "" && session.ResourceDataProxyCallerID != "" {
		m.mu.Unlock()
		return errCursorSessionNotFound
	}
	if resourceID != "" && (session.ResourceDataResourceID != resourceID ||
		session.ResourceDataProxyCallerID == "" || session.ResourceDataProxyCallerID != callerID) {
		m.mu.Unlock()
		return errCursorSessionNotFound
	}
	if !session.TryLock() {
		m.mu.Unlock()
		return errCursorSessionBusy
	}
	defer session.Unlock()
	expired := time.Now().Unix() >= atomic.LoadInt64(&session.ExpiresAtSec)
	m.removeLocked(cursor)
	m.mu.Unlock()
	closeSQLCursorResources(session)
	if expired {
		return errCursorSessionNotFound
	}
	return nil
}

// closeSQLCursorResources 在移除会话后调用；网络资源清理不持有会话管理器的全局锁。
func closeSQLCursorResources(sessions ...*interfaces.CursorSession) {
	for _, session := range sessions {
		if session.SQLCancel != nil {
			session.SQLCancel()
		}
		if session.SQLRows != nil {
			_ = session.SQLRows.Close()
		}
		if session.SQLConnector != nil {
			_ = session.SQLConnector.Close(context.Background())
		}
	}
}

// CloseCursorSession releases a cursor owned by the calling account.
func CloseCursorSession(ctx context.Context, accountID, cursor string) error {
	return cursorSessionCloseError(ctx, rawQueryCursorSessions.closeForAccount(accountID, cursor))
}

// CloseResourceDataCursorSession only releases a cursor for its bound managed
// account, original caller and resource.
func CloseResourceDataCursorSession(ctx context.Context, accountID, callerID, resourceID, cursor string) error {
	return cursorSessionCloseError(ctx, rawQueryCursorSessions.closeForAccountResource(accountID, callerID, resourceID, cursor))
}

func cursorSessionCloseError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, errCursorSessionNotFound):
		return cursorNotFoundError(ctx)
	case errors.Is(err, errCursorSessionForbidden):
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor does not belong to the current account")
	case errors.Is(err, errCursorSessionBusy):
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_CursorInUse).
			WithErrorDetails("cursor is in use, please retry later")
	default:
		return err
	}
}

func (m *cursorSessionManager) markPageSuccess(session *interfaces.CursorSession) {
	now := time.Now().Unix()
	session.LastSuccessfulPageAtSec = now
	atomic.StoreInt64(&session.ExpiresAtSec, now+int64(session.KeepAliveSec))
}

func (m *cursorSessionManager) removeLocked(cursor string) (*interfaces.CursorSession, bool) {
	session, ok := m.sessions[cursor]
	if !ok {
		return nil, false
	}
	delete(m.sessions, cursor)
	return session, true
}

func (m *cursorSessionManager) removeExpiredLocked(nowSec int64) []*interfaces.CursorSession {
	var removed []*interfaces.CursorSession
	for id, session := range m.sessions {
		// A page execution holds the session lock. Do not remove a session until that
		// execution finishes, otherwise it can return an unresolvable cursor.
		if !session.TryLock() {
			continue
		}
		expired := nowSec >= atomic.LoadInt64(&session.ExpiresAtSec)
		session.Unlock()
		if expired {
			m.removeLocked(id)
			removed = append(removed, session)
			logger.Debugf("Cursor session expired: catalog_id=%s, active_sessions=%d", session.CatalogID, len(m.sessions))
		}
	}
	return removed
}

func cursorPagingResponse(session *interfaces.CursorSession) *interfaces.PagingResponse {
	nextCursor := session.ID
	expiresAt := atomic.LoadInt64(&session.ExpiresAtSec)
	return &interfaces.PagingResponse{NextCursor: &nextCursor, ExpiresAtSec: &expiresAt}
}
