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

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-comm-go/logger"

	"vega-backend/interfaces"
)

type cursorSession struct {
	mu sync.Mutex

	ID          string
	AccountID   string
	CatalogID   string
	ResourceIDs []string
	CompiledSQL string

	QueryFormat     interfaces.QueryFormat
	OpenSearchQuery map[string]any
	OpenSearchIndex string
	SearchAfter     []any

	Offset   int
	PageSize int

	KeepAliveSec    int
	QueryTimeoutSec int

	CreatedAtSec            int64
	LastSuccessfulPageAtSec int64
	ExpiresAtSec            int64
}

type cursorSessionManager struct {
	mu          sync.Mutex
	sessions    map[string]*cursorSession
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
		sessions:    make(map[string]*cursorSession),
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

func (m *cursorSessionManager) create(accountID, catalogID string, resourceIDs []string, compiledSQL string, pageSize, keepAliveSec, queryTimeoutSec int) (*cursorSession, error) {
	if keepAliveSec == 0 {
		keepAliveSec = interfaces.DefaultCursorKeepAliveSec
	}
	now := time.Now().Unix()
	session := &cursorSession{
		ID:              uuid.NewString(),
		AccountID:       accountID,
		CatalogID:       catalogID,
		ResourceIDs:     append([]string(nil), resourceIDs...),
		CompiledSQL:     compiledSQL,
		QueryFormat:     interfaces.QueryFormatSQL,
		PageSize:        pageSize,
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

func (m *cursorSessionManager) get(cursor string) (*cursorSession, bool) {
	m.mu.Lock()
	session, ok := m.sessions[cursor]
	if !ok {
		m.mu.Unlock()
		return nil, false
	}
	if time.Now().Unix() >= atomic.LoadInt64(&session.ExpiresAtSec) {
		expiredSession, _ := m.removeLocked(cursor)
		activeSessions := len(m.sessions)
		m.mu.Unlock()
		logger.Infof("Cursor session expired: catalog_id=%s, active_sessions=%d", expiredSession.CatalogID, activeSessions)
		return nil, false
	}
	m.mu.Unlock()
	return session, true
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

func (m *cursorSessionManager) expire(cursor string) {
	m.mu.Lock()
	session, ok := m.removeLocked(cursor)
	activeSessions := len(m.sessions)
	m.mu.Unlock()
	if ok {
		logger.Infof("Cursor session expired: catalog_id=%s, active_sessions=%d", session.CatalogID, activeSessions)
	}
}

func (m *cursorSessionManager) markPageSuccess(session *cursorSession) {
	now := time.Now().Unix()
	session.LastSuccessfulPageAtSec = now
	atomic.StoreInt64(&session.ExpiresAtSec, now+int64(session.KeepAliveSec))
}

func (m *cursorSessionManager) removeLocked(cursor string) (*cursorSession, bool) {
	session, ok := m.sessions[cursor]
	if !ok {
		return nil, false
	}
	delete(m.sessions, cursor)
	return session, true
}

func (m *cursorSessionManager) removeExpiredLocked(nowSec int64) {
	for id, session := range m.sessions {
		if nowSec >= atomic.LoadInt64(&session.ExpiresAtSec) {
			m.removeLocked(id)
			logger.Infof("Cursor session expired: catalog_id=%s, active_sessions=%d", session.CatalogID, len(m.sessions))
		}
	}
}

func cursorPagingResponse(session *cursorSession) *interfaces.PagingResponse {
	nextCursor := session.ID
	expiresAt := atomic.LoadInt64(&session.ExpiresAtSec)
	return &interfaces.PagingResponse{NextCursor: &nextCursor, ExpiresAtSec: &expiresAt}
}
