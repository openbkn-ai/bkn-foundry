package mariadb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestMariaDBConnectorBuildPagedSQL(t *testing.T) {
	connector := &MariaDBConnector{}
	assert.Equal(t,
		"SELECT * FROM (SELECT id FROM orders) AS _raw_query_page LIMIT 10 OFFSET 20",
		connector.BuildPagedSQL("SELECT id FROM orders", 20, 10),
	)
}

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
			want:     "`total` = 10",
		},
		{
			name:     "not equal string alias",
			having:   &interfaces.HavingClause{Field: "__value", Operation: "!=", Value: "ok"},
			aggAlias: "status_count",
			want:     "`status_count` <> 'ok'",
		},
		{
			name:     "reserved word alias is quoted",
			having:   &interfaces.HavingClause{Field: "__value", Operation: ">=", Value: 3},
			aggAlias: "key",
			want:     "`key` >= 3",
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
			want:     "`total` IN ('a', 'b')",
		},
		{
			name:     "range uses placeholders",
			having:   &interfaces.HavingClause{Field: "__value", Operation: "range", Value: []any{1, 3}},
			aggAlias: "total",
			want:     "`total` BETWEEN ? AND ?",
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

func TestBuildSelectBuilderQuotesIdentifiers(t *testing.T) {
	connector := &MariaDBConnector{}
	// fact 表带一个名为 key 的列——MySQL 保留字，裸拼进 SELECT 会在执行期报 1064
	resource := &interfaces.Resource{
		SourceIdentifier: "yanfeng_kb.fact",
		SchemaDefinition: []*interfaces.Property{
			{Name: "id", OriginalName: "id", Type: interfaces.DataType_String},
			{Name: "key", OriginalName: "key", Type: interfaces.DataType_String},
			{Name: "created", OriginalName: "created_at", Type: interfaces.DataType_Datetime},
		},
	}
	fieldMap := map[string]*interfaces.Property{}
	for _, prop := range resource.SchemaDefinition {
		fieldMap[prop.Name] = prop
	}

	t.Run("detail query quotes every column and the table", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource,
			&interfaces.ResourceDataQueryParams{Limit: 10}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `id`, `key`, `created_at` FROM `yanfeng_kb`.`fact` LIMIT 10 OFFSET 0",
			sql)
	})

	t.Run("output_fields resolve to quoted original names", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource,
			&interfaces.ResourceDataQueryParams{OutputFields: []string{"key", "created"}, Limit: 5}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t, "SELECT `key`, `created_at` FROM `yanfeng_kb`.`fact` LIMIT 5 OFFSET 0", sql)
	})

	t.Run("sort quotes the column and maps to its original name", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			OutputFields: []string{"id"},
			Sort:         []*interfaces.SortField{{Field: "created", Direction: interfaces.DESC_DIRECTION}},
			Limit:        5,
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `id` FROM `yanfeng_kb`.`fact` ORDER BY `created_at` DESC LIMIT 5 OFFSET 0",
			sql)
	})

	// 构建任务补出来的向量字段只有 Name 没有 OriginalName，取不到就回落到属性名，
	// 否则会拼出空标识符 `` 让整条 SQL 报错
	t.Run("a property without original_name falls back to the property name", func(t *testing.T) {
		schemaless := &interfaces.Resource{
			SourceIdentifier: "yanfeng_kb.fact",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", OriginalName: "id", Type: interfaces.DataType_String},
				{Name: "content_vector", Type: interfaces.DataType_Vector},
			},
		}
		schemalessFields := map[string]*interfaces.Property{}
		for _, prop := range schemaless.SchemaDefinition {
			schemalessFields[prop.Name] = prop
		}

		builder, err := connector.buildSelectBuilder(schemaless, &interfaces.ResourceDataQueryParams{
			OutputFields: []string{"content_vector"},
			Sort:         []*interfaces.SortField{{Field: "content_vector", Direction: interfaces.DESC_DIRECTION}},
			Limit:        5,
		}, schemalessFields, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `content_vector` FROM `yanfeng_kb`.`fact` ORDER BY `content_vector` DESC LIMIT 5 OFFSET 0",
			sql)
	})

	t.Run("aggregation quotes the aggregated column, group by and alias", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy:     []*interfaces.GroupByItem{{Property: "key"}},
			Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "cnt"},
			Limit:       20,
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `key`, COUNT(`id`) AS `cnt` FROM `yanfeng_kb`.`fact` GROUP BY `key` LIMIT 20 OFFSET 0",
			sql)
	})

	// 别名与 schema 属性同名时，排序必须认聚合别名而不是那个属性的源列
	t.Run("sorting by an aggregate alias that shadows a property uses the alias", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy:     []*interfaces.GroupByItem{{Property: "key"}},
			Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "created"},
			Sort:        []*interfaces.SortField{{Field: "created", Direction: interfaces.DESC_DIRECTION}},
			Limit:       20,
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `key`, COUNT(`id`) AS `created` FROM `yanfeng_kb`.`fact` GROUP BY `key` "+
				"ORDER BY `created` DESC LIMIT 20 OFFSET 0",
			sql)
	})

	t.Run("output_fields already selected as an alias are not duplicated", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy:      []*interfaces.GroupByItem{{Property: "key"}},
			Aggregation:  &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "cnt"},
			OutputFields: []string{"key", "cnt"},
			Limit:        20,
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `key`, COUNT(`id`) AS `cnt` FROM `yanfeng_kb`.`fact` GROUP BY `key` LIMIT 20 OFFSET 0",
			sql)
	})

	t.Run("calendar interval keeps the date_format expression over a quoted column", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy: []*interfaces.GroupByItem{{Property: "created", CalendarInterval: interfaces.CALENDAR_UNIT_DAY}},
			Limit:   20,
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT date_format(`created_at`,'%Y-%m-%d') AS `created` FROM `yanfeng_kb`.`fact` "+
				"GROUP BY date_format(`created_at`,'%Y-%m-%d') LIMIT 20 OFFSET 0",
			sql)
	})
}

func TestMariaDBConvertValue(t *testing.T) {
	t.Run("maria dbconvert value", func(t *testing.T) {
		assert.Equal(t, "hello", convertValue([]byte("hello")))
		assert.Equal(t, int64(1), convertValue(int64(1)))
		assert.Nil(t, convertValue(nil))
	})
}
