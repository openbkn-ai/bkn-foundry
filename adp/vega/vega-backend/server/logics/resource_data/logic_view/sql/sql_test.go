// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package sql

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func TestLogicViewSQLBuildLogicDefinitionSQL(t *testing.T) {
	t.Run("resource and output nodes build projected sql", func(t *testing.T) {
		view := testSQLView()
		generator := NewlogicDefinitionSQLGenerator(view)

		got, err := generator.BuildLogicDefinitionSQL(context.Background(), view)

		require.NoError(t, err)
		assert.Equal(t, "SELECT `id`, `display_name` FROM (SELECT `id`, `name` AS `display_name` FROM {{resource-1}}) AS output", got)
	})

	t.Run("returns error for missing output node", func(t *testing.T) {
		view := testSQLView()
		view.LogicDefinition = []*interfaces.LogicDefinitionNode{
			testSQLResourceNode(),
		}
		generator := NewlogicDefinitionSQLGenerator(view)

		got, err := generator.BuildLogicDefinitionSQL(context.Background(), view)

		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "output node not found")
	})

	t.Run("returns error for output without input", func(t *testing.T) {
		view := testSQLView()
		view.LogicDefinition = []*interfaces.LogicDefinitionNode{
			{ID: "output", Type: interfaces.LogicDefinitionNodeType_Output},
		}
		generator := NewlogicDefinitionSQLGenerator(view)

		got, err := generator.BuildLogicDefinitionSQL(context.Background(), view)

		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "output node has no input")
	})
}

func TestLogicViewSQLBuildNodeSQL(t *testing.T) {
	t.Run("join node builds join sql", func(t *testing.T) {
		view := testSQLView()
		view.RefResources["resource-2"] = &interfaces.Resource{
			ID: "resource-2",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", OriginalName: "id", Type: interfaces.DataType_String},
				{Name: "score", OriginalName: "score", Type: interfaces.DataType_Integer},
			},
		}
		view.LogicDefinition = []*interfaces.LogicDefinitionNode{
			testSQLResourceNode(),
			{
				ID:           "res-2",
				Type:         interfaces.LogicDefinitionNodeType_Resource,
				Config:       map[string]any{"resource_id": "resource-2"},
				OutputFields: []*interfaces.ViewProperty{{Property: interfaces.Property{Name: "id"}}, {Property: interfaces.Property{Name: "score"}}},
			},
			{
				ID:     "join",
				Type:   interfaces.LogicDefinitionNodeType_Join,
				Inputs: []string{"res-1", "res-2"},
				Config: map[string]any{
					"join_type": interfaces.JoinType_Left,
					"join_on": []map[string]any{
						{"left_field": "id", "right_field": "id"},
					},
				},
				OutputFields: []*interfaces.ViewProperty{
					{Property: interfaces.Property{Name: "id"}, From: "id", FromNode: "res-1"},
					{Property: interfaces.Property{Name: "score"}, From: "score", FromNode: "res-2"},
				},
			},
			{ID: "output", Type: interfaces.LogicDefinitionNodeType_Output, Inputs: []string{"join"}},
		}
		generator := NewlogicDefinitionSQLGenerator(view)

		got, err := generator.BuildLogicDefinitionSQL(context.Background(), view)

		require.NoError(t, err)
		assert.Contains(t, got, "LEFT JOIN")
		assert.Contains(t, got, "l.`id` = r.`id`")
		assert.Contains(t, got, "l.`id` AS `id`")
		assert.Contains(t, got, "r.`score` AS `score`")
	})

	t.Run("union node builds union all sql", func(t *testing.T) {
		view := testSQLView()
		view.RefResources["resource-2"] = &interfaces.Resource{
			ID: "resource-2",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", OriginalName: "id", Type: interfaces.DataType_String},
				{Name: "name", OriginalName: "name", Type: interfaces.DataType_String},
			},
		}
		view.LogicDefinition = []*interfaces.LogicDefinitionNode{
			testSQLResourceNode(),
			{
				ID:           "res-2",
				Type:         interfaces.LogicDefinitionNodeType_Resource,
				Config:       map[string]any{"resource_id": "resource-2"},
				OutputFields: []*interfaces.ViewProperty{{Property: interfaces.Property{Name: "id"}}, {Property: interfaces.Property{Name: "display_name", OriginalName: "name"}}},
			},
			{
				ID:     "union",
				Type:   interfaces.LogicDefinitionNodeType_Union,
				Inputs: []string{"res-1", "res-2"},
				Config: map[string]any{"union_type": interfaces.UnionType_All},
				OutputFields: []*interfaces.ViewProperty{
					{Property: interfaces.Property{Name: "id"}, FromList: []*interfaces.OutputFieldRef{{FromNode: "res-1", From: "id"}, {FromNode: "res-2", From: "id"}}},
					{Property: interfaces.Property{Name: "display_name"}, FromList: []*interfaces.OutputFieldRef{{FromNode: "res-1", From: "display_name"}, {FromNode: "res-2", From: "display_name"}}},
				},
			},
			{ID: "output", Type: interfaces.LogicDefinitionNodeType_Output, Inputs: []string{"union"}},
		}
		generator := NewlogicDefinitionSQLGenerator(view)

		got, err := generator.BuildLogicDefinitionSQL(context.Background(), view)

		require.NoError(t, err)
		assert.Contains(t, got, "UNION ALL")
		assert.Contains(t, got, "AS union_final")
	})

	t.Run("sql node renders template helpers and trims semicolon", func(t *testing.T) {
		view := testSQLView()
		view.LogicDefinition = []*interfaces.LogicDefinitionNode{
			testSQLResourceNode(),
			{
				ID:     "sql-node",
				Type:   interfaces.LogicDefinitionNodeType_Sql,
				Inputs: []string{"res-1"},
				Config: map[string]any{
					"sql": "SELECT * FROM {{ node \"res-1\" }} WHERE {{ nodeAlias \"res-1\" }}.`id` IS NOT NULL;;",
				},
				OutputFields: []*interfaces.ViewProperty{{Property: interfaces.Property{Name: "id"}}},
			},
			{ID: "output", Type: interfaces.LogicDefinitionNodeType_Output, Inputs: []string{"sql-node"}},
		}
		generator := NewlogicDefinitionSQLGenerator(view)

		got, err := generator.BuildLogicDefinitionSQL(context.Background(), view)

		require.NoError(t, err)
		assert.Contains(t, got, "SELECT * FROM (SELECT")
		assert.NotContains(t, strings.TrimSpace(got), ";;")
	})
}

func TestLogicViewSQLHelpers(t *testing.T) {
	t.Run("interpolate formats args", func(t *testing.T) {
		generator := NewlogicDefinitionSQLGenerator(testSQLView())

		got, err := generator.interpolate("a = ? AND b = ? AND c = ? AND d = ?", []any{"O'Reilly", 12, true, nil})

		require.NoError(t, err)
		assert.Equal(t, "a = 'O''Reilly' AND b = 12 AND c = 1 AND d = NULL", got)
	})

	t.Run("interpolate rejects placeholder mismatch", func(t *testing.T) {
		generator := NewlogicDefinitionSQLGenerator(testSQLView())

		got, err := generator.interpolate("a = ?", []any{1, 2})

		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "placeholder count")
	})

	t.Run("limit helpers ignore comments and existing limits", func(t *testing.T) {
		assert.True(t, HasLimit("select * from t -- comment\n limit 10"))
		assert.True(t, HasLimit("select * from t /* comment */ limit 10 offset 20"))
		assert.Equal(t, "select * from t LIMIT 5", AddLimitIfMissing("select * from t;", 5))
		assert.Equal(t, "select * from t limit 10", AddLimitIfMissing("select * from t limit 10", 5))
	})

	t.Run("sql builder inserts filters before order and limit", func(t *testing.T) {
		generator := NewlogicDefinitionSQLGenerator(testSQLView())
		builder := generator.NewSQLBuilder("SELECT * FROM t ORDER BY name LIMIT 10")

		got := builder.AddWhere("age > 18").Build()

		assert.Equal(t, "SELECT * FROM t WHERE age > 18  ORDER BY name LIMIT 10", got)
	})

	t.Run("sort params quote fields", func(t *testing.T) {
		got := buildSQLSortParams([]*interfaces.SortField{
			{Field: "name", Direction: interfaces.ASC_DIRECTION},
			{Field: "age", Direction: interfaces.DESC_DIRECTION},
		})

		assert.Equal(t, "`name` asc, `age` desc", got)
	})
}

func TestLogicViewSQLBuilderAdditional(t *testing.T) {
	generator := NewlogicDefinitionSQLGenerator(testSQLView())

	t.Run("add wheres ignores blank and string builds query", func(t *testing.T) {
		builder := generator.NewSQLBuilder("SELECT * FROM users")

		got := builder.AddWheres([]string{"age > 18", " ", "name IS NOT NULL"}).String()

		assert.Equal(t, "SELECT * FROM users WHERE age > 18 AND name IS NOT NULL", got)
	})

	t.Run("existing where appends before group by", func(t *testing.T) {
		builder := generator.NewSQLBuilder("SELECT age, COUNT(*) FROM users WHERE active = 1 GROUP BY age")

		got := builder.AddWhere("age > 18").Build()

		assert.Equal(t, "SELECT age, COUNT(*) FROM users WHERE active = 1 AND age > 18  GROUP BY age", got)
	})

	t.Run("condition inserts before having", func(t *testing.T) {
		builder := generator.NewSQLBuilder("SELECT age, COUNT(*) FROM users GROUP BY age HAVING COUNT(*) > 1")

		got := builder.AddWhere("age > 18").Build()

		assert.Equal(t, "SELECT age, COUNT(*) FROM users WHERE age > 18  GROUP BY age HAVING COUNT(*) > 1", got)
	})

	t.Run("sub query without alias wraps with default alias", func(t *testing.T) {
		builder := generator.NewSQLBuilder("(SELECT * FROM users)")

		got := builder.AddWhere("age > 18").Build()

		assert.Equal(t, "((SELECT * FROM users)) AS subquery WHERE age > 18", got)
		assert.False(t, builder.hasAlias())
	})

	t.Run("sub query with alias keeps alias", func(t *testing.T) {
		builder := generator.NewSQLBuilder("(SELECT * FROM users) AS u")

		got := builder.AddWhere("age > 18").Build()

		assert.Equal(t, "(SELECT * FROM users) AS u WHERE age > 18", got)
		assert.True(t, builder.hasAlias())
	})

	t.Run("order by and limit append when missing", func(t *testing.T) {
		builder := generator.NewSQLBuilder("SELECT * FROM users")

		got := builder.OrderBy("`age` desc").Limit(5).Build()

		assert.Equal(t, "SELECT * FROM users ORDER BY `age` desc LIMIT 5", got)
	})

	t.Run("limit does not duplicate existing limit", func(t *testing.T) {
		builder := generator.NewSQLBuilder("SELECT * FROM users LIMIT 10")

		got := builder.Limit(5).Build()

		assert.Equal(t, "SELECT * FROM users LIMIT 10", got)
	})
}

func TestLogicViewSQLBuilderApplyParams(t *testing.T) {
	generator := NewlogicDefinitionSQLGenerator(testSQLView())

	t.Run("applies filter sort and standard limit", func(t *testing.T) {
		view := testSQLView()
		builder := generator.NewSQLBuilder("SELECT * FROM users")

		err := builder.ApplyParams(context.Background(), &interfaces.ResourceDataQueryParams{
			FilterCondCfg: &interfaces.FilterCondCfg{
				Name:      "age",
				Operation: filter_condition.OperationGt,
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Const,
					Value:     18,
				},
			},
			Sort:      []*interfaces.SortField{{Field: "age", Direction: interfaces.DESC_DIRECTION}},
			QueryType: interfaces.QueryType_Standard,
			Limit:     20,
		}, view)

		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM users WHERE `age` > 18 ORDER BY `age` desc LIMIT 20", builder.Build())
	})

	t.Run("stream query skips limit", func(t *testing.T) {
		view := testSQLView()
		builder := generator.NewSQLBuilder("SELECT * FROM users")

		err := builder.ApplyParams(context.Background(), &interfaces.ResourceDataQueryParams{
			QueryType: interfaces.QueryType_Stream,
			Limit:     20,
		}, view)

		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM users", builder.Build())
	})

	t.Run("invalid filter returns error", func(t *testing.T) {
		view := testSQLView()
		builder := generator.NewSQLBuilder("SELECT * FROM users")

		err := builder.ApplyParams(context.Background(), &interfaces.ResourceDataQueryParams{
			FilterCondCfg: &interfaces.FilterCondCfg{
				Name:      "missing",
				Operation: filter_condition.OperationEqual,
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Const,
					Value:     1,
				},
			},
		}, view)

		require.Error(t, err)
		assert.ErrorContains(t, err, "not found")
	})
}

func TestLogicViewSQLConvertFilterCondition(t *testing.T) {
	fields := testSQLFieldMap()
	generator := NewlogicDefinitionSQLGenerator(testSQLView())

	t.Run("equal condition converts to sqlizer", func(t *testing.T) {
		cond := mustSQLCondition(t, &interfaces.FilterCondCfg{
			Name:      "age",
			Operation: filter_condition.OperationEqual,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     18,
			},
		}, fields)

		sqlizer, err := generator.ConvertFilterCondition(context.Background(), cond, fields)
		require.NoError(t, err)
		sqlText, args, err := sqlizer.ToSql()

		require.NoError(t, err)
		assert.Equal(t, "`age` = ?", sqlText)
		assert.Equal(t, []any{18}, args)
	})

	t.Run("like escapes special chars", func(t *testing.T) {
		cond := mustSQLCondition(t, &interfaces.FilterCondCfg{
			Name:      "name",
			Operation: filter_condition.OperationLike,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "a_%'b",
			},
		}, fields)

		sqlizer, err := generator.ConvertFilterCondition(context.Background(), cond, fields)
		require.NoError(t, err)
		sqlText, args, err := sqlizer.ToSql()

		require.NoError(t, err)
		assert.Equal(t, "`name` LIKE ?", sqlText)
		assert.Equal(t, []any{`%a\_\%\'b%`}, args)
	})
}

func TestLogicViewSQLConvertFilterConditionAdditional(t *testing.T) {
	fields := testSQLConditionFieldMap()
	generator := NewlogicDefinitionSQLGenerator(testSQLView())

	tests := []struct {
		name    string
		cfg     *interfaces.FilterCondCfg
		wantSQL string
		wantArg []any
	}{
		{name: "not equal const", cfg: sqlConditionCfg("age", filter_condition.OperationNotEqual, interfaces.ValueFrom_Const, 18), wantSQL: "`age` <> ?", wantArg: []any{18}},
		{name: "gte field", cfg: sqlConditionCfg("age", filter_condition.OperationGte, interfaces.ValueFrom_Field, "score"), wantSQL: "`age` >= `score`"},
		{name: "in const slice", cfg: sqlConditionCfg("name", filter_condition.OperationIn, interfaces.ValueFrom_Const, []any{"alice", "bob"}), wantSQL: "`name` IN (?,?)", wantArg: []any{"alice", "bob"}},
		{name: "not in const slice", cfg: sqlConditionCfg("name", filter_condition.OperationNotIn, interfaces.ValueFrom_Const, []any{"alice", "bob"}), wantSQL: "`name` NOT IN (?,?)", wantArg: []any{"alice", "bob"}},
		{name: "not like escapes special chars", cfg: sqlConditionCfg("name", filter_condition.OperationNotLike, interfaces.ValueFrom_Const, "a_%"), wantSQL: "`name` NOT LIKE ?", wantArg: []any{`%a\_\%%`}},
		{name: "contain values", cfg: sqlConditionCfg("tags", filter_condition.OperationContain, interfaces.ValueFrom_Const, []any{"core", "pii"}), wantSQL: "(FIND_IN_SET(?, `tags`) > 0 AND FIND_IN_SET(?, `tags`) > 0)", wantArg: []any{"core", "pii"}},
		{name: "not contain values", cfg: sqlConditionCfg("tags", filter_condition.OperationNotContain, interfaces.ValueFrom_Const, []any{"core", "pii"}), wantSQL: "(FIND_IN_SET(?, `tags`) = 0 OR FIND_IN_SET(?, `tags`) = 0)", wantArg: []any{"core", "pii"}},
		{name: "range values", cfg: sqlConditionCfg("age", filter_condition.OperationRange, interfaces.ValueFrom_Const, []any{18, 30}), wantSQL: "(`age` >= ? AND `age` <= ?)", wantArg: []any{18, 30}},
		{name: "out range values", cfg: sqlConditionCfg("age", filter_condition.OperationOutRange, interfaces.ValueFrom_Const, []any{18, 30}), wantSQL: "(`age` < ? OR `age` > ?)", wantArg: []any{18, 30}},
		{name: "null", cfg: sqlConditionCfg("name", filter_condition.OperationNull, interfaces.ValueFrom_Const, nil), wantSQL: "`name` IS NULL"},
		{name: "not null", cfg: sqlConditionCfg("name", filter_condition.OperationNotNull, interfaces.ValueFrom_Const, nil), wantSQL: "`name` IS NOT NULL"},
		{name: "empty", cfg: sqlConditionCfg("name", filter_condition.OperationEmpty, interfaces.ValueFrom_Const, nil), wantSQL: "`name` = ?", wantArg: []any{""}},
		{name: "not empty", cfg: sqlConditionCfg("name", filter_condition.OperationNotEmpty, interfaces.ValueFrom_Const, nil), wantSQL: "`name` <> ?", wantArg: []any{""}},
		{name: "prefix", cfg: sqlConditionCfg("name", filter_condition.OperationPrefix, interfaces.ValueFrom_Const, "Al_"), wantSQL: "`name` LIKE ?", wantArg: []any{`Al\_%`}},
		{name: "not prefix", cfg: sqlConditionCfg("name", filter_condition.OperationNotPrefix, interfaces.ValueFrom_Const, "Al_"), wantSQL: "`name` NOT LIKE ?", wantArg: []any{`Al\_%`}},
		{name: "regex", cfg: sqlConditionCfg("name", filter_condition.OperationRegex, interfaces.ValueFrom_Const, "^A"), wantSQL: "`name` REGEXP ?", wantArg: []any{"^A"}},
		{name: "true", cfg: sqlConditionCfg("is_active", filter_condition.OperationTrue, interfaces.ValueFrom_Const, nil), wantSQL: "`is_active` = ?", wantArg: []any{true}},
		{name: "false", cfg: sqlConditionCfg("is_active", filter_condition.OperationFalse, interfaces.ValueFrom_Const, nil), wantSQL: "`is_active` = ?", wantArg: []any{false}},
		{name: "before", cfg: sqlConditionCfg("created_at", filter_condition.OperationBefore, interfaces.ValueFrom_Const, []any{float64(3), filter_condition.CurrentDay}), wantSQL: "`created_at` < DATE_SUB(NOW(), INTERVAL ? day)", wantArg: []any{3}},
		{name: "current month", cfg: sqlConditionCfg("created_at", filter_condition.OperationCurrent, interfaces.ValueFrom_Const, filter_condition.CurrentMonth), wantSQL: "DATE_FORMAT(`created_at`, '%Y-%m') = DATE_FORMAT(NOW(), '%Y-%m')"},
		{name: "between values", cfg: sqlConditionCfg("age", filter_condition.OperationBetween, interfaces.ValueFrom_Const, []any{18, 30}), wantSQL: "(`age` >= ? AND `age` <= ?)", wantArg: []any{18, 30}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := mustSQLCondition(t, tt.cfg, fields)

			sqlizer, err := generator.ConvertFilterCondition(context.Background(), cond, fields)
			require.NoError(t, err)
			gotSQL, gotArgs, err := sqlizer.ToSql()

			require.NoError(t, err)
			assert.Equal(t, tt.wantSQL, gotSQL)
			assert.Equal(t, tt.wantArg, gotArgs)
		})
	}
}

func TestLogicViewSQLConvertFilterConditionGroupsAndErrors(t *testing.T) {
	fields := testSQLConditionFieldMap()
	generator := NewlogicDefinitionSQLGenerator(testSQLView())

	t.Run("and/or group converts sub conditions", func(t *testing.T) {
		cond := mustSQLCondition(t, &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationOr,
			SubConds: []*interfaces.FilterCondCfg{
				sqlConditionCfg("age", filter_condition.OperationGt, interfaces.ValueFrom_Const, 18),
				{
					Operation: filter_condition.OperationAnd,
					SubConds: []*interfaces.FilterCondCfg{
						sqlConditionCfg("name", filter_condition.OperationLike, interfaces.ValueFrom_Const, "Al"),
						sqlConditionCfg("is_active", filter_condition.OperationTrue, interfaces.ValueFrom_Const, nil),
					},
				},
			},
		}, fields)

		sqlizer, err := generator.ConvertFilterCondition(context.Background(), cond, fields)
		require.NoError(t, err)
		gotSQL, gotArgs, err := sqlizer.ToSql()

		require.NoError(t, err)
		assert.Equal(t, "(`age` > ? OR (`name` LIKE ? AND `is_active` = ?))", gotSQL)
		assert.Equal(t, []any{18, "%Al%", true}, gotArgs)
	})

	t.Run("unsupported operation returns error", func(t *testing.T) {
		_, err := generator.ConvertFilterConditionWithOpr(context.Background(), unsupportedSQLCondition{}, fields)

		require.Error(t, err)
		assert.ErrorContains(t, err, "not supported")
	})

	t.Run("wrong concrete type returns error", func(t *testing.T) {
		_, err := generator.ConvertFilterConditionEqual(context.Background(), unsupportedSQLCondition{operation: filter_condition.OperationEqual}, fields)

		require.Error(t, err)
		assert.ErrorContains(t, err, "condition is not")
	})

	t.Run("invalid current format returns error", func(t *testing.T) {
		cond := &filter_condition.CurrentCond{
			Lfield: fields["created_at"],
			Cfg:    sqlConditionCfg("created_at", filter_condition.OperationCurrent, interfaces.ValueFrom_Const, "quarter"),
			Value:  "quarter",
		}

		_, err := generator.ConvertFilterConditionCurrent(context.Background(), cond, fields)

		require.Error(t, err)
		assert.ErrorContains(t, err, "unsupported format")
	})

	t.Run("invalid before interval returns error", func(t *testing.T) {
		cond := &filter_condition.BeforeCond{
			Lfield: fields["created_at"],
			Cfg:    sqlConditionCfg("created_at", filter_condition.OperationBefore, interfaces.ValueFrom_Const, []any{"3", filter_condition.CurrentDay}),
			Value:  []any{"3", filter_condition.CurrentDay},
		}

		_, err := generator.ConvertFilterConditionBefore(context.Background(), cond, fields)

		require.Error(t, err)
		assert.ErrorContains(t, err, "interval value should be a number")
	})
}

func sqlConditionCfg(name string, operation string, valueFrom string, value any) *interfaces.FilterCondCfg {
	return &interfaces.FilterCondCfg{
		Name:      name,
		Operation: operation,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: valueFrom,
			Value:     value,
		},
	}
}

func testSQLConditionFieldMap() map[string]*interfaces.Property {
	fields := testSQLFieldMap()
	fields["score"] = &interfaces.Property{Name: "score", OriginalName: "score", Type: interfaces.DataType_Integer}
	fields["tags"] = &interfaces.Property{Name: "tags", OriginalName: "tags", Type: interfaces.DataType_String}
	fields["created_at"] = &interfaces.Property{Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime}
	fields["is_active"] = &interfaces.Property{Name: "is_active", OriginalName: "is_active", Type: interfaces.DataType_Boolean}
	return fields
}

type unsupportedSQLCondition struct {
	operation string
}

func (u unsupportedSQLCondition) GetOperation() string {
	return u.operation
}

func (u unsupportedSQLCondition) SupportSubCond() bool { return false }

func (u unsupportedSQLCondition) NeedName() bool { return false }

func (u unsupportedSQLCondition) NeedValue() bool { return false }

func (u unsupportedSQLCondition) NeedConstValue() bool { return false }

func (u unsupportedSQLCondition) IsSingleValue() bool { return false }

func (u unsupportedSQLCondition) IsFixedLenArrayValue() bool { return false }

func (u unsupportedSQLCondition) RequiredValueLen() int { return 0 }

func (u unsupportedSQLCondition) New(
	ctx context.Context,
	cfg *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property,
) (interfaces.FilterCondition, error) {
	return u, nil
}

func testSQLView() *interfaces.LogicView {
	return &interfaces.LogicView{
		Resource: interfaces.Resource{
			ID:               "view-1",
			Name:             "view",
			Category:         interfaces.ResourceCategoryLogicView,
			LogicDefinition:  []*interfaces.LogicDefinitionNode{testSQLResourceNode(), testSQLOutputNode()},
			SchemaDefinition: testSQLProperties(),
		},
		RefResources: map[string]*interfaces.Resource{
			"resource-1": {
				ID:               "resource-1",
				Category:         interfaces.ResourceCategoryTable,
				SchemaDefinition: testSQLProperties(),
			},
		},
	}
}

func testSQLResourceNode() *interfaces.LogicDefinitionNode {
	return &interfaces.LogicDefinitionNode{
		ID:   "res-1",
		Type: interfaces.LogicDefinitionNodeType_Resource,
		Config: map[string]any{
			"resource_id": "resource-1",
		},
		OutputFields: []*interfaces.ViewProperty{
			{Property: interfaces.Property{Name: "id"}},
			{Property: interfaces.Property{Name: "display_name"}, From: "name"},
		},
	}
}

func testSQLOutputNode() *interfaces.LogicDefinitionNode {
	return &interfaces.LogicDefinitionNode{
		ID:     "output",
		Type:   interfaces.LogicDefinitionNodeType_Output,
		Inputs: []string{"res-1"},
		OutputFields: []*interfaces.ViewProperty{
			{Property: interfaces.Property{Name: "id"}},
			{Property: interfaces.Property{Name: "display_name"}},
		},
	}
}

func testSQLProperties() []*interfaces.Property {
	return []*interfaces.Property{
		{Name: "id", OriginalName: "id", Type: interfaces.DataType_String},
		{Name: "name", OriginalName: "name", Type: interfaces.DataType_String},
		{Name: "display_name", OriginalName: "name", Type: interfaces.DataType_String},
		{Name: "age", OriginalName: "age", Type: interfaces.DataType_Integer},
	}
}

func testSQLFieldMap() map[string]*interfaces.Property {
	fields := map[string]*interfaces.Property{}
	for _, prop := range testSQLProperties() {
		fields[prop.Name] = prop
	}
	return fields
}

func mustSQLCondition(t *testing.T, cfg *interfaces.FilterCondCfg, fields map[string]*interfaces.Property) interfaces.FilterCondition {
	t.Helper()

	cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, fields)
	require.NoError(t, err)
	return cond
}
