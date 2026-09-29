// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestCursorSessionManagerAcquire(t *testing.T) {
	t.Run("rejects expired session", func(t *testing.T) {
		manager := newCursorSessionManager(10, 9)
		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "SELECT 1", 100, 60, 30)
		require.NoError(t, err)
		t.Cleanup(func() { manager.remove(session.ID) })

		got, ok := manager.acquire(session.ID)
		require.True(t, ok)
		assert.Equal(t, session, got)
		manager.release(got)
		assert.Positive(t, session.CreatedAtSec)
		assert.Zero(t, session.LastSuccessfulPageAtSec)

		session.ExpiresAtSec = time.Now().Add(-time.Second).Unix()
		_, ok = manager.acquire(session.ID)
		assert.False(t, ok)
	})

	t.Run("retains active expired session", func(t *testing.T) {
		manager := newCursorSessionManager(2, 1)
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
	})

	t.Run("is exclusive", func(t *testing.T) {
		manager := newCursorSessionManager(2, 1)
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
	})
}

func TestCursorSessionManagerCreate(t *testing.T) {
	t.Run("rejects new session at capacity", func(t *testing.T) {
		manager := newCursorSessionManager(2, 1)
		first, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
		require.NoError(t, err)
		second, err := manager.create("account-2", "catalog-1", nil, "SELECT 2", 1, 60, 30)
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
	})
	t.Run("one account cannot exhaust another account's share", func(t *testing.T) {
		manager := newCursorSessionManager(3, 2)
		first, err := manager.create("account-a", "catalog-1", nil, "SELECT 1", 1, 60, 30)
		require.NoError(t, err)
		_, err = manager.create("account-a", "catalog-1", nil, "SELECT 2", 1, 60, 30)
		require.NoError(t, err)
		_, err = manager.create("account-a", "catalog-1", nil, "SELECT 3", 1, 60, 30)
		require.ErrorIs(t, err, errCursorAccountLimitReached)
		_, err = manager.create("account-b", "catalog-1", nil, "SELECT 4", 1, 60, 30)
		require.NoError(t, err)
		_, err = manager.create("account-b", "catalog-1", nil, "SELECT 5", 1, 60, 30)
		require.ErrorIs(t, err, errCursorSessionLimitReached)
		manager.remove(first.ID)
		_, err = manager.create("account-a", "catalog-1", nil, "SELECT 6", 1, 60, 30)
		require.NoError(t, err)
	})
	t.Run("raw query and resource data share the account quota", func(t *testing.T) {
		manager := newCursorSessionManager(4, 1)
		_, err := manager.create("account-a", "catalog-1", nil, "SELECT 1", 1, 60, 30)
		require.NoError(t, err)
		resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1"}
		params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 1}}
		_, err = manager.createResourceData("account-a", resource, params)
		require.ErrorIs(t, err, errCursorAccountLimitReached)
		_, err = manager.createResourceData("account-b", resource, params)
		assert.NoError(t, err)
	})
	t.Run("concurrent requests cannot exceed the account quota", func(t *testing.T) {
		manager := newCursorSessionManager(20, 2)
		var workers sync.WaitGroup
		results := make(chan error, 10)
		for i := 0; i < 10; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				_, err := manager.create("account-a", "catalog-1", nil, "SELECT 1", 1, 60, 30)
				results <- err
			}()
		}
		workers.Wait()
		close(results)
		created := 0
		for err := range results {
			if err == nil {
				created++
			} else {
				assert.ErrorIs(t, err, errCursorAccountLimitReached)
			}
		}
		assert.Equal(t, 2, created)
		_, err := manager.create("account-b", "catalog-1", nil, "SELECT 2", 1, 60, 30)
		assert.NoError(t, err)
	})
}

func TestCursorSessionManagerConfigure(t *testing.T) {
	t.Run("reduced capacity applies to new sessions", func(t *testing.T) {
		manager := newCursorSessionManager(3, 2)
		first, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
		require.NoError(t, err)
		second, err := manager.create("account-1", "catalog-1", nil, "SELECT 2", 1, 60, 30)
		require.NoError(t, err)
		third, err := manager.create("account-2", "catalog-1", nil, "SELECT 3", 1, 60, 30)
		require.NoError(t, err)

		manager.configure(1, 1)
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
	})
}

func TestCursorSessionManagerMarkPageSuccess(t *testing.T) {
	t.Run("refreshes successful page and expiry timestamps", func(t *testing.T) {
		manager := newCursorSessionManager(2, 1)
		session, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
		require.NoError(t, err)

		before := time.Now().Unix()
		manager.markPageSuccess(session)
		assert.GreaterOrEqual(t, session.LastSuccessfulPageAtSec, before)
		assert.Equal(t, session.LastSuccessfulPageAtSec+60, session.ExpiresAtSec)
	})
}

func TestCursorSessionManagerCloseSession(t *testing.T) {
	t.Run("removes the session", func(t *testing.T) {
		manager := newCursorSessionManager(2, 1)
		session, err := manager.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 30)
		require.NoError(t, err)

		manager.closeSession(session.ID)
		_, ok := manager.acquire(session.ID)
		assert.False(t, ok)
	})
}

func TestCursorSessionManagerCloseForAccount(t *testing.T) {
	manager := newCursorSessionManager(2, 1)
	session, err := manager.create("account-a", "catalog-1", nil, "SELECT 1", 1, 60, 30)
	require.NoError(t, err)
	assert.ErrorIs(t, manager.closeForAccount("account-b", session.ID), errCursorSessionForbidden)
	assert.ErrorIs(t, manager.closeForAccount("account-a", "missing"), errCursorSessionNotFound)
	session.Lock()
	assert.ErrorIs(t, manager.closeForAccount("account-a", session.ID), errCursorSessionBusy)
	session.Unlock()
	assert.NoError(t, manager.closeForAccount("account-a", session.ID))
	assert.ErrorIs(t, manager.closeForAccount("account-a", session.ID), errCursorSessionNotFound)
	_, err = manager.create("account-b", "catalog-1", nil, "SELECT 2", 1, 60, 30)
	assert.NoError(t, err)
}

func TestCloseCursorSession(t *testing.T) {
	previousManager := rawQueryCursorSessions
	manager := newCursorSessionManager(2, 1)
	rawQueryCursorSessions = manager
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })
	session, err := manager.create("account-a", "catalog-1", nil, "SELECT 1", 1, 60, 30)
	require.NoError(t, err)

	err = CloseCursorSession(context.Background(), "account-b", session.ID)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)

	session.Lock()
	err = CloseCursorSession(context.Background(), "account-a", session.ID)
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Query_CursorInUse, httpErr.BaseError.ErrorCode)
	session.Unlock()

	require.NoError(t, CloseCursorSession(context.Background(), "account-a", session.ID))
	err = CloseCursorSession(context.Background(), "account-a", session.ID)
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusNotFound, httpErr.HTTPCode)
}

func TestCursorSessionManagerRemoveExpiredLocked(t *testing.T) {
	t.Run("skips active session", func(t *testing.T) {
		manager := newCursorSessionManager(2, 1)
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
	})
}

func TestCursorPagingResponse(t *testing.T) {
	t.Run("returns cursor and expiry", func(t *testing.T) {
		manager := newCursorSessionManager(10, 9)
		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "SELECT 1", 100, 60, 30)
		require.NoError(t, err)
		t.Cleanup(func() { manager.remove(session.ID) })

		response := cursorPagingResponse(session)

		require.NotNil(t, response.NextCursor)
		require.NotNil(t, response.ExpiresAtSec)
		assert.Equal(t, session.ID, *response.NextCursor)
		assert.Equal(t, session.ExpiresAtSec, *response.ExpiresAtSec)
	})
}
