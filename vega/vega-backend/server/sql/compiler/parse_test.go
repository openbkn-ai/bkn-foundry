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

func TestParseSQL(t *testing.T) {
	t.Run("Cypher OPTIONAL MATCH SQL parses without rewriting its join scope", func(t *testing.T) {
		input := "SELECT t0.`f_uid` AS `a.employee_uid`, t1.`f_uid` AS `b.project_uid` FROM {{res_employee}} t0 " +
			"LEFT JOIN ({{res_member}} t2 JOIN {{res_project}} t1 ON t2.`project_code` = t1.`f_code` AND t2.`project_org_code` = t1.`f_org`) " +
			"ON t2.`employee_code` = t0.`f_code` AND t2.`employee_org_code` = t0.`f_org` AND t1.`f_org` = 'ORG1'"
		query, err := ParseSQL(input)
		require.NoError(t, err)
		assert.Equal(t, "res_employee", query.FromPart.(*sb.Table).ResourceID)
		group := query.JoinsPart[0].Source.(*sb.JoinedTable)
		assert.Equal(t, "res_member", group.From.(*sb.Table).ResourceID)
		assert.Equal(t, "res_project", group.Joins[0].Source.(*sb.Table).ResourceID)
	})
	t.Run("optional indirect relation preserves the parenthesized join group", func(t *testing.T) {
		query, err := ParseSQL("SELECT a.id,b.id AS matched FROM {{employees}} a LEFT JOIN ({{members}} m JOIN {{projects}} b ON m.project_id=b.id AND b.label='inner') ON a.id=m.employee_id AND a.label='outer' WHERE b.id IS NULL")
		require.NoError(t, err)
		require.Len(t, query.JoinsPart, 1)
		assert.Equal(t, sb.LeftJoin, query.JoinsPart[0].Kind)
		group, ok := query.JoinsPart[0].Source.(*sb.JoinedTable)
		require.True(t, ok)
		require.Len(t, group.Joins, 1)
		assert.Equal(t, sb.InnerJoin, group.Joins[0].Kind)
		for _, node := range []sb.SqlNode{query.FromPart, group.From, group.Joins[0].Source} {
			table := node.(*sb.Table)
			table.SourceIdentifier = "`" + table.ResourceID + "`"
		}
		text, args, err := mysql.ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `a`.`id`, `b`.`id` AS `matched` FROM `employees` AS `a` LEFT JOIN (`members` AS `m` INNER JOIN `projects` AS `b` ON ((`m`.`project_id` = `b`.`id`) AND (`b`.`label` = 'inner'))) ON ((`a`.`id` = `m`.`employee_id`) AND (`a`.`label` = 'outer')) WHERE (`b`.`id` IS NULL)", text)
		assert.Equal(t, []any(nil), args)
	})
	t.Run("resource join and clauses map to the new builder", func(t *testing.T) {
		query, err := ParseSQL("SELECT DISTINCT a.id AS aid, COUNT(*) AS n FROM {{ orders }} a LEFT JOIN {{orders}} b ON a.id=b.id WHERE a.id IN (1,2) GROUP BY a.id HAVING COUNT(*)>1 ORDER BY a.id DESC LIMIT 2,10;")
		require.NoError(t, err)
		left := query.FromPart.(*sb.Table)
		right := query.JoinsPart[0].Source.(*sb.Table)
		assert.Equal(t, "orders", left.ResourceID)
		assert.Equal(t, "orders", right.ResourceID)
		assert.Equal(t, "a", left.Alias)
		assert.Equal(t, "b", right.Alias)
		assert.NotSame(t, left, right)
		assert.Empty(t, left.SourceIdentifier)
		left.SourceIdentifier = "`sales`.`orders`"
		right.SourceIdentifier = "`sales`.`orders`"
		text, args, err := mysql.ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT DISTINCT `a`.`id` AS `aid`, COUNT(*) AS `n` FROM `sales`.`orders` AS `a` LEFT JOIN `sales`.`orders` AS `b` ON (`a`.`id` = `b`.`id`) WHERE (`a`.`id` IN (1, 2)) GROUP BY `a`.`id` HAVING (COUNT(*) > 1) ORDER BY `a`.`id` DESC LIMIT 10 OFFSET 2", text)
		assert.Equal(t, []any(nil), args)
	})
	t.Run("subquery limit and parameter order remain explicit", func(t *testing.T) {
		query, err := ParseSQL("SELECT 9 AS marker, q.id FROM (SELECT id FROM {{items}} WHERE n=1 ORDER BY id LIMIT 2) q WHERE q.id>3 LIMIT 4 OFFSET 1")
		require.NoError(t, err)
		inner := query.FromPart.(*sb.Subquery).Query
		assert.Equal(t, uint64(2), *inner.LimitPart)
		inner.FromPart.(*sb.Table).SourceIdentifier = "`items`"
		text, args, err := mysql.ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT 9 AS `marker`, `q`.`id` FROM (SELECT `id` FROM `items` WHERE (`n` = 1) ORDER BY `id` ASC LIMIT 2) AS `q` WHERE (`q`.`id` > 3) LIMIT 4 OFFSET 1", text)
		assert.Equal(t, []any(nil), args)
	})
	t.Run("quoted names strings and exact numbers retain their values", func(t *testing.T) {
		query, err := ParseSQL("SELECT `a`.`x``y.z` AS `结果`, 18446744073709551615, 001.23000000000000000001, 1e-100, 'O''Reilly', \"a\\n\\%\\_\\'\\\\\", TRUE, FALSE, NULL FROM {{x}} `a`")
		require.NoError(t, err)
		query.FromPart.(*sb.Table).SourceIdentifier = "`table`"
		text, args, err := mysql.ToSql(query)
		require.NoError(t, err)
		assert.Contains(t, text, "`a`.`x``y.z` AS `结果`")
		assert.Equal(t, []any(nil), args)
		_, err = json.Marshal(args)
		require.NoError(t, err)
	})
	t.Run("comments and zero limits", func(t *testing.T) {
		query, err := ParseSQL("/* SELECT is still one statement */ SELECT x.* FROM {{abc_1-2}} x # comment\n WHERE TRUE -- end\n LIMIT 0")
		require.NoError(t, err)
		assert.Equal(t, uint64(0), *query.LimitPart)
		assert.Equal(t, "abc_1-2", query.FromPart.(*sb.Table).ResourceID)
		assert.Equal(t, "x", query.ColumnsPart[0].(*sb.Star).Qualifier)
	})
	t.Run("malformed or unopened syntax never returns a partial builder", func(t *testing.T) {
		for _, sql := range []string{"", "SELECT id FROM ordinary_table", "SELECT id FROM {{x}}; SELECT id FROM {{x}}", "DELETE FROM {{x}}", "SELECT id FROM {{X}}", "SELECT id FROM {{x}} UNION SELECT id FROM {{x}}", "SELECT id FROM {{x}}; trailing", "SELECT /*+ hint */ id FROM {{x}}", "SELECT /*! hidden */ id FROM {{x}}", "SELECT 'unclosed FROM {{x}}", "SELECT id FROM {{x}} WHERE id IN ()", "SELECT id FROM {{x}} LIMIT 1.0", "SELECT id FROM {{x}} LIMIT 18446744073709551616", "SELECT id FROM {{x}} WHERE id LIKE 'x' ESCAPE ''", "SELECT id FROM {{x}} WHERE id LIKE 'x' ESCAPE 'ab'", "SELECT CAST(id AS DECIMAL(1e2)) FROM {{x}}", "SELECT id FROM {{" + strings.Repeat("a", 41) + "}}", string([]byte{0xff})} {
			t.Run(sql, func(t *testing.T) {
				query, err := ParseSQL(sql)
				require.Error(t, err)
				assert.Nil(t, query)
				var parseError *ParseError
				require.ErrorAs(t, err, &parseError)
				assert.Positive(t, parseError.Line)
				assert.Positive(t, parseError.Column)
			})
		}
	})
	t.Run("diagnostics contain locations without echoing SQL", func(t *testing.T) {
		query, err := ParseSQL("SELECT id\nFROM {{x}} WHERE password='sensitive-value' AND")
		assert.Nil(t, query)
		var parseError *ParseError
		require.ErrorAs(t, err, &parseError)
		assert.Equal(t, 2, parseError.Line)
		assert.NotContains(t, err.Error(), "sensitive-value")
	})
	t.Run("input budgets reject before returning a query", func(t *testing.T) {
		for _, sql := range []string{strings.Repeat(" ", maxSQLBytes+1), "SELECT id FROM " + strings.Repeat("(", maxDepth+1) + "{{x}}" + strings.Repeat(")", maxDepth+1), strings.Repeat("/*x*/ ", maxTokens+1) + "SELECT id FROM {{x}}", "SELECT " + strings.Repeat("(", maxDepth+1) + "id" + strings.Repeat(")", maxDepth+1) + " FROM {{x}}", "SELECT " + strings.Repeat("NOT ", maxDepth+1) + "id FROM {{x}}", "SELECT " + strings.Repeat("id+", maxDepth+1) + "id FROM {{x}}", "SELECT id FROM {{x}} WHERE id IN (" + strings.Repeat("1,", maxINValues) + "1)"} {
			query, err := ParseSQL(sql)
			assert.Nil(t, query)
			var parseError *ParseError
			require.ErrorAs(t, err, &parseError)
			assert.Equal(t, CodeLimit, parseError.Code)
		}
	})
}

func TestResourceIDs(t *testing.T) {
	ids, err := ResourceIDs("SELECT '{{fake}}', `{{quoted}}` FROM {{ orders }} a JOIN {{orders}} b ON a.id=b.id JOIN {{other}} c ON b.id=c.id /* {{hidden}} */ # {{line}}\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"orders", "other"}, ids)
	_, err = ResourceIDs(strings.Repeat("x", maxSQLBytes+1))
	require.Error(t, err)
	_, err = ResourceIDs("SELECT * FROM {{" + strings.Repeat("a", 41) + "}}")
	require.Error(t, err)
}
