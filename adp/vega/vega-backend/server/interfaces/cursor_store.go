package interfaces

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// CursorStore centralizes opaque cursor lifecycle. Payload is owned by the
// caller and must be protected by the caller when it is mutable.
type CursorStore struct {
	mu      sync.Mutex
	entries map[string]*CursorEntry
}

type CursorEntry struct {
	ID, AccountID string
	Payload       any
	ExpiresAtSec  int64
	KeepAliveSec  int
}

func NewCursorStore() *CursorStore { return &CursorStore{entries: make(map[string]*CursorEntry)} }

func (s *CursorStore) Create(accountID string, payload any, keepAliveSec int) *CursorEntry {
	if keepAliveSec == 0 {
		keepAliveSec = DefaultCursorKeepAliveSec
	}
	e := &CursorEntry{ID: uuid.NewString(), AccountID: accountID, Payload: payload, KeepAliveSec: keepAliveSec}
	s.Refresh(e)
	s.mu.Lock()
	s.entries[e.ID] = e
	s.mu.Unlock()
	return e
}

func (s *CursorStore) Get(id string) (*CursorEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok || time.Now().Unix() >= e.ExpiresAtSec {
		delete(s.entries, id)
		return nil, false
	}
	return e, true
}
func (s *CursorStore) Refresh(e *CursorEntry) {
	e.ExpiresAtSec = time.Now().Unix() + int64(e.KeepAliveSec)
}
func (s *CursorStore) Remove(id string) { s.mu.Lock(); delete(s.entries, id); s.mu.Unlock() }
