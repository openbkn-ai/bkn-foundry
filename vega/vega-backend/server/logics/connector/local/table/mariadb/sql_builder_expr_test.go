// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

func TestColumnToSql(t *testing.T) {
	for _, tc := range []struct {
		name   string
		column *sb.Column
		want   string
	}{
		{"plain", &sb.Column{Name: "id"}, "`id`"},
		{"qualified", &sb.Column{Qualifier: "a", Name: "id"}, "`a`.`id`"},
		{"literal dots and quotes", &sb.Column{Qualifier: "a.b", Name: "x`y.z"}, "`a.b`.`x``y.z`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, args, err := render(tc.column)
			require.NoError(t, err)
			assert.Equal(t, tc.want, text)
			assert.Empty(t, args)
		})
	}
	for _, name := range []string{"", "x\x00y", string([]byte{0xff})} {
		text, args, err := render((&sb.Column{Name: name}))
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
}

func TestParamToSql(t *testing.T) {
	for _, value := range []any{nil, "x' OR 1=1 --", "1.23000000000000000001", uint64(18446744073709551615), []byte{0, 1}, &sb.Column{Name: "not_expanded"}} {
		text, args, err := render((&sb.Param{Value: value}))
		require.NoError(t, err)
		assert.Equal(t, "?", text)
		assert.Equal(t, []any{value}, args)
	}
}

func TestStarToSql(t *testing.T) {
	for _, tc := range []struct {
		star *sb.Star
		want string
	}{{&sb.Star{}, "*"}, {&sb.Star{Qualifier: "a`b"}, "`a``b`.*"}} {
		text, args, err := render(tc.star)
		require.NoError(t, err)
		assert.Equal(t, tc.want, text)
		assert.Empty(t, args)
	}
}

func TestAliasToSql(t *testing.T) {
	text, args, err := render((&sb.Alias{Expr: &sb.Param{Value: 7}, Name: "value`name"}))
	require.NoError(t, err)
	assert.Equal(t, "? AS `value``name`", text)
	assert.Equal(t, []any{7}, args)
	for _, alias := range []*sb.Alias{{Expr: &sb.Param{Value: 1}}, {Name: "name"}} {
		text, args, err := render(alias)
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
}

func TestComparisonToSql(t *testing.T) {
	left, right := &sb.Param{Value: "left"}, &sb.Param{Value: "right"}
	for _, tc := range []struct {
		node sb.SqlNode
		op   string
	}{
		{&sb.Eq{Left: left, Right: right}, "="}, {&sb.NotEq{Left: left, Right: right}, "<>"},
		{&sb.Gt{Left: left, Right: right}, ">"}, {&sb.GtOrEq{Left: left, Right: right}, ">="},
		{&sb.Lt{Left: left, Right: right}, "<"}, {&sb.LtOrEq{Left: left, Right: right}, "<="},
	} {
		t.Run(tc.op, func(t *testing.T) {
			text, args, err := render(tc.node)
			require.NoError(t, err)
			assert.Equal(t, "(? "+tc.op+" ?)", text)
			assert.Equal(t, []any{"left", "right"}, args)
		})
	}
	text, args, err := render((&sb.Eq{Left: &sb.Column{Name: "x"}, Right: &sb.Param{Value: nil}}))
	require.NoError(t, err)
	assert.Equal(t, "(`x` = ?)", text)
	assert.Equal(t, []any{nil}, args)
	text, args, err = render((&sb.Eq{Left: left, Right: &sb.Column{}}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
	var missing *sb.Column
	text, args, err = render((&sb.Eq{Left: missing, Right: right}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestAndToSql(t *testing.T) {
	text, args, err := render((&sb.And{&sb.Eq{Left: &sb.Column{Name: "a"}, Right: &sb.Param{Value: 1}}, &sb.Or{&sb.Eq{Left: &sb.Column{Name: "b"}, Right: &sb.Param{Value: 2}}, &sb.Eq{Left: &sb.Column{Name: "c"}, Right: &sb.Param{Value: 3}}}}))
	require.NoError(t, err)
	assert.Equal(t, "((`a` = ?) AND ((`b` = ?) OR (`c` = ?)))", text)
	assert.Equal(t, []any{1, 2, 3}, args)
	text, args, err = render(&sb.And{})
	require.NoError(t, err)
	assert.Equal(t, "(1=1)", text)
	assert.Empty(t, args)
	text, args, err = render((&sb.And{nil}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestOrToSql(t *testing.T) {
	text, args, err := render((&sb.Or{}))
	require.NoError(t, err)
	assert.Equal(t, "(1=0)", text)
	assert.Empty(t, args)
}

func TestNotToSql(t *testing.T) {
	text, args, err := render((&sb.Not{Expr: &sb.Eq{Left: &sb.Column{Name: "x"}, Right: &sb.Param{Value: 1}}}))
	require.NoError(t, err)
	assert.Equal(t, "(NOT (`x` = ?))", text)
	assert.Equal(t, []any{1}, args)
}

func TestIsNullToSql(t *testing.T) {
	for _, tc := range []struct {
		negated bool
		want    string
	}{{false, "(`x` IS NULL)"}, {true, "(`x` IS NOT NULL)"}} {
		text, args, err := render((&sb.IsNull{Expr: &sb.Column{Name: "x"}, Negated: tc.negated}))
		require.NoError(t, err)
		assert.Equal(t, tc.want, text)
		assert.Empty(t, args)
	}
}

func TestFunctionToSql(t *testing.T) {
	for _, tc := range []struct {
		function *sb.Function
		want     string
		args     []any
	}{
		{&sb.Function{Name: "count", Args: []sb.SqlNode{&sb.Star{}}}, "COUNT(*)", nil},
		{&sb.Function{Name: "count", Args: []sb.SqlNode{&sb.Column{Name: "id"}}, Distinct: true}, "COUNT(DISTINCT `id`)", nil},
		{&sb.Function{Name: "coalesce", Args: []sb.SqlNode{&sb.Column{Name: "id"}, &sb.Param{Value: 0}}}, "COALESCE(`id`, ?)", []any{0}},
	} {
		text, args, err := render(tc.function)
		require.NoError(t, err)
		assert.Equal(t, tc.want, text)
		assert.Equal(t, tc.args, args)
	}
	for _, function := range []*sb.Function{{Name: "COUNT(*)"}, {Name: "x); DROP TABLE t; --"}, {}, {Name: "COUNT", Distinct: true}} {
		text, args, err := render(function)
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
}

func TestInToSql(t *testing.T) {
	for _, negated := range []bool{false, true} {
		keyword := " IN "
		if negated {
			keyword = " NOT IN "
		}
		node := &sb.In{Expr: &sb.Param{Value: "left"}, Values: []sb.SqlNode{&sb.Param{Value: "1.234567890123456789"}, &sb.Param{Value: nil}}, Negated: negated}
		text, args, err := render(node)
		require.NoError(t, err)
		assert.Equal(t, "(?"+keyword+"(?, ?))", text)
		assert.Equal(t, []any{"left", "1.234567890123456789", nil}, args)
	}
	for _, node := range []*sb.In{{Expr: &sb.Column{Name: "id"}}, {Expr: &sb.Column{Name: "id"}, Values: []sb.SqlNode{nil}}, {Values: []sb.SqlNode{&sb.Param{Value: 1}}}} {
		text, args, err := render(node)
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
}

func TestBetweenToSql(t *testing.T) {
	for _, negated := range []bool{false, true} {
		keyword := " BETWEEN "
		if negated {
			keyword = " NOT BETWEEN "
		}
		text, args, err := render((&sb.Between{Expr: &sb.Param{Value: "value"}, Lower: &sb.Param{Value: 1}, Upper: &sb.Param{Value: 10}, Negated: negated}))
		require.NoError(t, err)
		assert.Equal(t, "(?"+keyword+"? AND ?)", text)
		assert.Equal(t, []any{"value", 1, 10}, args)
	}
	text, args, err := render((&sb.Between{Expr: &sb.Column{Name: "id"}, Lower: &sb.Param{Value: 1}}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestLikeToSql(t *testing.T) {
	for _, escape := range []*string{nil, new("!"), new("'"), new("\\"), new("界")} {
		for _, negated := range []bool{false, true} {
			keyword := " LIKE "
			if negated {
				keyword = " NOT LIKE "
			}
			want := "(`name`" + keyword + "?"
			wantArgs := []any{"a%_b"}
			if escape != nil {
				want += " ESCAPE ?"
				wantArgs = append(wantArgs, *escape)
			}
			want += ")"
			text, args, err := render((&sb.Like{Expr: &sb.Column{Name: "name"}, Pattern: &sb.Param{Value: "a%_b"}, Escape: escape, Negated: negated}))
			require.NoError(t, err)
			assert.Equal(t, want, text)
			assert.Equal(t, wantArgs, args)
		}
	}
	for _, escape := range []string{"", "ab", string([]byte{0xff})} {
		text, args, err := render((&sb.Like{Expr: &sb.Column{Name: "name"}, Pattern: &sb.Param{Value: "x"}, Escape: &escape}))
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
	text, args, err := render((&sb.Like{Expr: &sb.Column{Name: "name"}}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestContainsToSql(t *testing.T) {
	value := "50%_!\\' OR 1=1 --"
	node := &sb.Contains{Expr: &sb.Column{Name: "name"}, Value: &sb.Param{Value: value}}
	text, args, err := render(node)
	require.NoError(t, err)
	assert.Equal(t, "(`name` LIKE CONCAT('%', REPLACE(REPLACE(REPLACE(?, '!', '!!'), '%', '!%'), '_', '!_'), '%') ESCAPE '!')", text)
	assert.Equal(t, []any{value}, args)
	assert.Equal(t, value, node.Value.(*sb.Param).Value)
	text, args, err = render((&sb.Contains{Expr: &sb.Param{Value: "haystack"}, Value: &sb.Column{Name: "needle"}}))
	require.NoError(t, err)
	assert.Equal(t, "(? LIKE CONCAT('%', REPLACE(REPLACE(REPLACE(`needle`, '!', '!!'), '%', '!%'), '_', '!_'), '%') ESCAPE '!')", text)
	assert.Equal(t, []any{"haystack"}, args)
	text, args, err = render((&sb.Contains{Expr: &sb.Column{Name: "name"}}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestArithmeticToSql(t *testing.T) {
	for _, op := range []sb.ArithmeticOperator{sb.Add, sb.Subtract, sb.Multiply, sb.Divide, sb.Modulo} {
		text, args, err := render((&sb.Arithmetic{Left: &sb.Param{Value: 6}, Right: &sb.Param{Value: 2}, Op: op}))
		require.NoError(t, err)
		assert.Equal(t, "(? "+string(op)+" ?)", text)
		assert.Equal(t, []any{6, 2}, args)
	}
	text, args, err := render((&sb.Arithmetic{Op: sb.Multiply, Left: &sb.Arithmetic{Left: &sb.Param{Value: 1}, Op: sb.Add, Right: &sb.Param{Value: 2}}, Right: &sb.Param{Value: 3}}))
	require.NoError(t, err)
	assert.Equal(t, "((? + ?) * ?)", text)
	assert.Equal(t, []any{1, 2, 3}, args)
	text, args, err = render((&sb.Arithmetic{Op: "+ 1); DROP TABLE t; --", Left: &sb.Param{Value: 1}, Right: &sb.Param{Value: 2}}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestUnaryToSql(t *testing.T) {
	for _, op := range []sb.UnaryOperator{sb.Positive, sb.Negative} {
		text, args, err := render((&sb.Unary{Expr: &sb.Param{Value: "18446744073709551615"}, Op: op}))
		require.NoError(t, err)
		assert.Equal(t, "("+string(op)+"?)", text)
		assert.Equal(t, []any{"18446744073709551615"}, args)
	}
	text, args, err := render((&sb.Unary{Expr: &sb.Param{Value: 1}, Op: "invalid"}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestCaseToSql(t *testing.T) {
	for _, tc := range []struct {
		name string
		node *sb.Case
		want string
		args []any
	}{
		{"searched", &sb.Case{WhenParts: []sb.When{{Condition: &sb.Eq{Left: &sb.Column{Name: "id"}, Right: &sb.Param{Value: 1}}, Result: &sb.Param{Value: "one"}}, {Condition: &sb.Param{Value: true}, Result: &sb.Param{Value: "other"}}}, Else: &sb.Param{Value: nil}}, "CASE WHEN (`id` = ?) THEN ? WHEN ? THEN ? ELSE ? END", []any{1, "one", true, "other", nil}},
		{"simple without else", &sb.Case{Operand: &sb.Param{Value: "operand"}, WhenParts: []sb.When{{Condition: &sb.Param{Value: "when"}, Result: &sb.Param{Value: "then"}}}}, "CASE ? WHEN ? THEN ? END", []any{"operand", "when", "then"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, args, err := render(tc.node)
			require.NoError(t, err)
			assert.Equal(t, tc.want, text)
			assert.Equal(t, tc.args, args)
		})
	}
	for _, node := range []*sb.Case{{}, {WhenParts: []sb.When{{Condition: &sb.Param{Value: true}}}}, {WhenParts: []sb.When{{Result: &sb.Param{Value: 1}}}}} {
		text, args, err := render(node)
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
	text, args, err := render((&sb.Case{WhenParts: []sb.When{{Condition: &sb.Param{Value: true}, Result: &sb.Param{Value: 1}}}, Else: &sb.Column{}}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestCastToSql(t *testing.T) {
	for _, tc := range []struct {
		typ        sb.CastType
		parameters []uint64
		target     string
	}{
		{sb.CastInteger, nil, "SIGNED"}, {sb.CastUnsignedInteger, nil, "UNSIGNED"}, {sb.CastDecimal, nil, "DECIMAL"},
		{sb.CastDecimal, []uint64{65, 30}, "DECIMAL(65,30)"}, {sb.CastDecimal, []uint64{10}, "DECIMAL(10)"},
		{sb.CastString, []uint64{20}, "CHAR(20)"}, {sb.CastBinary, []uint64{0}, "BINARY(0)"},
		{sb.CastDate, nil, "DATE"}, {sb.CastDateTime, []uint64{6}, "DATETIME(6)"}, {sb.CastTime, []uint64{0}, "TIME(0)"},
		{sb.CastJSON, nil, "JSON"}, {sb.CastFloat, []uint64{53}, "FLOAT(53)"}, {sb.CastDouble, nil, "DOUBLE"},
	} {
		text, args, err := render((&sb.Cast{Expr: &sb.Param{Value: "1.234567890123456789"}, Type: tc.typ, Parameters: tc.parameters}))
		require.NoError(t, err)
		assert.Equal(t, "CAST(? AS "+tc.target+")", text)
		assert.Equal(t, []any{"1.234567890123456789"}, args)
	}
	for _, node := range []*sb.Cast{
		{Type: sb.CastString}, {Expr: &sb.Param{Value: 1}}, {Expr: &sb.Param{Value: 1}, Type: "SIGNED); DROP TABLE t; --"},
		{Expr: &sb.Param{Value: 1}, Type: "invalid"}, {Expr: &sb.Param{Value: 1}, Type: sb.CastBoolean},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastTimestamp},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastInteger, Parameters: []uint64{1}},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastString, Parameters: []uint64{1, 2}},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastDecimal, Parameters: []uint64{0}},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastDecimal, Parameters: []uint64{66}},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastDecimal, Parameters: []uint64{65, 31}},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastDecimal, Parameters: []uint64{5, 6}},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastDecimal, Parameters: []uint64{10, 2, 1}},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastDateTime, Parameters: []uint64{7}},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastTime, Parameters: []uint64{1, 2}},
		{Expr: &sb.Param{Value: 1}, Type: sb.CastFloat, Parameters: []uint64{54}},
	} {
		text, args, err := render(node)
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
}

func TestNumericLiteralSQL(t *testing.T) {
	for _, tc := range []struct{ value, sql string }{
		{"1", "?"}, {"18446744073709551615", "?"}, {"1.2300", "CAST(? AS DECIMAL(5,4))"},
		{"18446744073709551616", "CAST(? AS DECIMAL(20,0))"}, {"1e-3", "?"},
	} {
		text, args, err := numericLiteralSQL(json.Number(tc.value))
		require.NoError(t, err)
		assert.Equal(t, tc.sql, text)
		assert.Equal(t, []any{json.Number(tc.value)}, args)
	}
	for _, value := range []string{"null", "true", "1;DELETE", strings.Repeat("1", 66), "0." + strings.Repeat("1", 31)} {
		text, args, err := numericLiteralSQL(json.Number(value))
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
}
