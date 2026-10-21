// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	qt "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler/querytree"
)

func TestBuilderExpr(t *testing.T) {
	t.Run("case base and branch boundaries", func(t *testing.T) {
		query, err := ParseSQL("SELECT CASE a WHEN 1 THEN b WHEN 2 THEN c ELSE d END, CASE WHEN a>0 THEN b END FROM {{x}}")
		require.NoError(t, err)
		simple, searched := query.Projections[0].Expr, query.Projections[1].Expr
		assert.Equal(t, qt.Case, simple.Kind)
		assert.True(t, simple.CaseBase)
		assert.True(t, simple.CaseElse)
		require.Len(t, simple.Args, 6)
		assert.Equal(t, []string{"a"}, simple.Args[0].Name)
		assert.Equal(t, []string{"d"}, simple.Args[5].Name)
		assert.False(t, searched.CaseBase)
		assert.False(t, searched.CaseElse)
		require.Len(t, searched.Args, 2)
		assert.Equal(t, ">", searched.Args[0].Op)
	})
	t.Run("range set and pattern retain explicit flags", func(t *testing.T) {
		query, err := ParseSQL("SELECT a NOT BETWEEN 1 AND 2, b NOT IN (1,2), c NOT LIKE 'x%' ESCAPE '!', d LIKE 'x%', COUNT(DISTINCT a) FROM {{x}}")
		require.NoError(t, err)
		assert.Equal(t, qt.Between, query.Projections[0].Expr.Kind)
		assert.True(t, query.Projections[0].Expr.Negated)
		assert.Equal(t, qt.In, query.Projections[1].Expr.Kind)
		assert.True(t, query.Projections[1].Expr.Negated)
		assert.True(t, query.Projections[2].Expr.Negated)
		assert.Equal(t, "!", *query.Projections[2].Expr.Escape)
		assert.Equal(t, "\\", *query.Projections[3].Expr.Escape)
		assert.True(t, query.Projections[4].Expr.Distinct)
	})
}

func TestDecodeString(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`'a''b'`, "a'b"}, {`"a""b"`, `a"b`}, {`'a\0\b\n\r\t\Z'`, "a\x00\b\n\r\t\x1a"},
		{`'\%\_\q\\\'\"'`, "\\%\\_q\\'\""}, {`'甲乙'`, "甲乙"}, {`''`, ""},
	} {
		t.Run(tc.input, func(t *testing.T) { assert.Equal(t, tc.want, decodeString(tc.input)) })
	}
}
