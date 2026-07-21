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

	got, ok := manager.get(session.ID)
	require.True(t, ok)
	assert.Equal(t, session, got)
	assert.Positive(t, session.CreatedAtSec)
	assert.Zero(t, session.LastSuccessfulPageAtSec)

	response := cursorPagingResponse(session)
	require.NotNil(t, response.NextCursor)
	require.NotNil(t, response.ExpiresAtSec)
	assert.Equal(t, session.ID, *response.NextCursor)
	assert.Equal(t, session.ExpiresAtSec, *response.ExpiresAtSec)

	session.ExpiresAtSec = time.Now().Add(-time.Second).Unix()
	_, ok = manager.get(session.ID)
	assert.False(t, ok)
}

func TestCursorSessionManagerRejectsNewSessionAtCapacity(t *testing.T) {
	manager := newCursorSessionManager(2)
	first, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
	require.NoError(t, err)
	second, err := manager.create("account-1", "catalog-1", nil, "SELECT 2", 1, 60, 30)
	require.NoError(t, err)

	_, ok := manager.get(first.ID)
	require.True(t, ok)
	third, err := manager.create("account-1", "catalog-1", nil, "SELECT 3", 1, 60, 30)
	require.ErrorIs(t, err, errCursorSessionLimitReached)
	assert.Nil(t, third)

	_, ok = manager.get(second.ID)
	assert.True(t, ok)
	got, ok := manager.get(first.ID)
	require.True(t, ok)
	assert.Equal(t, first, got)
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

	got, ok := manager.get(first.ID)
	require.True(t, ok)
	assert.Equal(t, first, got)
	got, ok = manager.get(second.ID)
	require.True(t, ok)
	assert.Equal(t, second, got)
	got, ok = manager.get(third.ID)
	require.True(t, ok)
	assert.Equal(t, third, got)
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
	_, ok := manager.get(session.ID)
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
	_, ok = manager.get(session.ID)
	assert.False(t, ok)
}

func TestCursorSessionManagerGetKeepsActiveExpiredSession(t *testing.T) {
	manager := newCursorSessionManager(2)
	session, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
	require.NoError(t, err)
	t.Cleanup(func() { manager.remove(session.ID) })
	atomic.StoreInt64(&session.ExpiresAtSec, time.Now().Add(-time.Second).Unix())

	session.Lock()
	defer session.Unlock()
	got, ok := manager.get(session.ID)

	assert.True(t, ok)
	assert.Equal(t, session, got)
}
