// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	mysql "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
)

func TestTreeBuilderExpressions(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		args        []any
	}{
		{"1+2*3-4/2%2", "((? + (? * ?)) - ((? / ?) % ?))", []any{json.Number("1"), json.Number("2"), json.Number("3"), json.Number("4"), json.Number("2"), json.Number("2")}},
		{"-1 + +2", "((-?) + (+?))", []any{json.Number("1"), json.Number("2")}},
		{"NOT (a=1 OR b<>2) AND c<=3", "((NOT ((`a` = ?) OR (`b` <> ?))) AND (`c` <= ?))", []any{json.Number("1"), json.Number("2"), json.Number("3")}},
		{"a!=1 OR a>2 OR a>=3 OR a<4", "((`a` <> ?) OR (`a` > ?) OR (`a` >= ?) OR (`a` < ?))", []any{json.Number("1"), json.Number("2"), json.Number("3"), json.Number("4")}},
		{"a IS NULL OR b IS NOT NULL", "((`a` IS NULL) OR (`b` IS NOT NULL))", nil},
		{"a NOT IN (1,NULL)", "(`a` NOT IN (?, ?))", []any{json.Number("1"), nil}},
		{"a BETWEEN 1 AND 2", "(`a` BETWEEN ? AND ?)", []any{json.Number("1"), json.Number("2")}},
		{"a NOT BETWEEN 1 AND 2", "(`a` NOT BETWEEN ? AND ?)", []any{json.Number("1"), json.Number("2")}},
		{"a LIKE 'x%_y'", "(`a` LIKE ? ESCAPE ?)", []any{"x%_y", "\\"}},
		{"a NOT LIKE 'x!%' ESCAPE '!'", "(`a` NOT LIKE ? ESCAPE ?)", []any{"x!%", "!"}},
		{"COUNT(DISTINCT a)", "COUNT(DISTINCT `a`)", nil},
		{"COALESCE(a,0)", "COALESCE(`a`, ?)", []any{json.Number("0")}},
		{"OCTET_LENGTH('中文')", "LENGTH(?)", []any{"中文"}},
		{"octet_length(NULL)", "LENGTH(?)", []any{nil}},
		{"CURRENT_TIMESTAMP", "CURRENT_TIMESTAMP()", nil},
		{"CURRENT_TIMESTAMP()", "CURRENT_TIMESTAMP()", nil},
		{"CASE a WHEN 1 THEN 'one' ELSE NULL END", "CASE `a` WHEN ? THEN ? ELSE ? END", []any{json.Number("1"), "one", nil}},
		{"CASE WHEN a=1 THEN 'one' WHEN a=2 THEN 'two' END", "CASE WHEN (`a` = ?) THEN ? WHEN (`a` = ?) THEN ? END", []any{json.Number("1"), "one", json.Number("2"), "two"}},
		{"CAST(1.2300 AS DECIMAL(12,4))", "CAST(CAST(? AS DECIMAL(5,4)) AS DECIMAL(12,4))", []any{json.Number("1.2300")}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			query, err := ParseSQL("SELECT " + tc.input + " FROM {{x}}")
			require.NoError(t, err)
			alias, ok := query.ColumnsPart[0].(*sb.Alias)
			require.True(t, ok)
			text, args, err := mysql.ToSql(sb.Select(alias.Expr))
			require.NoError(t, err)
			assert.Equal(t, "SELECT "+tc.want, text)
			assert.Equal(t, tc.args, args)
		})
	}
}

func TestTreeBuilderJoins(t *testing.T) {
	for _, tc := range []struct {
		clause string
		kind   sb.JoinKind
	}{
		{"JOIN {{b}} b ON a.id=b.id", sb.InnerJoin}, {"INNER JOIN {{b}} b ON a.id=b.id", sb.InnerJoin},
		{"LEFT OUTER JOIN {{b}} b ON a.id=b.id", sb.LeftJoin}, {"RIGHT JOIN {{b}} b ON a.id=b.id", sb.RightJoin},
		{"CROSS JOIN {{b}} b", sb.CrossJoin},
	} {
		query, err := ParseSQL("SELECT a.* FROM {{a}} a " + tc.clause)
		require.NoError(t, err)
		require.Len(t, query.JoinsPart, 1)
		assert.Equal(t, tc.kind, query.JoinsPart[0].Kind)
		assert.Equal(t, "b", query.JoinsPart[0].Source.(*sb.Table).ResourceID)
		if tc.kind == sb.CrossJoin {
			assert.Nil(t, query.JoinsPart[0].On)
		} else {
			assert.IsType(t, &sb.Eq{}, query.JoinsPart[0].On)
		}
	}
}

func TestStringValue(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`''`, ""}, {`'a''b'`, "a'b"}, {`"a""b"`, "a\"b"},
		{`'\0\b\n\r\t\Z'`, string([]byte{0, '\b', '\n', '\r', '\t', 26})},
		{`'\%\_\\\'\"\q'`, "\\%\\_\\'\"q"}, {`'中文'`, "中文"},
	} {
		assert.Equal(t, tc.want, stringValue(tc.input), tc.input)
	}
}

func TestTreeBuilderCastTypes(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  sb.CastType
	}{
		{"SIGNED", sb.CastInteger}, {"INTEGER", sb.CastInteger}, {"INT", sb.CastInteger}, {"BIGINT", sb.CastInteger},
		{"UNSIGNED", sb.CastUnsignedInteger}, {"DECIMAL", sb.CastDecimal},
		{"CHAR", sb.CastString}, {"VARCHAR", sb.CastString}, {"STRING", sb.CastString},
		{"BINARY", sb.CastBinary}, {"BOOLEAN", sb.CastBoolean}, {"BOOL", sb.CastBoolean},
		{"DATE", sb.CastDate}, {"DATETIME", sb.CastDateTime}, {"TIMESTAMP", sb.CastTimestamp},
		{"TIME", sb.CastTime}, {"FLOAT", sb.CastFloat}, {"DOUBLE", sb.CastDouble}, {"JSON", sb.CastJSON},
	} {
		t.Run(tc.input, func(t *testing.T) {
			query, err := ParseSQL("SELECT CAST(id AS " + tc.input + ") FROM {{x}}")
			require.NoError(t, err)
			assert.Equal(t, tc.want, query.ColumnsPart[0].(*sb.Alias).Expr.(*sb.Cast).Type)
		})
	}
	query, err := ParseSQL("SELECT CAST(id AS DECIMAL(100,40)) FROM {{x}}")
	require.NoError(t, err)
	assert.Equal(t, []uint64{100, 40}, query.ColumnsPart[0].(*sb.Alias).Expr.(*sb.Cast).Parameters)
	query.FromPart.(*sb.Table).SourceIdentifier = "`items`"
	text, args, err := mysql.ToSql(query)
	require.Error(t, err) // 目标限制属于 MySQL 生成器，不由语法解析阶段代替检查。
	assert.Empty(t, text)
	assert.Nil(t, args)
	query, err = ParseSQL("SELECT CAST(id AS unknown_type) FROM {{x}}")
	require.Error(t, err)
	assert.Nil(t, query)
}

func TestTreeBuilderOrderPosition(t *testing.T) {
	query, err := ParseSQL("SELECT id FROM {{orders}} ORDER BY 1 DESC")
	require.NoError(t, err)
	assert.EqualValues(t, 1, query.OrderByPart[0].Position)
	assert.Nil(t, query.OrderByPart[0].Expr)
	query.FromPart.(*sb.Table).SourceIdentifier = "`orders`"
	text, args, err := mysql.ToSql(query)
	require.NoError(t, err)
	assert.Equal(t, "SELECT `id` FROM `orders` ORDER BY 1 DESC", text)
	assert.Empty(t, args)
	_, err = ParseSQL("SELECT id FROM {{orders}} ORDER BY 0")
	require.Error(t, err)
}

// TestTreeBuilderGroupPosition 验证数字分组保持原始位置，语义由目标数据库判断。
func TestTreeBuilderGroupPosition(t *testing.T) {
	for _, input := range []string{
		"SELECT ID%2 AS k,COUNT(*) AS n FROM {{r}} GROUP BY 1",
		"SELECT ID FROM {{r}} GROUP BY 0",
		"SELECT ID FROM {{r}} GROUP BY 2",
		"SELECT COUNT(*) FROM {{r}} GROUP BY 1",
		"SELECT SUM(ID)+1 FROM {{r}} GROUP BY 1",
		"SELECT * FROM {{r}} GROUP BY 1",
	} {
		query, err := ParseSQL(input)
		require.NoError(t, err)
		_, ok := query.GroupByPart[0].(*sb.Param)
		assert.True(t, ok, input)
	}
}

func TestTreeBuilderProjectionNames(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		names       []string
		args        []any
	}{
		{"SELECT 1,2 FROM {{x}}", "SELECT ? AS `1`, ? AS `2` FROM `orders`", []string{"1", "2"}, []any{json.Number("1"), json.Number("2")}},
		{"SELECT 001,1.20,1e2 FROM {{x}}", "SELECT ? AS `001`, CAST(? AS DECIMAL(3,2)) AS `1.20`, ? AS `1e2` FROM `orders`", []string{"001", "1.20", "1e2"}, []any{json.Number("1"), json.Number("1.20"), json.Number("1e2")}},
		{"SELECT 'label','a`b','' FROM {{x}}", "SELECT ? AS `label`, ? AS `a``b`, ? AS `` FROM `orders`", []string{"label", "a`b", ""}, []any{"label", "a`b", ""}},
		{"SELECT 1 +  2, SUM(id) FROM {{x}}", "SELECT (? + ?) AS `1 +  2`, SUM(`id`) AS `SUM(id)` FROM `orders`", []string{"1 +  2", "SUM(id)"}, []any{json.Number("1"), json.Number("2")}},
		{"SELECT ((001)),(id),a.* FROM {{x}} a", "SELECT ? AS `001`, `id`, `a`.* FROM `orders` AS `a`", []string{"001"}, []any{json.Number("1")}},
		{"SELECT q.`1` FROM (SELECT 1 FROM {{x}}) q", "SELECT `q`.`1` FROM (SELECT ? AS `1` FROM `orders`) AS `q`", nil, []any{json.Number("1")}},
		{"SELECT NOW(6) FROM {{x}}", "SELECT NOW(6) AS `NOW(6)` FROM `orders`", []string{"NOW(6)"}, nil},
		{"SELECT 1 AS explicit_name FROM {{x}}", "SELECT ? AS `explicit_name` FROM `orders`", []string{"explicit_name"}, []any{json.Number("1")}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			query, err := ParseSQL(tc.input)
			require.NoError(t, err)
			if source, ok := query.FromPart.(*sb.Table); ok {
				source.SourceIdentifier = "`orders`"
			} else {
				query.FromPart.(*sb.Subquery).Query.FromPart.(*sb.Table).SourceIdentifier = "`orders`"
			}
			text, args, err := mysql.ToSql(query)
			require.NoError(t, err)
			assert.Equal(t, tc.want, text)
			assert.Equal(t, tc.args, args)
			for i, name := range tc.names {
				assert.Equal(t, name, query.ColumnsPart[i].(*sb.Alias).Name)
			}
			count := sb.Select(&sb.Function{Name: "COUNT", Args: []sb.SqlNode{&sb.Star{}}}).FromSelect(query, "total")
			text, args, err = mysql.ToSql(count)
			require.NoError(t, err)
			assert.Equal(t, "SELECT COUNT(*) FROM ("+tc.want+") AS `total`", text)
			assert.Equal(t, tc.args, args)
		})
	}
	t.Run("implicit names retain Unicode boundaries and literal names", func(t *testing.T) {
		for _, tc := range []struct{ expression, want string }{
			{"'  label '", "label "},
			{"'" + strings.Repeat("中", 100) + "'", strings.Repeat("中", 85)},
			{"1 /* keep spacing */ +  2", "1 /* keep spacing */ +  2"},
		} {
			query, err := ParseSQL("SELECT " + tc.expression + " FROM {{x}}")
			require.NoError(t, err)
			assert.Equal(t, tc.want, query.ColumnsPart[0].(*sb.Alias).Name)
		}
		query, err := ParseSQL("SELECT 'a\\0b' FROM {{x}}")
		assert.Nil(t, query)
		var failure *ParseError
		require.ErrorAs(t, err, &failure)
		assert.Equal(t, CodeUnsupported, failure.Code)
		query, err = ParseSQL("SELECT 'a\\0b' AS label FROM {{x}}")
		require.NoError(t, err)
		assert.Equal(t, "a\x00b", query.ColumnsPart[0].(*sb.Alias).Expr.(*sb.Param).Value)
	})

}

func TestTreeBuilderFunctionArguments(t *testing.T) {
	t.Run("byte length alias keeps the original output name", func(t *testing.T) {
		query, err := ParseSQL("SELECT OCTET_LENGTH(label) FROM {{x}}")
		require.NoError(t, err)
		alias := query.ColumnsPart[0].(*sb.Alias)
		assert.Equal(t, "OCTET_LENGTH(label)", alias.Name)
		assert.Equal(t, "LENGTH", alias.Expr.(*sb.Function).Name)
	})
	for _, expr := range []string{"OCTET_LENGTH()", "OCTET_LENGTH(1,2)", "OCTET_LENGTH(*)", "OCTET_LENGTH(DISTINCT id)"} {
		t.Run("reject "+expr, func(t *testing.T) {
			query, err := ParseSQL("SELECT " + expr + " FROM {{x}}")
			assert.Nil(t, query)
			require.Error(t, err)
		})
	}
	for _, expr := range []string{"CHAR(31)", "CHAR(65,66)", "CHAR(NULL,65,NULL)", "CHAR(id+64)", "CHAR(50089)"} {
		t.Run("accept "+expr, func(t *testing.T) {
			query, err := ParseSQL("SELECT " + expr + " AS value FROM {{x}}")
			require.NoError(t, err)
			assert.IsType(t, &sb.Function{}, query.ColumnsPart[0].(*sb.Alias).Expr)
		})
	}
	for _, expr := range []string{"CHAR()", "CHAR(*)", "CHAR(DISTINCT id)"} {
		t.Run("reject "+expr, func(t *testing.T) {
			query, err := ParseSQL("SELECT " + expr + " AS value FROM {{x}}")
			assert.Nil(t, query)
			var failure *ParseError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, CodeUnsupported, failure.Code)
		})
	}
	for _, expr := range []string{"COUNT()", "COUNT(a,b)", "COUNT(DISTINCT *)", "SUM(*)", "SUM(a,b)", "ABS()", "ABS(1,2)", "ABS(DISTINCT id)", "CONCAT(*)", "COALESCE()", "IFNULL(1)", "ROUND(1,2,3)", "SUBSTRING('a')", "SUBSTR('a',1,2,3)", "REPLACE('a','b')", "CONCAT_WS(',')", "NOW(7)", "NOW(-1)", "NOW(id)", "NOW(1.5)", "CURDATE(1)"} {
		t.Run("reject "+expr, func(t *testing.T) {
			query, err := ParseSQL("SELECT " + expr + " FROM {{x}}")
			assert.Nil(t, query)
			var failure *ParseError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, CodeUnsupported, failure.Code)
			assert.NotContains(t, err.Error(), expr)
		})
	}
	for _, expr := range []string{"COUNT(*)", "COUNT(id)", "COUNT(DISTINCT id,name)", "SUM(DISTINCT id)", "MIN(DISTINCT id)", "MAX(id)", "ABS(id)", "ROUND(id)", "ROUND(id,2)", "COALESCE(id,0)", "CONCAT(id)", "CONCAT_WS(',',id)", "IFNULL(id,0)", "SUBSTRING(id,1)", "SUBSTR(id,1,2)", "REPLACE(id,'a','b')", "NOW()", "NOW(6)", "CURDATE()", "CURRENT_TIMESTAMP"} {
		t.Run("accept "+expr, func(t *testing.T) {
			query, err := ParseSQL("SELECT " + expr + " FROM {{x}}")
			require.NoError(t, err)
			require.NotNil(t, query)
		})
	}
}
