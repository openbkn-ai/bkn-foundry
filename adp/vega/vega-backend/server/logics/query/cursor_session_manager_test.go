// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorSessionManagerExpiryAndResponse(t *testing.T) {
	manager := newCursorSessionManager(10)
	session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "SELECT 1", 100, 60, 30)
	require.NoError(t, err)
	t.Cleanup(func() { manager.remove(session.ID) })

	got, ok := manager.acquire(session.ID)
	require.True(t, ok)
	assert.Equal(t, session, got)
	manager.release(got)
	assert.Positive(t, session.CreatedAtSec)
	assert.Zero(t, session.LastSuccessfulPageAtSec)

	response := cursorPagingResponse(session)
	require.NotNil(t, response.NextCursor)
	require.NotNil(t, response.ExpiresAtSec)
	assert.Equal(t, session.ID, *response.NextCursor)
	assert.Equal(t, session.ExpiresAtSec, *response.ExpiresAtSec)

	session.ExpiresAtSec = time.Now().Add(-time.Second).Unix()
	_, ok = manager.acquire(session.ID)
	assert.False(t, ok)
}

func TestCursorSessionManagerRejectsNewSessionAtCapacity(t *testing.T) {
	manager := newCursorSessionManager(2)
	first, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
	require.NoError(t, err)
	second, err := manager.create("account-1", "catalog-1", nil, "SELECT 2", 1, 60, 30)
	require.NoError(t, err)

	got, ok := manager.acquire(first.ID)
	require.True(t, ok)
	manager.release(got)
	third, err := manager.create("account-1", "catalog-1", nil, "SELECT 3", 1, 60, 30)
	require.ErrorIs(t, err, errCursorSessionLimitReached)
	assert.Nil(t, third)

	got, ok = manager.acquire(second.ID)
	assert.True(t, ok)
	manager.release(got)
	got, ok = manager.acquire(first.ID)
	require.True(t, ok)
	assert.Equal(t, first, got)
	manager.release(got)
}

func TestCursorSessionManagerRejectsNewSessionAfterCapacityIsReduced(t *testing.T) {
	manager := newCursorSessionManager(3)
	first, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
	require.NoError(t, err)
	second, err := manager.create("account-1", "catalog-1", nil, "SELECT 2", 1, 60, 30)
	require.NoError(t, err)
	third, err := manager.create("account-1", "catalog-1", nil, "SELECT 3", 1, 60, 30)
	require.NoError(t, err)

	manager.configure(1)
	fourth, err := manager.create("account-1", "catalog-1", nil, "SELECT 4", 1, 60, 30)
	require.ErrorIs(t, err, errCursorSessionLimitReached)
	assert.Nil(t, fourth)

	got, ok := manager.acquire(first.ID)
	require.True(t, ok)
	assert.Equal(t, first, got)
	manager.release(got)
	got, ok = manager.acquire(second.ID)
	require.True(t, ok)
	assert.Equal(t, second, got)
	manager.release(got)
	got, ok = manager.acquire(third.ID)
	require.True(t, ok)
	assert.Equal(t, third, got)
	manager.release(got)
}

func TestCursorSessionLifecycleTracksSuccessfulPagesAndClosure(t *testing.T) {
	manager := newCursorSessionManager(2)
	session, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
	require.NoError(t, err)

	before := time.Now().Unix()
	manager.markPageSuccess(session)
	assert.GreaterOrEqual(t, session.LastSuccessfulPageAtSec, before)
	assert.Equal(t, session.LastSuccessfulPageAtSec+60, session.ExpiresAtSec)

	manager.closeSession(session.ID)
	_, ok := manager.acquire(session.ID)
	assert.False(t, ok)
}

func TestCursorSessionManagerReclaimerSkipsActiveSession(t *testing.T) {
	manager := newCursorSessionManager(2)
	session, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
	require.NoError(t, err)
	session.ExpiresAtSec = time.Now().Add(-time.Second).Unix()

	session.Lock()
	manager.mu.Lock()
	manager.removeExpiredLocked(time.Now().Unix())
	manager.mu.Unlock()
	_, ok := manager.sessions[session.ID]
	assert.True(t, ok)
	session.Unlock()

	manager.mu.Lock()
	manager.removeExpiredLocked(time.Now().Unix())
	manager.mu.Unlock()
	_, ok = manager.acquire(session.ID)
	assert.False(t, ok)
}

func TestCursorSessionManagerAcquireRejectsActiveExpiredSessionWithoutRemovingIt(t *testing.T) {
	manager := newCursorSessionManager(2)
	session, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
	require.NoError(t, err)
	t.Cleanup(func() { manager.remove(session.ID) })
	atomic.StoreInt64(&session.ExpiresAtSec, time.Now().Add(-time.Second).Unix())

	session.Lock()
	_, ok := manager.acquire(session.ID)

	assert.False(t, ok)
	manager.mu.Lock()
	_, retained := manager.sessions[session.ID]
	manager.mu.Unlock()
	assert.True(t, retained)
	session.Unlock()
}

func TestCursorSessionManagerAcquireIsExclusive(t *testing.T) {
	manager := newCursorSessionManager(2)
	session, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
	require.NoError(t, err)
	t.Cleanup(func() { manager.remove(session.ID) })

	acquired, ok := manager.acquire(session.ID)
	require.True(t, ok)
	assert.Equal(t, session, acquired)

	_, ok = manager.acquire(session.ID)
	assert.False(t, ok)

	manager.release(acquired)
	reacquired, ok := manager.acquire(session.ID)
	require.True(t, ok)
	assert.Equal(t, session, reacquired)
	manager.release(reacquired)
}
