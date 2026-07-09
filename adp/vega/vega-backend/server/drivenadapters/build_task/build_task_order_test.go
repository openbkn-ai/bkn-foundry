// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package build_task

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"vega-backend/interfaces"
)

func Test_buildOrderByClause(t *testing.T) {
	t.Run("default puts active statuses first and ignores order", func(t *testing.T) {
		clause := buildOrderByClause(interfaces.BuildTaskOrderByDefault, "asc")

		assert.Contains(t, clause, "CASE f_status")
		assert.Contains(t, clause, "WHEN 'running' THEN 1")
		assert.Contains(t, clause, "WHEN 'completed' THEN 6")
		assert.True(t, strings.HasSuffix(clause, "END ASC, f_create_time DESC"))
	})

	t.Run("unknown order_by falls back to default", func(t *testing.T) {
		assert.True(t, strings.HasSuffix(buildOrderByClause("bogus", "desc"), "END ASC, f_create_time DESC"))
	})

	t.Run("created_at follows order direction without tie breaker", func(t *testing.T) {
		assert.Equal(t, "f_create_time ASC", buildOrderByClause(interfaces.BuildTaskOrderByCreatedAt, "asc"))
		assert.Equal(t, "f_create_time DESC", buildOrderByClause(interfaces.BuildTaskOrderByCreatedAt, "desc"))
	})

	t.Run("updated_at follows order direction without tie breaker", func(t *testing.T) {
		assert.Equal(t, "f_update_time ASC", buildOrderByClause(interfaces.BuildTaskOrderByUpdatedAt, "asc"))
		assert.Equal(t, "f_update_time DESC", buildOrderByClause(interfaces.BuildTaskOrderByUpdatedAt, "desc"))
	})

	t.Run("status bucket follows order direction with create tie breaker", func(t *testing.T) {
		assert.True(t, strings.HasSuffix(buildOrderByClause(interfaces.BuildTaskOrderByStatus, "asc"), "END ASC, f_create_time DESC"))
		assert.True(t, strings.HasSuffix(buildOrderByClause(interfaces.BuildTaskOrderByStatus, "desc"), "END DESC, f_create_time DESC"))
	})

	t.Run("mode follows order direction with create tie breaker", func(t *testing.T) {
		assert.Equal(t, "f_mode ASC, f_create_time DESC", buildOrderByClause(interfaces.BuildTaskOrderByMode, "asc"))
	})
}

func Test_statusBucketCase(t *testing.T) {
	clause := statusBucketCase()
	for _, status := range interfaces.BuildTaskStatusOrder {
		assert.Contains(t, clause, "WHEN '"+status+"' THEN ")
	}
	assert.True(t, strings.HasPrefix(clause, "CASE f_status"))
	assert.True(t, strings.HasSuffix(clause, "ELSE 99 END"))
}
