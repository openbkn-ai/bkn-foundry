// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"vega-backend/interfaces"
)

type cursorSession struct {
	mu sync.Mutex

	ID              string
	AccountID       string
	CatalogID       string
	ResourceIDs     []string
	CompiledSQL     string
	QueryFormat     interfaces.QueryFormat
	OpenSearchQuery map[string]any
	OpenSearchIndex string
	SearchAfter     []any
	PageSize        int
	KeepAliveSec    int
	QueryTimeoutSec int
	Offset          int
	ExpiresAtSec    int64
}

type cursorSessionManager struct {
	mu       sync.Mutex
	sessions map[string]*cursorSession
}

var rawQueryCursorSessions = &cursorSessionManager{sessions: make(map[string]*cursorSession)}

func init() {
	go rawQueryCursorSessions.reclaimExpired()
}

func (m *cursorSessionManager) create(accountID, catalogID string, resourceIDs []string, compiledSQL string, pageSize, keepAliveSec, queryTimeoutSec int) *cursorSession {
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
		ExpiresAtSec:    now + int64(keepAliveSec),
	}
	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()
	return session
}

// reclaimExpired bounds memory usage for cursors that are never continued.
// get also checks expiry, so the periodic reclaim is only best-effort cleanup.
func (m *cursorSessionManager) reclaimExpired() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for now := range ticker.C {
		m.mu.Lock()
		for id, session := range m.sessions {
			if now.Unix() >= session.ExpiresAtSec {
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
	}
}

func (m *cursorSessionManager) get(cursor string) (*cursorSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[cursor]
	if !ok {
		return nil, false
	}
	if time.Now().Unix() >= session.ExpiresAtSec {
		delete(m.sessions, cursor)
		return nil, false
	}
	return session, true
}

func (m *cursorSessionManager) remove(cursor string) {
	m.mu.Lock()
	delete(m.sessions, cursor)
	m.mu.Unlock()
}

func (s *cursorSession) refreshExpiry(now time.Time) {
	s.ExpiresAtSec = now.Unix() + int64(s.KeepAliveSec)
}

func cursorPagingResponse(session *cursorSession) *interfaces.PagingResponse {
	nextCursor := session.ID
	expiresAt := session.ExpiresAtSec
	return &interfaces.PagingResponse{NextCursor: &nextCursor, ExpiresAtSec: &expiresAt}
}
