package mariadb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestBuildHavingCondition(t *testing.T) {
	connector := &MariaDBConnector{}
	tests := []struct {
		name       string
		having     *interfaces.HavingClause
		aggAlias   string
		want       string
		errContain string
	}{
		{
			name:     "equal numeric alias",
			having:   &interfaces.HavingClause{Field: "__value", Operation: "==", Value: 10},
			aggAlias: "total",
			want:     "total = 10",
		},
		{
			name:     "not equal string alias",
			having:   &interfaces.HavingClause{Field: "__value", Operation: "!=", Value: "ok"},
			aggAlias: "status_count",
			want:     "status_count <> 'ok'",
		},
		{
			name:   "count star uses count expression",
			having: &interfaces.HavingClause{Field: "count(*)", Operation: ">=", Value: 3},
			want:   "COUNT(*) >= 3",
		},
		{
			name:     "in string list quotes values",
			having:   &interfaces.HavingClause{Field: "__value", Operation: "in", Value: []string{"a", "b"}},
			aggAlias: "total",
			want:     "total IN ('a', 'b')",
		},
		{
			name:     "range uses placeholders",
			having:   &interfaces.HavingClause{Field: "__value", Operation: "range", Value: []any{1, 3}},
			aggAlias: "total",
			want:     "total BETWEEN ? AND ?",
		},
		{
			name:       "rejects unsupported field",
			having:     &interfaces.HavingClause{Field: "name", Operation: "==", Value: 1},
			errContain: "HAVING field must be",
		},
		{
			name:       "rejects unsupported operation",
			having:     &interfaces.HavingClause{Field: "__value", Operation: "like", Value: 1},
			aggAlias:   "total",
			errContain: "unsupported HAVING operation",
		},
		{
			name:       "rejects invalid range value",
			having:     &interfaces.HavingClause{Field: "__value", Operation: "range", Value: []any{1}},
			aggAlias:   "total",
			errContain: "range operation requires",
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

func TestFormatInValues(t *testing.T) {
	t.Run("format in values", func(t *testing.T) {
		assert.Equal(t, "1, 2", formatInValues([]any{1, 2}))
		assert.Equal(t, "'a', 'b'", formatInValues([]string{"a", "b"}))
		assert.Equal(t, "single", formatInValues("single"))
	})
}

func TestBuildDateFormat(t *testing.T) {
	connector := &MariaDBConnector{}
	tests := []struct {
		name     string
		interval string
		want     string
	}{
		{name: "minute", interval: interfaces.CALENDAR_UNIT_MINUTE, want: "date_format(created_at,'%Y-%m-%d %H:%i')"},
		{name: "hour", interval: interfaces.CALENDAR_UNIT_HOUR, want: "date_format(created_at,'%Y-%m-%d %H')"},
		{name: "day", interval: interfaces.CALENDAR_UNIT_DAY, want: "date_format(created_at,'%Y-%m-%d')"},
		{name: "week", interval: interfaces.CALENDAR_UNIT_WEEK, want: "date_format(created_at,'%x-%v')"},
		{name: "month", interval: interfaces.CALENDAR_UNIT_MONTH, want: "date_format(created_at,'%Y-%m')"},
		{name: "quarter", interval: interfaces.CALENDAR_UNIT_QUARTER, want: "format('%d-Q%d',year(created_at),quarter(created_at))"},
		{name: "year", interval: interfaces.CALENDAR_UNIT_YEAR, want: "date_format(created_at,'%Y')"},
		{name: "unknown", interval: "unknown", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, connector.buildDateFormat("created_at", "created_at", tt.interval))
		})
	}
}

func TestMariaDBConvertValue(t *testing.T) {
	t.Run("maria dbconvert value", func(t *testing.T) {
		assert.Equal(t, "hello", convertValue([]byte("hello")))
		assert.Equal(t, int64(1), convertValue(int64(1)))
		assert.Nil(t, convertValue(nil))
	})
}
