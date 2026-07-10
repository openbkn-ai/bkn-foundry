// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSQLSyntax(t *testing.T) {
	tests := []struct {
		name          string
		sql           string
		expectError   bool
		errorContains string
	}{
		{name: "valid SQL with node variable", sql: "SELECT * FROM .node1"},
		{name: "valid SQL with multiple node variables", sql: "SELECT .node1.id, .node2.name FROM .node1 JOIN .node2 ON .node1.id = .node2.user_id"},
		{name: "valid SQL with node variable in subquery", sql: "SELECT * FROM (SELECT id, name FROM .node1) AS subq"},
		{name: "valid SQL with node variable and WHERE", sql: "SELECT * FROM .node1 WHERE .node1.age > 18"},
		{name: "valid SQL with node variable and GROUP BY", sql: "SELECT .node1.department, COUNT(*) FROM .node1 GROUP BY .node1.department"},
		{name: "valid SQL with node variable and ORDER BY", sql: "SELECT * FROM .node1 ORDER BY .node1.created_at DESC LIMIT 10"},
		{name: "valid simple SELECT", sql: "SELECT * FROM users"},
		{name: "valid SELECT with columns", sql: "SELECT id, name, email FROM users WHERE age > 18"},
		{name: "valid SELECT with JOIN", sql: "SELECT u.id, o.total FROM users u JOIN orders o ON u.id = o.user_id"},
		{name: "valid SQL with GROUP BY", sql: "SELECT department, COUNT(*) FROM employees GROUP BY department"},
		{name: "valid SQL with ORDER BY", sql: "SELECT * FROM users ORDER BY created_at DESC LIMIT 10"},
		{name: "valid SQL with subquery", sql: "SELECT * FROM (SELECT id, name FROM users) AS subq"},
		{name: "valid SQL with DISTINCT", sql: "SELECT DISTINCT name FROM users"},
		{name: "valid SQL with WITH clause", sql: "WITH temp AS (SELECT * FROM users) SELECT * FROM temp"},
		{name: "empty SQL", sql: ""},
		{name: "invalid SQL - double FROM", sql: "SELECT * FROM FROM users", expectError: true, errorContains: "Duplicate FROM"},
		{name: "invalid SQL - double SELECT", sql: "SELECT SELECT * FROM users", expectError: true, errorContains: "Duplicate SELECT"},
		{name: "invalid SQL - missing table after FROM", sql: "SELECT * FROM", expectError: true, errorContains: "FROM clause must specify a table"},
		{name: "invalid SQL - unclosed parenthesis", sql: "SELECT * FROM users WHERE (id = 1", expectError: true, errorContains: "Unbalanced parentheses"},
		{name: "invalid SQL - extra closing parenthesis", sql: "SELECT * FROM users WHERE id = 1)", expectError: true, errorContains: "Unbalanced parentheses"},
		{name: "invalid SQL - missing SELECT keyword", sql: "* FROM users", expectError: true, errorContains: "must start with SELECT"},
		{name: "invalid SQL - WHERE without condition", sql: "SELECT * FROM users WHERE", expectError: true, errorContains: "WHERE clause must have a condition"},
		{name: "invalid SQL - GROUP BY without column", sql: "SELECT * FROM users GROUP BY", expectError: true, errorContains: "GROUP BY must have at least one column"},
		{name: "invalid SQL - ORDER BY without column", sql: "SELECT * FROM users ORDER BY", expectError: true, errorContains: "ORDER BY must have at least one column"},
		{name: "invalid SQL - dot notation without table", sql: "SELECT * FROM FROM .node1", expectError: true, errorContains: "Duplicate FROM"},
		{name: "invalid SQL - SELECT without FROM", sql: "SELECT id, name", expectError: true, errorContains: "must contain a FROM clause"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQLSyntax(context.Background(), tt.sql)
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
				return
			}

			require.NoError(t, err)
		})
	}
}
