package postgresql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestPostgresqlBuildHavingCondition(t *testing.T) {
	connector := &PostgresqlConnector{}
	tests := []struct {
		name       string
		having     *interfaces.HavingClause
		aggAlias   string
		want       string
		errContain string
	}{
		{
			name:     "equal uses placeholder",
			having:   &interfaces.HavingClause{Field: "__value", Operation: "==", Value: 10},
			aggAlias: "total",
			want:     "total = ?",
		},
		{
			name:     "not in formats list",
			having:   &interfaces.HavingClause{Field: "__value", Operation: "not_in", Value: []string{"a", "b"}},
			aggAlias: "total",
			want:     "total NOT IN ('a', 'b')",
		},
		{
			name:     "out range uses placeholders",
			having:   &interfaces.HavingClause{Field: "__value", Operation: "out_range", Value: []any{1, 3}},
			aggAlias: "total",
			want:     "total NOT BETWEEN ? AND ?",
		},
		{
			name:       "rejects unsupported field",
			having:     &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 1},
			aggAlias:   "total",
			errContain: "HAVING field must be",
		},
		{
			name:       "rejects invalid operation",
			having:     &interfaces.HavingClause{Field: "__value", Operation: "like", Value: 1},
			aggAlias:   "total",
			errContain: "unsupported HAVING operation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := connector.buildHavingCondition(tt.having, tt.aggAlias)

			if tt.errContain != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.errContain)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPostgresqlFormatInValues(t *testing.T) {
	assert.Equal(t, "1, 2", formatInValues([]any{1, 2}))
	assert.Equal(t, "'a', 'b'", formatInValues([]string{"a", "b"}))
	assert.Equal(t, "single", formatInValues("single"))
}

func TestPostgresqlConvertValue(t *testing.T) {
	utc := time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC)
	converted := convertValue(utc, "created_at", map[string]string{
		"created_at": "timestamptz",
	})

	require.IsType(t, time.Time{}, converted)
	assert.Equal(t, utc.Local(), converted)
	assert.Equal(t, "hello", convertValue([]byte("hello"), "name", map[string]string{"name": "varchar"}))
	assert.Equal(t, "hello", convertValue([]byte("hello"), "unknown", map[string]string{}))
	assert.Equal(t, int64(1), convertValue(int64(1), "count", map[string]string{"count": "int8"}))
	assert.Nil(t, convertValue(nil, "name", map[string]string{"name": "varchar"}))
}

func TestPostgresqlBuildConnString(t *testing.T) {
	connector := &PostgresqlConnector{
		config: &postgresqlConfig{
			Host:     "postgres",
			Port:     5432,
			Username: "user",
			Password: "pa ss",
			Database: "/app",
			Options: map[string]any{
				"search_path": "public",
				"sslmode":     "require",
			},
		},
	}

	got := connector.buildConnString()

	assert.Contains(t, got, "postgres://user:pa%20ss@postgres:5432/app?")
	assert.Contains(t, got, "search_path=public")
	assert.Contains(t, got, "sslmode=require")

	connector.config.Options = nil
	assert.Contains(t, connector.buildConnString(), "sslmode=disable")
}
