// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	qt "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler/querytree"
)

func TestParseSQL(t *testing.T) {
	t.Run("native resource and qualified wildcard", func(t *testing.T) {
		q, err := ParseSQL("select a.* from {{ resource-1 }} a;")
		require.NoError(t, err)
		assert.Equal(t, "resource-1", q.From.ResourceID)
		assert.Equal(t, "a", q.From.Alias)
		assert.Equal(t, qt.Wildcard, q.Projections[0].Expr.Kind)
		assert.Equal(t, []string{"a"}, q.Projections[0].Expr.Name)
	})
	t.Run("join derived query and clause scopes", func(t *testing.T) {
		q, err := ParseSQL("SELECT a.id, COUNT(b.id) n FROM {{a}} a LEFT OUTER JOIN (SELECT id FROM {{b}}) b ON a.id = b.id WHERE a.id > 0 GROUP BY a.id HAVING COUNT(b.id) > 1 ORDER BY n DESC LIMIT 5,10")
		require.NoError(t, err)
		assert.Equal(t, qm.JoinLeft, q.From.JoinKind)
		assert.Equal(t, qt.Derived, q.From.Right.Kind)
		assert.Equal(t, "b", q.From.Right.Query.From.ResourceID)
		assert.Equal(t, ">", q.Where.Op)
		assert.Equal(t, "1", q.Having.Args[1].Text)
		assert.Len(t, q.GroupBy, 1)
		assert.True(t, q.OrderBy[0].Descending)
		assert.Equal(t, int64(5), q.Limit.Offset)
		assert.Equal(t, int64(10), q.Limit.Count)
	})
	t.Run("precision precedence and literal decoding", func(t *testing.T) {
		q, err := ParseSQL("SELECT 18446744073709551616, 1.234567890123456789e-9, 1 + 2 * 3, 'a''b\\n', \"c\\t\" FROM {{x}} WHERE NOT a = 1 OR b = 2 AND c IS NOT NULL")
		require.NoError(t, err)
		assert.Equal(t, "18446744073709551616", q.Projections[0].Expr.Text)
		assert.Equal(t, qm.LiteralInteger, q.Projections[0].Expr.LiteralKind)
		assert.Equal(t, "1.234567890123456789e-9", q.Projections[1].Expr.Text)
		assert.Equal(t, "*", q.Projections[2].Expr.Args[1].Op)
		assert.Equal(t, "a'b\n", q.Projections[3].Expr.Text)
		assert.Equal(t, "c\t", q.Projections[4].Expr.Text)
		assert.Equal(t, "OR", q.Where.Op)
		assert.Equal(t, "NOT", q.Where.Args[0].Op)
		assert.Equal(t, "AND", q.Where.Args[1].Op)
		assert.True(t, q.Where.Args[1].Args[1].Negated)
	})
	t.Run("utf8 byte span and quoted names", func(t *testing.T) {
		sql := "SELECT '甲', `名``称` FROM {{x}} WHERE `名``称` = '乙'"
		q, err := ParseSQL(sql)
		require.NoError(t, err)
		e := q.Projections[1].Expr
		assert.Equal(t, []string{"名`称"}, e.Name)
		assert.Equal(t, "`名``称`", sql[e.Span.Start:e.Span.End])
		assert.Equal(t, "'乙'", sql[q.Where.Args[1].Span.Start:q.Where.Args[1].Span.End])
	})
	t.Run("strings and comments do not introduce resources", func(t *testing.T) {
		q, err := ParseSQL("SELECT '{{ fake }}', `{{ quoted }}` FROM /* {{ hidden }} */ {{real}} -- {{ comment }}\n# {{other}}\n")
		require.NoError(t, err)
		assert.Equal(t, "real", q.From.ResourceID)
		assert.Equal(t, "{{ fake }}", q.Projections[0].Expr.Text)
		assert.Equal(t, []string{"{{ quoted }}"}, q.Projections[1].Expr.Name)
	})
	for _, sql := range []string{
		"SELECT CASE a WHEN 1 THEN 2 ELSE 3 END x FROM {{x}}",
		"SELECT CASE WHEN a IS NULL THEN 0 ELSE a END x FROM {{x}}",
		"SELECT CAST(a AS DECIMAL(20,6)), NOW(), CURRENT_TIMESTAMP FROM {{x}}",
		"SELECT * FROM {{x}} x RIGHT JOIN {{y}} y ON x.a = y.a CROSS JOIN {{z}} z",
		"SELECT * FROM {{x}} WHERE a NOT BETWEEN 1 AND 2 AND b NOT IN (1, -2, NULL) AND c LIKE 'a\\_%' ESCAPE '!' LIMIT 10 OFFSET 2",
	} {
		t.Run(sql, func(t *testing.T) {
			q, err := ParseSQL(sql)
			require.NoError(t, err)
			require.NotNil(t, q.From)
		})
	}
	for _, sql := range []string{
		"", "SELECT 1", "SELECT * FROM physical_table", "SELECT * FROM {{}}", "SELECT * FROM {{.id}}", "SELECT * FROM {{Upper}}", "SELECT * FROM {{a b}}", "SELECT * FROM {{x}",
		"SELECT * FROM {{" + strings.Repeat("a", 41) + "}}", "SELECT {{x}} FROM {{y}}", "SELECT * FROM {{x}}; SELECT * FROM {{y}}", "SELECT * FROM {{x}} trailing tokens",
		"SELECT * FROM {{x}} JOIN {{y}}", "SELECT * FROM (SELECT * FROM {{x}})", "SELECT * FROM {{x}} WHERE a IN ()", "SELECT * FROM {{x}} WHERE a =", "SELECT ))(((a))) FROM {{x}}",
		"SELECT 'unterminated FROM {{x}}", "SELECT * FROM {{x}} /* unterminated", "SELECT @variable FROM {{x}}", "SELECT ? FROM {{x}}",
		"SELECT * FROM {{x}} LIMIT -1", "SELECT * FROM {{x}} LIMIT 1.5", "SELECT * FROM {{x}} LIMIT 9223372036854775808", "SELECT * FROM {{x}} WHERE a LIKE 'x' ESCAPE 'too long'",
	} {
		t.Run("reject "+sql, func(t *testing.T) {
			q, err := ParseSQL(sql)
			require.Error(t, err)
			assert.Nil(t, q)
			var classified *qm.CompileError
			require.ErrorAs(t, err, &classified)
			assert.Equal(t, qm.ErrorSyntax, classified.Kind)
		})
	}
	for _, sql := range []string{
		"WITH t AS (SELECT * FROM {{x}}) SELECT * FROM t", "SELECT * FROM {{x}} UNION SELECT * FROM {{y}}", "DELETE FROM {{x}}",
		"SELECT /*+ hint */ * FROM {{x}}", "SELECT /*!50000 1 */ * FROM {{x}}", "SELECT * FROM {{x}} FOR UPDATE", "SELECT * INTO file FROM {{x}}",
		"SELECT * FROM {{x}} FULL JOIN {{y}} ON 1 = 1", "SELECT * FROM {{x}} JOIN {{y}} USING (id)",
	} {
		t.Run("unsupported "+sql, func(t *testing.T) {
			q, err := ParseSQL(sql)
			require.Error(t, err)
			assert.Nil(t, q)
			var e *qm.CompileError
			require.ErrorAs(t, err, &e)
			assert.Equal(t, qm.ErrorUnsupported, e.Kind)
		})
	}
	for _, tc := range []struct{ name, sql string }{
		{"bytes", strings.Repeat(" ", qm.MaxSQLBytes+1)},
		{"tokens", "SELECT " + strings.Repeat("a,", qm.MaxTokens/2) + "a FROM {{x}}"},
		{"hidden tokens", "SELECT * FROM {{x}}" + strings.Repeat("/*x*/", qm.MaxTokens)},
		{"parentheses", "SELECT " + strings.Repeat("(", qm.MaxDepth+1) + "a" + strings.Repeat(")", qm.MaxDepth+1) + " FROM {{x}}"},
		{"prefix", "SELECT * FROM {{x}} WHERE " + strings.Repeat("NOT ", qm.MaxDepth+1) + "a"},
		{"expression depth", "SELECT a" + strings.Repeat("+a", qm.MaxDepth) + " FROM {{x}}"},
		{"nodes", "SELECT " + strings.Repeat(strings.Repeat("a+", 25)+"a,", qm.MaxNodes/51+1) + "a FROM {{x}}"},
		{"in values", "SELECT * FROM {{x}} WHERE a IN (" + strings.Repeat("1,", qm.MaxINValues) + "1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := ParseSQL(tc.sql)
			require.Error(t, err)
			assert.Nil(t, q)
			var e *qm.CompileError
			require.ErrorAs(t, err, &e)
			assert.Equal(t, qm.ErrorLimit, e.Kind)
		})
	}
	t.Run("token budget includes hidden tokens at boundary", func(t *testing.T) {
		// SELECT、*、FROM、RESOURCE 共四个 token，其余是隐藏通道注释。
		sql := "SELECT*FROM{{x}}" + strings.Repeat("/*x*/", qm.MaxTokens-4)
		query, err := ParseSQL(sql)
		require.NoError(t, err)
		assert.Equal(t, "x", query.From.ResourceID)
		query, err = ParseSQL(sql + "/*x*/")
		require.Error(t, err)
		assert.Nil(t, query)
		var classified *qm.CompileError
		require.ErrorAs(t, err, &classified)
		assert.Equal(t, qm.ErrorLimit, classified.Kind)
	})
	t.Run("in value boundary", func(t *testing.T) {
		sql := "SELECT * FROM {{x}} WHERE a IN (" + strings.Repeat("1,", qm.MaxINValues-1) + "1)"
		query, err := ParseSQL(sql)
		require.NoError(t, err)
		assert.Len(t, query.Where.Args, qm.MaxINValues+1)
	})
	t.Run("lexical error byte position", func(t *testing.T) {
		sql := "SELECT '甲', @ FROM {{x}}"
		_, err := ParseSQL(sql)
		var e *qm.CompileError
		require.ErrorAs(t, err, &e)
		assert.Equal(t, "@", sql[e.Origin.Span.Start:e.Origin.Span.End])
	})
	t.Run("parallel calls have independent trees", func(t *testing.T) {
		for i := 0; i < 8; i++ {
			t.Run("parse", func(t *testing.T) {
				t.Parallel()
				q, err := ParseSQL("SELECT a.* FROM {{id}} a")
				require.NoError(t, err)
				assert.Equal(t, "id", q.From.ResourceID)
				q.From.ResourceID = "changed"
			})
		}
	})
	t.Run("invalid utf8", func(t *testing.T) {
		q, err := ParseSQL("SELECT '\xff' FROM {{x}}")
		require.Error(t, err)
		assert.Nil(t, q)
	})
	t.Run("error excludes input", func(t *testing.T) {
		_, err := ParseSQL("SELECT private_secret FROM @sensitive")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private_secret")
		assert.NotContains(t, err.Error(), "sensitive")
	})
}

func FuzzParseSQL(f *testing.F) {
	for _, seed := range []string{"SELECT a.* FROM {{id}} a", "SELECT CASE WHEN x > 0 THEN 1 END FROM {{x}}", "SELECT '{{x}}' FROM {{x}}", "SELECT * FROM {{x}} WHERE a IN (1,2)"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, sql string) {
		q, err := ParseSQL(sql)
		if err != nil {
			require.Nil(t, q)
		} else {
			require.NotNil(t, q)
			require.NotNil(t, q.From)
		}
	})
}
