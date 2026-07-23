// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/rs/xid"

	"vega-backend/interfaces"
)

type cursorSessionManager struct {
	mu          sync.Mutex
	sessions    map[string]*interfaces.CursorSession
	maxSessions int
}

const defaultCursorMaxSessions = 1000

var errCursorSessionLimitReached = errors.New("cursor session limit reached")

var rawQueryCursorSessions = newCursorSessionManager(defaultCursorMaxSessions)

func init() {
	go rawQueryCursorSessions.reclaimExpired()
}

func newCursorSessionManager(maxSessions int) *cursorSessionManager {
	if maxSessions <= 0 {
		maxSessions = defaultCursorMaxSessions
	}
	return &cursorSessionManager{
		sessions:    make(map[string]*interfaces.CursorSession),
		maxSessions: maxSessions,
	}
}

func (m *cursorSessionManager) configure(maxSessions int) {
	if maxSessions <= 0 {
		maxSessions = defaultCursorMaxSessions
	}
	m.mu.Lock()
	m.maxSessions = maxSessions
	m.mu.Unlock()
}

func (m *cursorSessionManager) create(accountID, catalogID string, resourceIDs []string, compiledSQL string, limit, keepAliveSec, queryTimeoutSec int) (*interfaces.CursorSession, error) {
	if keepAliveSec == 0 {
		keepAliveSec = interfaces.DefaultCursorKeepAliveSec
	}
	now := time.Now().Unix()
	session := &interfaces.CursorSession{
		ID:              xid.New().String(),
		AccountID:       accountID,
		CatalogID:       catalogID,
		ResourceIDs:     append([]string(nil), resourceIDs...),
		CompiledSQL:     compiledSQL,
		QueryFormat:     interfaces.QueryFormatSQL,
		Limit:           limit,
		KeepAliveSec:    keepAliveSec,
		QueryTimeoutSec: queryTimeoutSec,
		CreatedAtSec:    now,
		ExpiresAtSec:    now + int64(keepAliveSec),
	}
	m.mu.Lock()
	m.removeExpiredLocked(time.Now().Unix())
	if len(m.sessions) >= m.maxSessions {
		m.mu.Unlock()
		return nil, errCursorSessionLimitReached
	}
	m.sessions[session.ID] = session
	activeSessionsLen := len(m.sessions)
	m.mu.Unlock()
	logger.Infof("Cursor session created: catalog_id=%s, query_format=%s, active_sessions_len=%d", session.CatalogID, session.QueryFormat, activeSessionsLen)
	return session, nil
}

func (m *cursorSessionManager) createResourceData(accountID string, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.CursorSession, error) {
	keepAliveSec := params.Paging.KeepAliveSec
	if keepAliveSec == 0 {
		keepAliveSec = interfaces.DefaultCursorKeepAliveSec
	}
	now := time.Now().Unix()
	session := &interfaces.CursorSession{
		ID:                     xid.New().String(),
		AccountID:              accountID,
		CatalogID:              resource.CatalogID,
		ResourceIDs:            []string{resource.ID},
		ResourceDataResourceID: resource.ID,
		ResourceDataUpdateTime: resource.UpdateTime,
		ResourceDataParams:     cloneResourceDataQueryParams(params),
		ResourceDataCategory:   resource.Category,
		Limit:                  params.Paging.Limit,
		KeepAliveSec:           keepAliveSec,
		CreatedAtSec:           now,
		ExpiresAtSec:           now + int64(keepAliveSec),
	}
	m.mu.Lock()
	m.removeExpiredLocked(now)
	if len(m.sessions) >= m.maxSessions {
		m.mu.Unlock()
		return nil, errCursorSessionLimitReached
	}
	m.sessions[session.ID] = session
	activeSessionsLen := len(m.sessions)
	m.mu.Unlock()
	logger.Infof("Cursor session created: catalog_id=%s, query_format=resource_data, active_sessions_len=%d", session.CatalogID, activeSessionsLen)
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
		m.removeExpiredLocked(now.Unix())
		m.mu.Unlock()
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
		session.Unlock()
		activeSessions := len(m.sessions)
		m.mu.Unlock()
		logger.Infof("Cursor session expired: catalog_id=%s, active_sessions=%d", session.CatalogID, activeSessions)
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
	m.removeLocked(cursor)
	m.mu.Unlock()
}

func (m *cursorSessionManager) closeSession(cursor string) {
	m.mu.Lock()
	session, ok := m.removeLocked(cursor)
	activeSessions := len(m.sessions)
	m.mu.Unlock()
	if ok {
		logger.Infof("Cursor session closed at final page: catalog_id=%s, active_sessions=%d", session.CatalogID, activeSessions)
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

func (m *cursorSessionManager) removeExpiredLocked(nowSec int64) {
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
			logger.Infof("Cursor session expired: catalog_id=%s, active_sessions=%d", session.CatalogID, len(m.sessions))
		}
	}
}

func cursorPagingResponse(session *interfaces.CursorSession) *interfaces.PagingResponse {
	nextCursor := session.ID
	expiresAt := atomic.LoadInt64(&session.ExpiresAtSec)
	return &interfaces.PagingResponse{NextCursor: &nextCursor, ExpiresAtSec: &expiresAt}
}
