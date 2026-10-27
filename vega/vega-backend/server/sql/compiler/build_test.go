// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"encoding/json"
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
		{"CURRENT_TIMESTAMP", "CURRENT_TIMESTAMP()", nil},
		{"CURRENT_TIMESTAMP()", "CURRENT_TIMESTAMP()", nil},
		{"CASE a WHEN 1 THEN 'one' ELSE NULL END", "CASE `a` WHEN ? THEN ? ELSE ? END", []any{json.Number("1"), "one", nil}},
		{"CASE WHEN a=1 THEN 'one' WHEN a=2 THEN 'two' END", "CASE WHEN (`a` = ?) THEN ? WHEN (`a` = ?) THEN ? END", []any{json.Number("1"), "one", json.Number("2"), "two"}},
		{"CAST(1.2300 AS DECIMAL(12,4))", "CAST(CAST(? AS DECIMAL(5,4)) AS DECIMAL(12,4))", []any{json.Number("1.2300")}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			query, err := ParseSQL("SELECT " + tc.input + " FROM {{x}}")
			require.NoError(t, err)
			text, args, err := mysql.ToSql(sb.Select(query.ColumnsPart[0]))
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
			assert.Equal(t, tc.want, query.ColumnsPart[0].(*sb.Cast).Type)
		})
	}
	query, err := ParseSQL("SELECT CAST(id AS DECIMAL(100,40)) FROM {{x}}")
	require.NoError(t, err)
	assert.Equal(t, []uint64{100, 40}, query.ColumnsPart[0].(*sb.Cast).Parameters)
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
