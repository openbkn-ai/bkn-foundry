// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorSessionManagerExpiryAndResponse(t *testing.T) {
	manager := &cursorSessionManager{sessions: make(map[string]*cursorSession)}
	session := manager.create("account-1", "catalog-1", []string{"resource-1"}, "SELECT 1", 100, 60, 30)
	t.Cleanup(func() { manager.remove(session.ID) })

	got, ok := manager.get(session.ID)
	require.True(t, ok)
	assert.Equal(t, session, got)

	response := cursorPagingResponse(session)
	require.NotNil(t, response.NextCursor)
	require.NotNil(t, response.ExpiresAtSec)
	assert.Equal(t, session.ID, *response.NextCursor)
	assert.Equal(t, session.ExpiresAtSec, *response.ExpiresAtSec)

	session.ExpiresAtSec = time.Now().Add(-time.Second).Unix()
	_, ok = manager.get(session.ID)
	assert.False(t, ok)
}

func TestCursorSessionRefreshExpiry(t *testing.T) {
	session := &cursorSession{KeepAliveSec: 60}
	now := time.Unix(1000, 0)
	session.refreshExpiry(now)
	assert.Equal(t, int64(1060), session.ExpiresAtSec)
}
