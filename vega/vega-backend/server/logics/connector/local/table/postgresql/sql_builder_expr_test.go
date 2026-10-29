// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package postgresql

import (
	"encoding/json"
	"strings"
	"testing"

	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/sql/compiler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToSql 检查 PG 目标生成的标识符、绑定编号、类型、子句及拒绝边界。
func TestToSql(t *testing.T) {
	t.Run("parenthesized join group preserves predicates and binding order", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT a.ID,b.ID AS matched FROM {{r}} a LEFT JOIN ({{r}} m JOIN {{r}} b ON m.ID=b.ID AND b.LABEL='inner') ON a.ID=m.ID AND a.LABEL='outer' WHERE b.ID IS NULL")
		require.NoError(t, err)
		group := query.JoinsPart[0].Source.(*sb.JoinedTable)
		for _, node := range []sb.SqlNode{query.FromPart, group.From, group.Joins[0].Source} {
			node.(*sb.Table).SourceIdentifier = "\"T\""
		}
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT \"a\".\"ID\", \"b\".\"ID\" AS \"matched\" FROM \"T\" AS \"a\" LEFT JOIN (\"T\" AS \"m\" INNER JOIN \"T\" AS \"b\" ON ((\"m\".\"ID\" = \"b\".\"ID\") AND (\"b\".\"LABEL\" = $1))) ON ((\"a\".\"ID\" = \"m\".\"ID\") AND (\"a\".\"LABEL\" = $2)) WHERE (\"b\".\"ID\" IS NULL)", text)
		assert.Equal(t, []any{"inner", "outer"}, args)
	})

	t.Run("CHAR preserves byte construction and bound arguments", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT CHAR(31,NULL,50089,ID+64) AS value FROM {{r}}")
		require.NoError(t, err)
		query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Contains(t, text, "CONVERT_FROM(DECODE(CONCAT(")
		assert.Contains(t, text, "TO_HEX(")
		assert.Contains(t, text, "4294967296")
		assert.Contains(t, text, "4294967295")
		assert.Equal(t, []any{"31", nil, "50089", "64"}, args)
		assert.NotContains(t, text, "CHR(")
	})
	t.Run("DISTINCT repeats the projected expression bindings in ORDER BY", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT DISTINCT DATE_FORMAT(TIME_VALUE,'%Y-%m') AS m FROM {{r}} ORDER BY DATE_FORMAT(TIME_VALUE,'%Y-%m')")
		require.NoError(t, err)
		query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, `SELECT DISTINCT TO_CHAR(CAST("TIME_VALUE" AS TIMESTAMP), $1) AS "m" FROM "s"."t" ORDER BY TO_CHAR(CAST("TIME_VALUE" AS TIMESTAMP), $1) ASC NULLS FIRST`, text)
		assert.Equal(t, []any{`YYYY"-"MM`}, args)
	})
	t.Run("HAVING aggregate arguments resolve source columns rather than aliases", func(t *testing.T) {
		for _, function := range []string{"COUNT", "SUM", "AVG", "MIN", "MAX"} {
			t.Run(function, func(t *testing.T) {
				query, err := compiler.ParseSQL("SELECT dept," + function + "(amount) AS amount FROM {{r}} GROUP BY dept HAVING " + function + "(amount)>100 AND amount>200")
				require.NoError(t, err)
				query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
				text, args, err := ToSql(query)
				require.NoError(t, err)
				assert.Contains(t, text, ` HAVING ((`+function+`("amount") > CAST($1 AS BIGINT)) AND (`+function+`("amount") > CAST($2 AS BIGINT)))`)
				assert.NotContains(t, text, function+"("+function+"(")
				assert.Equal(t, []any{"100", "200"}, args)
			})
		}
	})
	t.Run("equivalent rendered fragments reuse bindings without matching AST shapes", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT DATE(STR_TO_DATE('2020-01-02','%Y-%m-%d')) AS d,COUNT(*) AS n FROM {{r}} GROUP BY CAST(STR_TO_DATE('2020-01-02','%Y-%m-%d') AS DATE)")
		require.NoError(t, err)
		query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, `SELECT CAST(CAST(TO_TIMESTAMP(CAST($1 AS TEXT), $2) AS TIMESTAMP) AS DATE) AS "d", COUNT(*) AS "n" FROM "s"."t" GROUP BY CAST(CAST(TO_TIMESTAMP(CAST($1 AS TEXT), $2) AS TIMESTAMP) AS DATE)`, text)
		assert.Equal(t, []any{"2020-01-02", `YYYY"-"MM"-"DD`}, args)
	})
	t.Run("fragment reuse preserves placeholder-like text and parameter type contexts", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT COALESCE(`a$1?`,'literal$2?') AS k,COUNT(*) AS n FROM {{r}} GROUP BY COALESCE(`a$1?`,'literal$2?')")
		require.NoError(t, err)
		query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, `SELECT COALESCE("a$1?", $1) AS "k", COUNT(*) AS "n" FROM "s"."t" GROUP BY COALESCE("a$1?", $1)`, text)
		assert.Equal(t, []any{"literal$2?"}, args)
		query, err = compiler.ParseSQL("SELECT COALESCE(AMOUNT,'1') AS a,COALESCE(GROUP_CODE,'1') AS b FROM {{r}} GROUP BY COALESCE(AMOUNT,'1'),COALESCE(GROUP_CODE,'1')")
		require.NoError(t, err)
		query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err = ToSql(query)
		require.NoError(t, err)
		assert.Contains(t, text, `COALESCE("AMOUNT", $1)`)
		assert.Contains(t, text, `COALESCE("GROUP_CODE", $2)`)
		assert.Equal(t, []any{"1", "1"}, args)
	})
	t.Run("grouping positions and function synonyms preserve expression bindings", func(t *testing.T) {
		for _, input := range []string{
			"SELECT SUBSTRING(LABEL,1,1) AS k,COUNT(*) AS n FROM {{r}} GROUP BY 1 ORDER BY k",
			"SELECT SUBSTRING(LABEL,1,1) AS k,COUNT(*) AS n FROM {{r}} GROUP BY SUBSTR(LABEL,1,1) ORDER BY k",
		} {
			query, err := compiler.ParseSQL(input)
			require.NoError(t, err)
			query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
			text, args, err := ToSql(query)
			require.NoError(t, err)
			if strings.Contains(input, "GROUP BY 1") {
				assert.Contains(t, text, " GROUP BY 1 ORDER BY")
			} else {
				assert.Contains(t, text, " GROUP BY CASE WHEN CAST($1 AS BIGINT)")
			}
			assert.Len(t, args, 7)
		}
	})
	t.Run("star projections do not change grouping into a projection ordinal", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT a.*,SUBSTRING(b.LABEL,1,1) AS k,COUNT(*) AS n FROM (SELECT GROUP_CODE,ID FROM {{r}}) a JOIN {{r}} b ON a.GROUP_CODE=b.GROUP_CODE GROUP BY a.GROUP_CODE,a.ID,SUBSTRING(b.LABEL,1,1)")
		require.NoError(t, err)
		query.FromPart.(*sb.Subquery).Query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		query.JoinsPart[0].Source.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Contains(t, text, ` GROUP BY "a"."GROUP_CODE", "a"."ID", CASE WHEN CAST($1 AS BIGINT)`)
		assert.Len(t, args, 7)
	})
	t.Run("parsed grouping expressions reuse their projection and bindings", func(t *testing.T) {
		for _, tc := range []struct {
			name, input, group, order string
			args                      []any
		}{
			{"arithmetic", "SELECT ID % 2 AS k,COUNT(*) AS n FROM {{r}} GROUP BY ID % 2 ORDER BY ID % 2", ` GROUP BY ("ID" % CAST($1 AS BIGINT))`, ` ORDER BY ("ID" % CAST($1 AS BIGINT)) ASC NULLS FIRST`, []any{"2"}},
			{"format with HAVING alias", "SELECT DATE_FORMAT(TIME_VALUE,'%Y-%m') AS m,COUNT(*) AS n FROM {{r}} GROUP BY DATE_FORMAT(TIME_VALUE,'%Y-%m') HAVING m>='2020-02' ORDER BY DATE_FORMAT(TIME_VALUE,'%Y-%m')", ` GROUP BY TO_CHAR(CAST("TIME_VALUE" AS TIMESTAMP), $1)`, ` HAVING (TO_CHAR(CAST("TIME_VALUE" AS TIMESTAMP), $1) >= $2) ORDER BY TO_CHAR(CAST("TIME_VALUE" AS TIMESTAMP), $1) ASC NULLS FIRST`, []any{`YYYY"-"MM`, "2020-02"}},
			{"group alias with explicit HAVING expression", "SELECT ID % 2 AS k,COUNT(*) AS n FROM {{r}} GROUP BY k HAVING ID % 2=1 ORDER BY ID % 2", ` GROUP BY "k"`, ` HAVING (("ID" % CAST($1 AS BIGINT)) = CAST($2 AS BIGINT)) ORDER BY ("ID" % CAST($1 AS BIGINT)) ASC NULLS FIRST`, []any{"2", "1"}},
			{"substring", "SELECT SUBSTRING(LABEL,1,1) AS k,COUNT(*) AS n FROM {{r}} GROUP BY SUBSTRING(LABEL,1,1) ORDER BY SUBSTRING(LABEL,1,1)", " GROUP BY CASE WHEN CAST($1 AS BIGINT)", " ORDER BY CASE WHEN", []any{"1", "1", "1", "1", "1", "1", "1"}},
			{"not projected", "SELECT COUNT(*) AS n FROM {{r}} GROUP BY ID % 2 HAVING ID % 2=1 ORDER BY ID % 2", ` GROUP BY ("ID" % CAST($1 AS BIGINT))`, ` HAVING (("ID" % CAST($1 AS BIGINT)) = CAST($2 AS BIGINT)) ORDER BY ("ID" % CAST($1 AS BIGINT)) ASC NULLS FIRST`, []any{"2", "1"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				query, err := compiler.ParseSQL(tc.input)
				require.NoError(t, err)
				query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
				before, err := json.Marshal(query)
				require.NoError(t, err)
				text, args, err := ToSql(query)
				require.NoError(t, err)
				assert.Contains(t, text, tc.group)
				assert.Contains(t, text, tc.order)
				assert.Equal(t, tc.args, args)
				again, bound, err := ToSql(query)
				require.NoError(t, err)
				assert.Equal(t, text, again)
				assert.Equal(t, args, bound)
				after, err := json.Marshal(query)
				require.NoError(t, err)
				assert.Equal(t, before, after)
			})
		}
	})
	t.Run("HAVING source names that are also aliases retain alias expansion", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT ID+1 AS ID,COUNT(*) AS n FROM {{r}} GROUP BY ID+1 HAVING ID+1=3")
		require.NoError(t, err)
		query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Contains(t, text, `GROUP BY ("ID" + CAST($1 AS BIGINT)) HAVING ((("ID" + CAST($1 AS BIGINT)) + CAST($2 AS BIGINT)) = CAST($3 AS BIGINT))`)
		assert.Equal(t, []any{"1", "1", "3"}, args)
	})
	t.Run("equal standalone strings in different contexts keep separate bindings", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT GROUP_CODE,COUNT(*) AS n FROM {{r}} WHERE AMOUNT='1' AND GROUP_CODE='1' GROUP BY GROUP_CODE")
		require.NoError(t, err)
		query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Contains(t, text, `WHERE (("AMOUNT" = $1) AND ("GROUP_CODE" = $2))`)
		assert.Equal(t, []any{"1", "1"}, args)
	})
	t.Run("different grouping values remain separate", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT ID % 2 AS k,COUNT(*) AS n FROM {{r}} GROUP BY ID % 3")
		require.NoError(t, err)
		query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, `SELECT ("ID" % CAST($1 AS BIGINT)) AS "k", COUNT(*) AS "n" FROM "s"."t" GROUP BY ("ID" % CAST($2 AS BIGINT))`, text)
		assert.Equal(t, []any{"2", "3"}, args)
	})
	t.Run("nested grouping caches stay within their SELECT", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT q.k % 3 AS m,COUNT(*) AS n FROM (SELECT ID % 2 AS k FROM {{r}} GROUP BY ID % 2) q GROUP BY q.k % 3 ORDER BY q.k % 3")
		require.NoError(t, err)
		query.FromPart.(*sb.Subquery).Query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, `SELECT ("q"."k" % CAST($1 AS BIGINT)) AS "m", COUNT(*) AS "n" FROM (SELECT ("ID" % CAST($2 AS BIGINT)) AS "k" FROM "s"."t" GROUP BY ("ID" % CAST($2 AS BIGINT))) AS "q" GROUP BY ("q"."k" % CAST($1 AS BIGINT)) ORDER BY ("q"."k" % CAST($1 AS BIGINT)) ASC NULLS FIRST`, text)
		assert.Equal(t, []any{"3", "2"}, args)
	})

	t.Run("nested numbering does not rewrite quoted identifiers or string contents", func(t *testing.T) {
		inner := sb.Select(&sb.Alias{Expr: &sb.Param{Value: json.Number("9007199254740993")}, Name: "id?"}).Where(&sb.Eq{Left: &sb.Param{Value: true}, Right: &sb.Param{Value: true}})
		query := sb.Select(&sb.Alias{Expr: &sb.Param{Value: "literal$1?"}, Name: `a"?`}, &sb.Column{Qualifier: "q", Name: "id?"}).FromSelect(inner, "q").Where(&sb.Gt{Left: &sb.Column{Qualifier: "q", Name: "id?"}, Right: &sb.Param{Value: json.Number("1")}})
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, `SELECT $1 AS "a""?", "q"."id?" FROM (SELECT CAST($2 AS BIGINT) AS "id?" WHERE (CAST($3 AS BOOLEAN) = CAST($4 AS BOOLEAN))) AS "q" WHERE ("q"."id?" > CAST($5 AS BIGINT))`, text)
		assert.Equal(t, []any{"literal$1?", "9007199254740993", true, true, "1"}, args)
		again, bound, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, text, again)
		assert.Equal(t, args, bound)
	})
	t.Run("NULL ordering and offset without limit", func(t *testing.T) {
		query := sb.Select(&sb.Column{Name: "value"}).OrderBy(sb.Order{Expr: &sb.Column{Name: "value"}}, sb.Order{Position: 1, Descending: true}, sb.Order{Expr: &sb.Column{Name: "value"}, Nulls: sb.NullsLast}).Offset(2)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, `SELECT "value" ORDER BY "value" ASC NULLS FIRST, 1 DESC NULLS LAST, "value" ASC NULLS LAST OFFSET 2`, text)
		assert.Empty(t, args)
	})
	for _, tc := range []struct{ input, want string }{
		{"SELECT 1.23 AS n", `SELECT CAST($1 AS NUMERIC) AS "n"`},
		{"SELECT 18446744073709551615 AS n", `SELECT CAST($1 AS NUMERIC) AS "n"`},
		{"SELECT 1e2 AS n", `SELECT CAST($1 AS DOUBLE PRECISION) AS "n"`},
		{"SELECT 5/2 AS n", `SELECT (CAST(CAST($1 AS BIGINT) AS NUMERIC) / CAST(CAST($2 AS BIGINT) AS NUMERIC)) AS "n"`},
		{"SELECT IFNULL(a,'x') AS n", `SELECT COALESCE("a", $1) AS "n"`},
		{"SELECT CONCAT(a,NULL) AS n", `SELECT (CAST("a" AS TEXT) || CAST($1 AS TEXT)) AS "n"`},
		{"SELECT LENGTH(a) AS n", `SELECT OCTET_LENGTH("a") AS "n"`},
		{"SELECT OCTET_LENGTH(a) AS n", `SELECT OCTET_LENGTH("a") AS "n"`},
		{"SELECT NOW(6) AS n", `SELECT LOCALTIMESTAMP(6) AS "n"`},
		{"SELECT CURDATE() AS n", `SELECT CURRENT_DATE AS "n"`},
		{"SELECT CAST(a AS SIGNED) AS n", `SELECT CAST("a" AS BIGINT) AS "n"`},
		{"SELECT a AS k,SUM(b) AS n FROM {{r}} GROUP BY a HAVING n>1 ORDER BY k", `SELECT "a" AS "k", SUM("b") AS "n" FROM "s"."t" GROUP BY "a" HAVING (SUM("b") > CAST($1 AS BIGINT)) ORDER BY "k" ASC NULLS FIRST`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			input, want := tc.input, tc.want
			if !strings.Contains(input, " FROM ") {
				input += " FROM {{r}}"
				want += ` FROM "s"."t"`
			}
			query, err := compiler.ParseSQL(input)
			require.NoError(t, err)
			if query.FromPart != nil {
				query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
			}
			text, _, err := ToSql(query)
			require.NoError(t, err)
			assert.Equal(t, want, text)
		})
	}
	t.Run("byte length alias preserves Unicode and NULL bindings", func(t *testing.T) {
		query, err := compiler.ParseSQL("SELECT OCTET_LENGTH('中文') AS bytes,OCTET_LENGTH(NULL) AS absent FROM {{r}}")
		require.NoError(t, err)
		query.FromPart.(*sb.Table).SourceIdentifier = `"s"."t"`
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, `SELECT OCTET_LENGTH($1) AS "bytes", OCTET_LENGTH($2) AS "absent" FROM "s"."t"`, text)
		assert.Equal(t, []any{"中文", nil}, args)
	})
	t.Run("LIKE escape and structured regex remain bound", func(t *testing.T) {
		escape := "!"
		query := sb.Select(&sb.Star{}).Where(&sb.Like{Expr: &sb.Column{Name: "label"}, Pattern: &sb.Param{Value: "a!_%"}, Escape: &escape}, &sb.Regex{Expr: &sb.Column{Name: "label"}, Pattern: &sb.Param{Value: "^a"}})
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, `SELECT * WHERE ("label" LIKE $1 ESCAPE $2) AND ("label" ~ $3)`, text)
		assert.Equal(t, []any{"a!_%", "!", "^a"}, args)
	})
	t.Run("SQL NULL is represented by a non-nil parameter node", func(t *testing.T) {
		text, args, err := ToSql(sb.Select(&sb.Param{Value: nil}))
		require.NoError(t, err)
		assert.Equal(t, "SELECT $1", text)
		assert.Equal(t, []any{nil}, args)
	})
	t.Run("unmapped and malformed nodes fail without partial SQL", func(t *testing.T) {
		for _, node := range []sb.SqlNode{nil, &sb.Cast{Expr: &sb.Param{Value: 1}, Type: sb.CastUnsignedInteger}, &sb.Function{Name: "pg_read_file", Args: []sb.SqlNode{&sb.Param{Value: "secret"}}}, &sb.Function{Name: "CHAR"}, &sb.Function{Name: "CHAR", Args: []sb.SqlNode{&sb.Param{Value: 65}}, Distinct: true}, &sb.In{Expr: &sb.Column{Name: "id"}}, &sb.Column{Name: "bad\x00name"}, &sb.Param{Value: json.Number("1;DROP TABLE t")}, &sb.Cast{Expr: &sb.Param{Value: 1}, Type: sb.CastDecimal, Parameters: []uint64{2, 3}}} {
			text, args, err := ToSql(sb.Select(node))
			require.Error(t, err)
			assert.Empty(t, text)
			assert.Nil(t, args)
		}
		_, _, err := ToSql(nil)
		require.Error(t, err)
	})
}
