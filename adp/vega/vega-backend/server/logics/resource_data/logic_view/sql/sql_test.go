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
