// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/postgresql"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/sql/compiler"
)

func TestBindColumns(t *testing.T) {
	for _, test := range []struct {
		name, sql, want string
	}{
		{"physical names preserve public output", "SELECT id FROM {{a}}", "SELECT `order.id` AS `id` FROM `a`"},
		{"explicit aliases retain case", "SELECT a.id AS Mixed FROM {{a}} a", "SELECT `a`.`order.id` AS `Mixed` FROM `a` AS `a`"},
		{"stars preserve public order and COUNT remains a function", "SELECT a.*, COUNT(*) AS n FROM {{a}} a", "SELECT `a`.`order.id` AS `id`, `a`.`native_label` AS `label`, COUNT(*) AS `n` FROM `a` AS `a`"},
		{"view fields do not map to hidden originals", "SELECT v.id FROM {{v}} v", "SELECT `v`.`id` FROM `v` AS `v`"},
		{"subquery exposes public default outputs", "SELECT q.id FROM (SELECT id FROM {{a}}) q", "SELECT `q`.`id` FROM (SELECT `order.id` AS `id` FROM `a`) AS `q`"},
		{"subquery exposes star outputs", "SELECT q.id FROM (SELECT * FROM {{a}}) q", "SELECT `q`.`id` FROM (SELECT `order.id` AS `id`, `native_label` AS `label` FROM `a`) AS `q`"},
		{"self join retains each source instance", "SELECT x.id, y.id AS other FROM {{a}} x JOIN {{a}} y ON x.id=y.id", "SELECT `x`.`order.id` AS `id`, `y`.`order.id` AS `other` FROM `a` AS `x` INNER JOIN `a` AS `y` ON (`x`.`order.id` = `y`.`order.id`)"},
		{"group source wins and having output wins", "SELECT id AS label FROM {{a}} GROUP BY label HAVING label>0 ORDER BY label", "SELECT `_source_1`.`order.id` AS `label` FROM `a` AS `_source_1` GROUP BY `native_label` HAVING (`_source_1`.`order.id` > 0) ORDER BY `_source_1`.`order.id` ASC"},
		{"aggregate arguments use sources instead of output aliases", "SELECT SUM(id) AS id FROM {{a}} HAVING SUM(id)>1 AND id>0 ORDER BY SUM(id)", "SELECT SUM(`_source_1`.`order.id`) AS `id` FROM `a` AS `_source_1` HAVING ((SUM(`order.id`) > 1) AND (SUM(`_source_1`.`order.id`) > 0)) ORDER BY SUM(`order.id`) ASC"},
		{"group falls back to exact output alias", "SELECT id AS Mixed FROM {{a}} GROUP BY Mixed ORDER BY Mixed", "SELECT `_source_1`.`order.id` AS `Mixed` FROM `a` AS `_source_1` GROUP BY `_source_1`.`order.id` ORDER BY `_source_1`.`order.id` ASC"},
		{"group and order positions use expanded star width", "SELECT * FROM {{a}} GROUP BY 2 ORDER BY 2", "SELECT `order.id` AS `id`, `native_label` AS `label` FROM `a` GROUP BY 2 ORDER BY 2 ASC"},
		{"bound expression defaults remain parser names", "SELECT q.`id+1` FROM (SELECT id+1 FROM {{a}}) q", "SELECT `q`.`id+1` FROM (SELECT (`order.id` + 1) AS `id+1` FROM `a`) AS `q`"},
		{"empty schema permits constant projection", "SELECT 1 FROM {{empty}}", "SELECT 1 AS `1` FROM `empty`"},
		{"empty schema permits COUNT star", "SELECT COUNT(*) FROM {{empty}}", "SELECT COUNT(*) AS `COUNT(*)` FROM `empty`"},
		{"zero output alias sorts by its output position", "SELECT 0 AS zero FROM {{a}} ORDER BY zero", "SELECT 0 AS `zero` FROM `a` ORDER BY 1 ASC"},
		{"constant grouping alias is not aggregate ordinal", "SELECT COUNT(*) AS total,1 AS one FROM {{a}} GROUP BY one", "SELECT COUNT(*) AS `total`, 1 AS `one` FROM `a` GROUP BY (1 + 0)"},
		{"constant ordering alias uses its own output position", "SELECT id,1 AS one FROM {{a}} ORDER BY one", "SELECT `order.id` AS `id`, 1 AS `one` FROM `a` ORDER BY 2 ASC"},
		{"UNION only exposes first branch output", "SELECT q.first FROM (SELECT id AS first FROM {{a}} UNION ALL SELECT id AS second FROM {{b}}) q", "SELECT `q`.`first` FROM (SELECT * FROM (SELECT `order.id` AS `first` FROM `a` UNION ALL SELECT `id` AS `second` FROM `b`) AS `_union_result`) AS `q`"},
	} {
		t.Run(test.name, func(t *testing.T) {
			query, err := compiler.ParseSQL(test.sql)
			require.NoError(t, err)
			bindTestTableNames(query)
			resources := columnTestResources()
			before, err := json.Marshal(resources)
			require.NoError(t, err)
			require.NoError(t, BindColumns(query, resources))
			text, args, err := mariadb.ToSql(query)
			require.NoError(t, err)
			assert.Equal(t, test.want, text)
			assert.Empty(t, args)
			after, err := json.Marshal(resources)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after))
		})
	}
	t.Run("DISTINCT literal output aliases keep the selected sort expression", func(t *testing.T) {
		for _, test := range []struct{ sql, want string }{
			{"SELECT DISTINCT 0 AS zero FROM {{a}} ORDER BY zero", `SELECT DISTINCT CAST(0 AS BIGINT) AS "zero" FROM a ORDER BY 1 ASC NULLS FIRST`},
			{"SELECT DISTINCT 1 AS one FROM {{a}} ORDER BY one", `SELECT DISTINCT CAST(1 AS BIGINT) AS "one" FROM a ORDER BY 1 ASC NULLS FIRST`},
			{"SELECT DISTINCT id,1 AS one FROM {{a}} ORDER BY one DESC", `SELECT DISTINCT "order.id" AS "id", CAST(1 AS BIGINT) AS "one" FROM a ORDER BY 2 DESC NULLS LAST`},
			{"SELECT DISTINCT 0 AS id FROM {{a}} ORDER BY id", `SELECT DISTINCT CAST(0 AS BIGINT) AS "id" FROM a ORDER BY 1 ASC NULLS FIRST`},
			{"SELECT DISTINCT 0 AS n,1 AS N FROM {{a}} ORDER BY N,n", `SELECT DISTINCT CAST(0 AS BIGINT) AS "n", CAST(1 AS BIGINT) AS "N" FROM a ORDER BY 2 ASC NULLS FIRST, 1 ASC NULLS FIRST`},
			{"SELECT DISTINCT id,'x' AS k FROM {{a}} ORDER BY k", `SELECT DISTINCT "order.id" AS "id", 'x' AS "k" FROM a ORDER BY 2 ASC NULLS FIRST`},
			{"SELECT DISTINCT NULL AS k FROM {{a}} ORDER BY k", `SELECT DISTINCT NULL AS "k" FROM a ORDER BY 1 ASC NULLS FIRST`},
			{"SELECT DISTINCT TRUE AS k FROM {{a}} ORDER BY k DESC", `SELECT DISTINCT TRUE AS "k" FROM a ORDER BY 1 DESC NULLS LAST`},
			{"SELECT DISTINCT FALSE AS k FROM {{a}} ORDER BY k", `SELECT DISTINCT FALSE AS "k" FROM a ORDER BY 1 ASC NULLS FIRST`},
			{"SELECT DISTINCT 1.5 AS k FROM {{a}} ORDER BY k", `SELECT DISTINCT CAST(1.5 AS NUMERIC) AS "k" FROM a ORDER BY 1 ASC NULLS FIRST`},
			{"SELECT DISTINCT 'x' AS k,NULL AS K FROM {{a}} ORDER BY K,k", `SELECT DISTINCT 'x' AS "k", NULL AS "K" FROM a ORDER BY 2 ASC NULLS FIRST, 1 ASC NULLS FIRST`},
		} {
			t.Run(test.sql, func(t *testing.T) {
				query, err := compiler.ParseSQL(test.sql)
				require.NoError(t, err)
				query.FromPart.(*sb.Table).SourceIdentifier = "a"
				require.NoError(t, BindColumns(query, columnTestResources()))
				text, args, err := postgresql.ToSql(query)
				require.NoError(t, err)
				assert.Equal(t, test.want, text)
				assert.Empty(t, args)
			})
		}
	})

	for _, sql := range []string{
		"SELECT missing FROM {{a}}",
		"SELECT ID FROM {{a}}",
		"SELECT `order.id` FROM {{a}}",
		"SELECT v.hidden_id FROM {{v}} v",
		"SELECT V.id FROM {{v}} v",
		"SELECT V.* FROM {{v}} v",
		"SELECT id AS Alias FROM {{a}} ORDER BY alias",
		"SELECT id AS Alias FROM {{a}} WHERE Alias>0",
		"SELECT id AS Alias FROM {{a}} GROUP BY alias",
		"SELECT id AS Alias FROM {{a}} HAVING alias>0",
		"SELECT q.id FROM (SELECT label FROM {{a}}) q",
		"SELECT q.second FROM (SELECT id AS first FROM {{a}} UNION SELECT id AS second FROM {{b}}) q",
		"SELECT id FROM {{empty}}",
		"SELECT 1 FROM {{a}} WHERE missing IS NULL",
		"SELECT 1 FROM {{a}} GROUP BY missing",
		"SELECT 1 FROM {{a}} HAVING missing>0",
		"SELECT 1 FROM {{a}} ORDER BY missing",
		"SELECT a.id FROM {{a}} a JOIN {{b}} b ON a.id=c.id JOIN {{b}} c ON b.id=c.id",
		"SELECT a.id FROM {{a}} a JOIN ({{b}} b JOIN {{v}} v ON a.id=v.id) ON a.id=b.id",
		"SELECT a.id AS n FROM {{a}} a JOIN {{b}} b ON n=b.id",
		"SELECT CASE WHEN missing>0 THEN id ELSE label END FROM {{a}}",
		"SELECT CASE id WHEN 1 THEN missing ELSE label END FROM {{a}}",
		"SELECT CASE id WHEN 1 THEN label ELSE missing END FROM {{a}}",
		"SELECT CAST(missing AS SIGNED) FROM {{a}}",
		"SELECT ABS(missing) FROM {{a}}",
		"SELECT -missing FROM {{a}}",
		"SELECT id+missing FROM {{a}}",
		"SELECT id FROM {{a}} WHERE NOT (missing=1 OR id<>missing)",
		"SELECT id FROM {{a}} WHERE id IN (1,missing)",
		"SELECT id FROM {{a}} WHERE id BETWEEN 1 AND missing",
		"SELECT id FROM {{a}} WHERE label LIKE missing",
	} {
		t.Run("unknown column: "+sql, func(t *testing.T) {
			query, err := compiler.ParseSQL(sql)
			require.NoError(t, err)
			err = BindColumns(query, columnTestResources())
			var failure *interfaces.SourceQueryInvalidParameterError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, interfaces.SourceQueryInvalidParameterUnknownColumn, failure.Reason)
			detail, invalid := interfaces.RequestSideQueryError(err)
			assert.True(t, invalid)
			assert.Equal(t, "query references an unknown column", detail)
			assert.Nil(t, failure.Cause)
		})
	}

	for _, sql := range []string{
		"SELECT id FROM {{a}} a JOIN {{b}} b ON a.id=b.id",
		"SELECT a.id FROM {{a}} a JOIN {{b}} a ON a.id=a.id",
		"SELECT id AS n, label AS n FROM {{a}} ORDER BY n",
		"SELECT 0 AS n, 1 AS n FROM {{a}} ORDER BY n",
		"SELECT id AS n, label AS n FROM {{a}} GROUP BY n",
		"SELECT id FROM {{a}} ORDER BY 2",
		"SELECT id FROM {{a}} GROUP BY 0",
		"SELECT id FROM {{a}} GROUP BY 2",
		"SELECT * FROM {{empty}}",
		"SELECT * FROM {{a}} UNION SELECT * FROM {{b}}",
	} {
		t.Run("invalid query shape: "+sql, func(t *testing.T) {
			query, err := compiler.ParseSQL(sql)
			require.NoError(t, err)
			err = BindColumns(query, columnTestResources())
			var failure *interfaces.ConditionBuildError
			require.ErrorAs(t, err, &failure)
			assert.NotContains(t, err.Error(), "order.id")
			assert.NotContains(t, err.Error(), "hidden_id")
		})
	}

	t.Run("all structured expression children are checked", func(t *testing.T) {
		for _, expression := range []sb.SqlNode{
			&sb.Regex{Expr: &sb.Column{Name: "label"}, Pattern: &sb.Column{Name: "missing"}},
			&sb.Contains{Expr: &sb.Column{Name: "label"}, Value: &sb.Column{Name: "missing"}},
			&sb.Interval{Value: &sb.Column{Name: "missing"}, Unit: sb.IntervalDay},
		} {
			query := sb.Select(expression).From(&sb.Table{ResourceID: "a"})
			var failure *interfaces.SourceQueryInvalidParameterError
			require.ErrorAs(t, BindColumns(query, columnTestResources()), &failure)
		}
	})
	t.Run("nil query and missing resource metadata are safe input failures", func(t *testing.T) {
		for _, query := range []*sb.SelectBuilder{nil, sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{ResourceID: "missing"})} {
			var failure *interfaces.ConditionBuildError
			require.ErrorAs(t, BindColumns(query, columnTestResources()), &failure)
		}
	})
	t.Run("distinct public names sharing a native name stay qualified", func(t *testing.T) {
		resources := columnTestResources()
		resources["a"].SchemaDefinition = []*interfaces.Property{{Name: "first", OriginalName: "id"}}
		resources["b"].SchemaDefinition = []*interfaces.Property{{Name: "second", OriginalName: "id"}}
		query, err := compiler.ParseSQL("SELECT first, second FROM {{a}} a CROSS JOIN {{b}} b WHERE first=second")
		require.NoError(t, err)
		bindTestTableNames(query)
		require.NoError(t, BindColumns(query, resources))
		text, _, err := mariadb.ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `a`.`id` AS `first`, `b`.`id` AS `second` FROM `a` AS `a` CROSS JOIN `b` AS `b` WHERE (`a`.`id` = `b`.`id`)", text)
	})
	t.Run("swapped output aliases keep source meaning with or without metadata names", func(t *testing.T) {
		for _, sourceName := range []string{"", "a"} {
			t.Run(sourceName, func(t *testing.T) {
				resources := columnTestResources()
				resources["a"].SchemaDefinition = []*interfaces.Property{{Name: "id"}, {Name: "label"}}
				query, err := compiler.ParseSQL("SELECT id AS label,label AS id FROM {{a}} ORDER BY label")
				require.NoError(t, err)
				bindTestTableNames(query)
				query.FromPart.(*sb.Table).SourceName = sourceName
				require.NoError(t, BindColumns(query, resources))
				text, _, err := mariadb.ToSql(query)
				require.NoError(t, err)
				if sourceName == "" {
					assert.Equal(t, "SELECT `_source_1`.`id` AS `label`, `label` AS `id` FROM `a` AS `_source_1` ORDER BY `_source_1`.`id` ASC", text)
				} else {
					assert.Equal(t, "SELECT `a`.`id` AS `label`, `label` AS `id` FROM `a` ORDER BY `a`.`id` ASC", text)
				}
			})
		}
	})
	t.Run("native and public names cannot redirect expanded alias ordering", func(t *testing.T) {
		resources := columnTestResources()
		resources["a"].SchemaDefinition = []*interfaces.Property{{Name: "id", OriginalName: "native_id"}, {Name: "label", OriginalName: "id"}}
		query, err := compiler.ParseSQL("SELECT id,label FROM {{a}} ORDER BY label")
		require.NoError(t, err)
		bindTestTableNames(query)
		require.NoError(t, BindColumns(query, resources))
		text, _, err := mariadb.ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `native_id` AS `id`, `_source_1`.`id` AS `label` FROM `a` AS `_source_1` ORDER BY `_source_1`.`id` ASC", text)
	})
	t.Run("source fallback in ordering cannot resolve to another output alias", func(t *testing.T) {
		resources := columnTestResources()
		resources["a"].SchemaDefinition = []*interfaces.Property{{Name: "id", OriginalName: "native_id"}, {Name: "label", OriginalName: "id"}}
		query, err := compiler.ParseSQL("SELECT label AS native_id FROM {{a}} HAVING id>0 ORDER BY id")
		require.NoError(t, err)
		bindTestTableNames(query)
		require.NoError(t, BindColumns(query, resources))
		text, _, err := mariadb.ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `id` AS `native_id` FROM `a` AS `_source_1` HAVING (`_source_1`.`native_id` > 0) ORDER BY `_source_1`.`native_id` ASC", text)
	})
	t.Run("missing physical names in multi-source queries get distinct safe aliases", func(t *testing.T) {
		for _, projection := range []string{"first,second", "*"} {
			resources := columnTestResources()
			resources["a"].SchemaDefinition = []*interfaces.Property{{Name: "first", OriginalName: "id"}}
			resources["b"].SchemaDefinition = []*interfaces.Property{{Name: "second", OriginalName: "id"}}
			query, err := compiler.ParseSQL("SELECT " + projection + " FROM {{a}} CROSS JOIN {{b}} WHERE first=second")
			require.NoError(t, err)
			bindTestTableNames(query)
			require.NoError(t, BindColumns(query, resources))
			text, _, err := mariadb.ToSql(query)
			require.NoError(t, err)
			assert.Equal(t, "SELECT `_source_1`.`id` AS `first`, `_source_2`.`id` AS `second` FROM `a` AS `_source_1` CROSS JOIN `b` AS `_source_2` WHERE (`_source_1`.`id` = `_source_2`.`id`)", text)
		}
	})
	t.Run("generated aliases conservatively avoid source database case collisions", func(t *testing.T) {
		resources := columnTestResources()
		resources["a"].SchemaDefinition = []*interfaces.Property{{Name: "first", OriginalName: "id"}}
		resources["b"].SchemaDefinition = []*interfaces.Property{{Name: "second", OriginalName: "id"}}
		query, err := compiler.ParseSQL("SELECT first,second FROM {{a}} CROSS JOIN {{b}} _SOURCE_1")
		require.NoError(t, err)
		bindTestTableNames(query)
		require.NoError(t, BindColumns(query, resources))
		text, _, err := mariadb.ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `_source_2`.`id` AS `first`, `_SOURCE_1`.`id` AS `second` FROM `a` AS `_source_2` CROSS JOIN `b` AS `_SOURCE_1`", text)
	})
	t.Run("database case folding cannot change exact output and source binding", func(t *testing.T) {
		for _, test := range []struct{ sql, want string }{
			{"SELECT label AS ID FROM {{a}} HAVING id>0 ORDER BY id", "SELECT `label` AS `ID` FROM `a` AS `_source_1` HAVING (`_source_1`.`id` > 0) ORDER BY `_source_1`.`id` ASC"},
			{"SELECT id,label AS ID FROM {{a}} HAVING id>0 ORDER BY id", "SELECT `_source_1`.`id`, `label` AS `ID` FROM `a` AS `_source_1` HAVING (`_source_1`.`id` > 0) ORDER BY `_source_1`.`id` ASC"},
		} {
			resources := columnTestResources()
			resources["a"].SchemaDefinition = []*interfaces.Property{{Name: "id"}, {Name: "label"}}
			query, err := compiler.ParseSQL(test.sql)
			require.NoError(t, err)
			bindTestTableNames(query)
			require.NoError(t, BindColumns(query, resources))
			text, _, err := mariadb.ToSql(query)
			require.NoError(t, err)
			assert.Equal(t, test.want, text)
		}
	})
}

func columnTestResources() map[string]*interfaces.Resource {
	return map[string]*interfaces.Resource{
		"a": {ID: "a", Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{
			{Name: "id", OriginalName: "order.id"}, {Name: "label", OriginalName: "native_label"},
		}},
		"b":     {ID: "b", Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
		"v":     {ID: "v", Category: interfaces.ResourceCategoryLogicalView, SchemaDefinition: []*interfaces.Property{{Name: "id", OriginalName: "hidden_id"}}},
		"empty": {ID: "empty", Category: interfaces.ResourceCategoryTable},
	}
}

func bindTestTableNames(node sb.SqlNode) {
	switch value := node.(type) {
	case *sb.Table:
		value.SourceIdentifier = "`" + value.ResourceID + "`"
	case *sb.SelectBuilder:
		bindTestTableNames(value.FromPart)
		for _, join := range value.JoinsPart {
			bindTestTableNames(join.Source)
		}
	case *sb.Subquery:
		bindTestTableNames(value.Query)
	case *sb.Union:
		bindTestTableNames(value.Left)
		bindTestTableNames(value.Right)
	case *sb.JoinedTable:
		bindTestTableNames(value.From)
		for _, join := range value.Joins {
			bindTestTableNames(join.Source)
		}
	}
}
