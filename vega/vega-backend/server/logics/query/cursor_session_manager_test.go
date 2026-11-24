// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"database/sql"
	"github.com/DATA-DOG/go-sqlmock"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"go.uber.org/mock/gomock"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

func TestCursorSessionManagerAcquire(t *testing.T) {
	t.Run("rejects expired session", func(t *testing.T) {
		manager := newCursorSessionManager(10, 9)
		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, 100, 60, 30)
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
		session, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
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
		session, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
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
		first, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
		second, err := manager.create("account-2", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)

		got, ok := manager.acquire(first.ID)
		require.True(t, ok)
		manager.release(got)
		third, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
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
	t.Run("expired session counts toward capacity until reclaimed", func(t *testing.T) {
		manager := newCursorSessionManager(1, 1)
		session, err := manager.create("account", "catalog", nil, 1, 60, 30)
		require.NoError(t, err)
		atomic.StoreInt64(&session.ExpiresAtSec, 0)

		_, err = manager.create("account", "catalog", nil, 1, 60, 30)
		require.ErrorIs(t, err, errCursorSessionLimitReached)
		assert.Contains(t, manager.sessions, session.ID)
	})
	t.Run("one account cannot exhaust another account's share", func(t *testing.T) {
		manager := newCursorSessionManager(3, 2)
		first, err := manager.create("account-a", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
		_, err = manager.create("account-a", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
		_, err = manager.create("account-a", "catalog-1", nil, 1, 60, 30)
		require.ErrorIs(t, err, errCursorAccountLimitReached)
		_, err = manager.create("account-b", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
		_, err = manager.create("account-b", "catalog-1", nil, 1, 60, 30)
		require.ErrorIs(t, err, errCursorSessionLimitReached)
		manager.remove(first.ID)
		_, err = manager.create("account-a", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
	})
	t.Run("raw query and resource data share the account quota", func(t *testing.T) {
		manager := newCursorSessionManager(4, 1)
		_, err := manager.create("account-a", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
		resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1"}
		params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 1}}
		_, err = manager.createResourceData("account-a", "account-a", resource, params)
		require.ErrorIs(t, err, errCursorAccountLimitReached)
		_, err = manager.createResourceData("account-b", "account-b", resource, params)
		assert.NoError(t, err)
	})
	t.Run("resource data uses the caller quota while retaining the access account", func(t *testing.T) {
		manager := newCursorSessionManager(4, 1)
		resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1"}
		params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 1}}
		first, err := manager.createResourceData("admin", "caller-a", resource, params)
		require.NoError(t, err)
		assert.Equal(t, "admin", first.AccountID)
		_, err = manager.createResourceData("admin", "caller-a", resource, params)
		require.ErrorIs(t, err, errCursorAccountLimitReached)
		second, err := manager.createResourceData("admin", "caller-b", resource, params)
		require.NoError(t, err)
		assert.Equal(t, "admin", second.AccountID)
		assert.ErrorIs(t, manager.closeForAccount("caller-b", first.ID), errCursorSessionForbidden)
	})
	t.Run("concurrent requests cannot exceed the account quota", func(t *testing.T) {
		manager := newCursorSessionManager(20, 2)
		var workers sync.WaitGroup
		results := make(chan error, 10)
		for i := 0; i < 10; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				_, err := manager.create("account-a", "catalog-1", nil, 1, 60, 30)
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
		_, err := manager.create("account-b", "catalog-1", nil, 1, 60, 30)
		assert.NoError(t, err)
	})
}

func TestCursorSessionManagerConfigure(t *testing.T) {
	t.Run("clamps account limit below global limit", func(t *testing.T) {
		manager := newCursorSessionManager(3, 5)
		_, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
		_, err = manager.create("account-1", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
		_, err = manager.create("account-1", "catalog-1", nil, 1, 60, 30)
		require.ErrorIs(t, err, errCursorAccountLimitReached)
		_, err = manager.create("account-2", "catalog-1", nil, 1, 60, 30)
		assert.NoError(t, err)
	})
	t.Run("reduced capacity applies to new sessions", func(t *testing.T) {
		manager := newCursorSessionManager(3, 2)
		first, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
		second, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
		third, err := manager.create("account-2", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)

		manager.configure(1, 1)
		fourth, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
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

func TestCursorCreationDoesNotWaitForExpiredSessionClose(t *testing.T) {
	manager := newCursorSessionManager(1, 1)
	session, err := manager.create("account", "catalog", nil, 1, 60, 30)
	require.NoError(t, err)
	stream := vmock.NewMockQueryPageSession(gomock.NewController(t))
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	closed := make(chan struct{})
	resume := sync.OnceFunc(func() { close(releaseClose) })
	stream.EXPECT().Close().DoAndReturn(func() error {
		close(closeStarted)
		<-releaseClose
		close(closed)
		return nil
	})
	session.ReadSession = stream
	atomic.StoreInt64(&session.ExpiresAtSec, 0)
	ctx, cancel := context.WithCancel(t.Context())
	reclaimerDone := make(chan struct{})
	go func() {
		defer close(reclaimerDone)
		manager.reclaimExpired(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		resume()
		<-reclaimerDone
	})

	select {
	case <-closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("quota pressure did not wake up the reclaimer")
	}
	created := make(chan error, 1)
	go func() {
		_, err := manager.create("account", "catalog", nil, 1, 60, 30)
		created <- err
	}()
	select {
	case err := <-created:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cursor creation waited for an expired snapshot to close")
	}
	resume()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("expired snapshot was never closed")
	}
}

func TestCursorSessionManagerReclaimThresholds(t *testing.T) {
	t.Run("global threshold rounds up and repeated requests coalesce", func(t *testing.T) {
		manager := newCursorSessionManager(6, 5)
		for i := range 4 {
			account := "a"
			if i%2 != 0 {
				account = "b"
			}
			_, err := manager.create(account, "catalog", nil, 1, 60, 30)
			require.NoError(t, err)
		}
		require.Empty(t, manager.reclaimRequested)

		_, err := manager.create("a", "catalog", nil, 1, 60, 30)
		require.NoError(t, err)
		require.Len(t, manager.reclaimRequested, 1)
		_, err = manager.create("b", "catalog", nil, 1, 60, 30)
		require.NoError(t, err)
		require.Len(t, manager.reclaimRequested, 1)

		<-manager.reclaimRequested
		_, err = manager.create("b", "catalog", nil, 1, 60, 30)
		require.ErrorIs(t, err, errCursorSessionLimitReached)
		assert.Len(t, manager.reclaimRequested, 1)
	})

	t.Run("account threshold combines raw query and resource-data cursors", func(t *testing.T) {
		manager := newCursorSessionManager(20, 5)
		for range 3 {
			_, err := manager.create("caller", "catalog", nil, 1, 60, 30)
			require.NoError(t, err)
		}
		require.Empty(t, manager.reclaimRequested)

		resource := &interfaces.Resource{ID: "resource", CatalogID: "catalog"}
		params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 1}}
		_, err := manager.createResourceData("admin", "caller", resource, params)
		require.NoError(t, err)
		require.Len(t, manager.reclaimRequested, 1)

		_, err = manager.createResourceData("admin", "caller", resource, params)
		require.NoError(t, err)
		require.Len(t, manager.reclaimRequested, 1)
		<-manager.reclaimRequested
		_, err = manager.createResourceData("admin", "caller", resource, params)
		require.ErrorIs(t, err, errCursorAccountLimitReached)
		assert.Len(t, manager.reclaimRequested, 1)
	})
}

func TestCursorSessionManagerReclaimsOnPeriodicTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := newCursorSessionManager(10, 9)
		session, err := manager.create("account", "catalog", nil, 1, 60, 30)
		require.NoError(t, err)
		atomic.StoreInt64(&session.ExpiresAtSec, 0)
		require.Empty(t, manager.reclaimRequested)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go manager.reclaimExpired(ctx)
		synctest.Wait()
		manager.mu.Lock()
		_, registered := manager.sessions[session.ID]
		manager.mu.Unlock()
		assert.True(t, registered)

		time.Sleep(time.Minute)
		synctest.Wait()
		manager.mu.Lock()
		_, registered = manager.sessions[session.ID]
		manager.mu.Unlock()
		assert.False(t, registered)
	})
}

func TestCursorSessionManagerMarkPageSuccess(t *testing.T) {
	t.Run("refreshes successful page and expiry timestamps", func(t *testing.T) {
		manager := newCursorSessionManager(2, 1)
		session, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
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
		session, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)

		manager.closeSession(session.ID)
		_, ok := manager.acquire(session.ID)
		assert.False(t, ok)
	})
}

func TestCursorSessionManagerCloseForAccount(t *testing.T) {
	manager := newCursorSessionManager(2, 1)
	session, err := manager.create("account-a", "catalog-1", nil, 1, 60, 30)
	require.NoError(t, err)
	assert.ErrorIs(t, manager.closeForAccount("account-b", session.ID), errCursorSessionForbidden)
	assert.ErrorIs(t, manager.closeForAccount("account-a", "missing"), errCursorSessionNotFound)
	session.Lock()
	assert.ErrorIs(t, manager.closeForAccount("account-a", session.ID), errCursorSessionBusy)
	session.Unlock()
	assert.NoError(t, manager.closeForAccount("account-a", session.ID))
	assert.ErrorIs(t, manager.closeForAccount("account-a", session.ID), errCursorSessionNotFound)
	_, err = manager.create("account-b", "catalog-1", nil, 1, 60, 30)
	assert.NoError(t, err)
}

func TestCloseCursorSession(t *testing.T) {
	previousManager := rawQueryCursorSessions
	manager := newCursorSessionManager(2, 1)
	rawQueryCursorSessions = manager
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })
	session, err := manager.create("account-a", "catalog-1", nil, 1, 60, 30)
	require.NoError(t, err)

	err = CloseCursorSession(context.Background(), "account-b", session.ID)
	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)

	session.Lock()
	err = CloseCursorSession(context.Background(), "account-a", session.ID)
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Query_CursorInUse, httpErr.ErrorCode)
	session.Unlock()

	require.NoError(t, CloseCursorSession(context.Background(), "account-a", session.ID))
	err = CloseCursorSession(context.Background(), "account-a", session.ID)
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusNotFound, httpErr.HTTPCode)
}

func TestCursorSessionManagerCloseExpiredSessions(t *testing.T) {
	t.Run("skips active session", func(t *testing.T) {
		manager := newCursorSessionManager(2, 1)
		session, err := manager.create("account-1", "catalog-1", nil, 1, 60, 30)
		require.NoError(t, err)
		session.ExpiresAtSec = time.Now().Add(-time.Second).Unix()

		session.Lock()
		manager.closeExpiredSessions(time.Now().Unix())
		_, ok := manager.sessions[session.ID]
		assert.True(t, ok)
		session.Unlock()

		manager.closeExpiredSessions(time.Now().Unix())
		assert.NotContains(t, manager.sessions, session.ID)
	})
}

func TestCursorPagingResponse(t *testing.T) {
	t.Run("returns cursor and expiry", func(t *testing.T) {
		manager := newCursorSessionManager(10, 9)
		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, 100, 60, 30)
		require.NoError(t, err)
		t.Cleanup(func() { manager.remove(session.ID) })

		response := cursorPagingResponse(session)

		require.NotNil(t, response.NextCursor)
		require.NotNil(t, response.ExpiresAtSec)
		assert.Equal(t, session.ID, *response.NextCursor)
		assert.Equal(t, session.ExpiresAtSec, *response.ExpiresAtSec)
	})
}

func TestCursorSessionManagerSQLRowsLifecycle(t *testing.T) {
	for _, reason := range []string{"remove", "final page", "explicit close", "expired acquire", "expired close", "periodic expiry"} {
		t.Run(reason, func(t *testing.T) {
			manager := newCursorSessionManager(10, 9)
			session, err := manager.create("owner", "catalog", nil, 1, 60, 30)
			require.NoError(t, err)
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2)).RowsWillBeClosed()
			rows, err := db.QueryContext(t.Context(), "SELECT id")
			require.NoError(t, err)
			defer func() { assert.NoError(t, rows.Close()) }()
			require.NoError(t, rows.Err())
			mock.ExpectClose()
			base := vmock.NewMockTableConnector(gomock.NewController(t))
			query := sb.Select(&sb.Column{Name: "id"})
			connectorClosed := false
			base.EXPECT().Close(gomock.Any()).DoAndReturn(func(context.Context) error {
				connectorClosed = true
				// 关闭连接不能持有全局锁，否则一个慢连接将阻塞所有 cursor。
				unlocked := manager.mu.TryLock()
				assert.True(t, unlocked)
				if unlocked {
					manager.mu.Unlock()
				}
				return db.Close()
			})
			connector := &sqlCursorConnector{
				TableConnector: base,
				open:           func(context.Context, *sb.SelectBuilder) (*sql.Rows, error) { return rows, nil },
			}
			stream, err := connector.OpenSQLRead(t.Context(), query, nil, 0)
			require.NoError(t, err)
			session.ReadConnector = connector
			session.ReadSession = stream
			page, err := session.ReadSession.ReadPage(t.Context(), session.PageLimit)
			require.NoError(t, err)
			require.True(t, page.HasMore)
			assert.ErrorIs(t, manager.closeForAccount("foreign", session.ID), errCursorSessionForbidden)
			assert.False(t, connectorClosed)
			session.Lock()
			atomic.StoreInt64(&session.ExpiresAtSec, 0)
			manager.closeExpiredSessions(time.Now().Unix())
			assert.Contains(t, manager.sessions, session.ID)
			assert.False(t, connectorClosed)
			session.Unlock()
			switch reason {
			case "remove":
				manager.remove(session.ID)
			case "final page":
				manager.closeSession(session.ID)
			case "explicit close":
				atomic.StoreInt64(&session.ExpiresAtSec, time.Now().Unix()+60)
				require.NoError(t, manager.closeForAccount("owner", session.ID))
			case "expired acquire":
				_, ok := manager.acquire(session.ID)
				assert.False(t, ok)
			case "expired close":
				assert.ErrorIs(t, manager.closeForAccount("owner", session.ID), errCursorSessionNotFound)
			case "periodic expiry":
				manager.closeExpiredSessions(time.Now().Unix())
			}
			if reason == "expired acquire" || reason == "expired close" {
				assert.False(t, connectorClosed, "rejecting an expired cursor must not close the connection")
				assert.Contains(t, manager.sessions, session.ID)
				assert.Len(t, manager.reclaimRequested, 1)
				manager.closeExpiredSessions(time.Now().Unix())
			}
			assert.True(t, connectorClosed)
			assert.Error(t, rows.Scan(new(any)))
			assert.NotContains(t, manager.sessions, session.ID)
			manager.remove(session.ID) // 重复释放不能再次关闭连接。
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
