// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dsl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func TestLogicViewDSLBuildDSL(t *testing.T) {
	t.Run("single resource builds filter query and sort", func(t *testing.T) {
		view := testDSLView()
		generator := NewlogicViewDSLGenerator(view)

		got, err := generator.BuildDSL(context.Background(), interfaces.ResourceDataQueryParams{
			Offset:    5,
			Limit:     10,
			NeedTotal: true,
			Sort: []*interfaces.SortField{
				{Field: "title", Direction: interfaces.ASC_DIRECTION},
				{Field: "__score", Direction: interfaces.DESC_DIRECTION},
			},
		}, view, map[string][]string{"resource-1": {"idx-a", "idx-b"}})

		require.NoError(t, err)
		assert.Equal(t, 5, got.From)
		assert.Equal(t, 10, got.Size)
		assert.True(t, got.TrackTotalHits)
		assert.Equal(t, []map[string]any{
			{"title.keyword": interfaces.ASC_DIRECTION},
			{"_score": interfaces.DESC_DIRECTION},
		}, got.Sort)
		require.Len(t, got.Query.Bool.Filter, 1)
		assert.Equal(t, map[string]any{
			"terms": map[string]any{
				"_index": []string{"idx-a", "idx-b"},
			},
		}, got.Query.Bool.Filter[0])
	})

	t.Run("stream query adds _id default sort and de-duplicates", func(t *testing.T) {
		view := testDSLView()
		generator := NewlogicViewDSLGenerator(view)

		got, err := generator.BuildDSL(context.Background(), interfaces.ResourceDataQueryParams{
			Limit:     10,
			QueryType: interfaces.QueryType_Stream,
			Sort: []*interfaces.SortField{
				{Field: "_id", Direction: interfaces.ASC_DIRECTION},
			},
		}, view, map[string][]string{"resource-1": {"idx"}})

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"_id": interfaces.ASC_DIRECTION}}, got.Sort)
	})

	t.Run("text sort without keyword feature is skipped", func(t *testing.T) {
		view := testDSLView()
		view.SchemaDefinition = append(view.SchemaDefinition, &interfaces.Property{
			Name: "body",
			Type: interfaces.DataType_Text,
		})
		generator := NewlogicViewDSLGenerator(view)

		got, err := generator.BuildDSL(context.Background(), interfaces.ResourceDataQueryParams{
			Limit: 10,
			Sort:  []*interfaces.SortField{{Field: "body", Direction: interfaces.ASC_DIRECTION}},
		}, view, map[string][]string{"resource-1": {"idx"}})

		require.NoError(t, err)
		assert.Empty(t, got.Sort)
	})

	t.Run("rejects binary sort field", func(t *testing.T) {
		view := testDSLView()
		view.SchemaDefinition = append(view.SchemaDefinition, &interfaces.Property{
			Name: "blob",
			Type: interfaces.DataType_Binary,
		})
		generator := NewlogicViewDSLGenerator(view)

		got, err := generator.BuildDSL(context.Background(), interfaces.ResourceDataQueryParams{
			Limit: 10,
			Sort:  []*interfaces.SortField{{Field: "blob", Direction: interfaces.ASC_DIRECTION}},
		}, view, map[string][]string{"resource-1": {"idx"}})

		require.Error(t, err)
		assert.Empty(t, got.Sort)
		assert.Contains(t, err.Error(), "binary type")
	})
}

func TestLogicViewDSLBuildDSLQuery(t *testing.T) {
	t.Run("multiple resource nodes build should query", func(t *testing.T) {
		view := testDSLView()
		view.LogicDefinition = []*interfaces.LogicDefinitionNode{
			resourceNode("node-1", "resource-1", nil),
			resourceNode("node-2", "resource-2", nil),
			{
				ID:   "union",
				Type: interfaces.LogicDefinitionNodeType_Union,
				Config: map[string]any{
					"union_type": interfaces.UnionType_All,
				},
			},
			{ID: "output", Type: interfaces.LogicDefinitionNodeType_Output},
		}
		view.RefResources["resource-2"] = &interfaces.Resource{
			ID:               "resource-2",
			SchemaDefinition: testProperties(),
		}
		generator := NewlogicViewDSLGenerator(view)

		got, err := generator.buildDSLQuery(context.Background(), view, map[string][]string{
			"resource-1": {"idx-a"},
			"resource-2": {"idx-b"},
		})

		require.NoError(t, err)
		assert.Len(t, got.Query.Bool.Should, 2)
		assert.Equal(t, 1, got.Query.Bool.MinShouldMatch)
	})

	t.Run("rejects unsupported union type", func(t *testing.T) {
		view := testDSLView()
		view.LogicDefinition = []*interfaces.LogicDefinitionNode{
			{
				ID:   "union",
				Type: interfaces.LogicDefinitionNodeType_Union,
				Config: map[string]any{
					"union_type": interfaces.UnionType_Distinct,
				},
			},
		}
		generator := NewlogicViewDSLGenerator(view)

		_, err := generator.buildDSLQuery(context.Background(), view, map[string][]string{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported union type")
	})

	t.Run("rejects nil logic definition", func(t *testing.T) {
		view := testDSLView()
		view.LogicDefinition = nil
		generator := NewlogicViewDSLGenerator(view)

		_, err := generator.buildDSLQuery(context.Background(), view, map[string][]string{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "logic definition is nil")
	})
}

func TestLogicViewDSLConvertFilterCondition(t *testing.T) {
	fields := testFieldMap()
	generator := NewlogicViewDSLGenerator(testDSLView())

	t.Run("equal text uses keyword feature suffix", func(t *testing.T) {
		cond := mustDSLCondition(t, &interfaces.FilterCondCfg{
			Name:      "title",
			Operation: filter_condition.OperationEqual,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "hello",
			},
		}, fields)

		got, err := generator.ConvertFilterCondition(context.Background(), cond, fields)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"term": map[string]any{
				"title.keyword": "hello",
			},
		}, got)
	})

	t.Run("range condition converts to range dsl", func(t *testing.T) {
		cond := mustDSLCondition(t, &interfaces.FilterCondCfg{
			Name:      "age",
			Operation: filter_condition.OperationGt,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     18,
			},
		}, fields)

		got, err := generator.ConvertFilterCondition(context.Background(), cond, fields)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"range": map[string]any{
				"age": map[string]any{
					"gt": 18,
				},
			},
		}, got)
	})

	t.Run("and condition combines must clauses", func(t *testing.T) {
		cond := mustDSLCondition(t, &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationAnd,
			SubConds: []*interfaces.FilterCondCfg{
				{
					Name:      "age",
					Operation: filter_condition.OperationGt,
					ValueOptCfg: interfaces.ValueOptCfg{
						ValueFrom: interfaces.ValueFrom_Const,
						Value:     18,
					},
				},
				{
					Name:      "active",
					Operation: filter_condition.OperationEqual,
					ValueOptCfg: interfaces.ValueOptCfg{
						ValueFrom: interfaces.ValueFrom_Const,
						Value:     true,
					},
				},
			},
		}, fields)

		got, err := generator.ConvertFilterCondition(context.Background(), cond, fields)

		require.NoError(t, err)
		boolQuery := got["bool"].(map[string]any)
		assert.Len(t, boolQuery["must"], 2)
	})

	t.Run("text comparison without keyword feature fails", func(t *testing.T) {
		noKeywordFields := map[string]*interfaces.Property{
			"body": {Name: "body", OriginalName: "body", Type: interfaces.DataType_Text},
		}
		cond := mustDSLCondition(t, &interfaces.FilterCondCfg{
			Name:      "body",
			Operation: filter_condition.OperationEqual,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "hello",
			},
		}, noKeywordFields)

		got, err := generator.ConvertFilterCondition(context.Background(), cond, noKeywordFields)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "has no keyword feature")
	})
}

func testDSLView() *interfaces.LogicView {
	return &interfaces.LogicView{
		Resource: interfaces.Resource{
			ID:               "view-1",
			Name:             "view",
			SchemaDefinition: testProperties(),
			LogicDefinition: []*interfaces.LogicDefinitionNode{
				resourceNode("node-1", "resource-1", nil),
				{ID: "output", Type: interfaces.LogicDefinitionNodeType_Output, Inputs: []string{"node-1"}},
			},
		},
		RefResources: map[string]*interfaces.Resource{
			"resource-1": {
				ID:               "resource-1",
				SchemaDefinition: testProperties(),
			},
		},
	}
}

func resourceNode(id, resourceID string, filters *interfaces.FilterCondCfg) *interfaces.LogicDefinitionNode {
	return &interfaces.LogicDefinitionNode{
		ID:   id,
		Type: interfaces.LogicDefinitionNodeType_Resource,
		Config: map[string]any{
			"resource_id": resourceID,
			"filters":     filters,
		},
	}
}

func testProperties() []*interfaces.Property {
	return []*interfaces.Property{
		{Name: "title", OriginalName: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
			{FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword},
		}},
		{Name: "age", OriginalName: "age", Type: interfaces.DataType_Integer},
		{Name: "active", OriginalName: "active", Type: interfaces.DataType_Boolean},
	}
}

func testFieldMap() map[string]*interfaces.Property {
	fields := map[string]*interfaces.Property{}
	for _, prop := range testProperties() {
		fields[prop.Name] = prop
	}
	return fields
}

func mustDSLCondition(t *testing.T, cfg *interfaces.FilterCondCfg, fields map[string]*interfaces.Property) interfaces.FilterCondition {
	t.Helper()

	cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, fields)
	require.NoError(t, err)
	return cond
}
