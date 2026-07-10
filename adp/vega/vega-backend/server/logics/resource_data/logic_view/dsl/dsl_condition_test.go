// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dsl

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func TestLogicViewDSLConvertFilterConditionAdditional(t *testing.T) {
	fields := testDSLAdditionalFieldMap()
	generator := NewlogicViewDSLGenerator(testDSLView())

	tests := []struct {
		name string
		cfg  *interfaces.FilterCondCfg
		want map[string]any
	}{
		{
			name: "not equal const",
			cfg:  dslConditionCfg("title", filter_condition.OperationNotEqual, interfaces.ValueFrom_Const, "hello"),
			want: map[string]any{"bool": map[string]any{"must_not": map[string]any{"term": map[string]any{"title.keyword": "hello"}}}},
		},
		{
			name: "equal field uses script",
			cfg:  dslConditionCfg("age", filter_condition.OperationEqual, interfaces.ValueFrom_Field, "score"),
			want: map[string]any{"script": map[string]any{"source": "doc['age'].value == doc['score'].value"}},
		},
		{
			name: "not in",
			cfg:  dslConditionCfg("title", filter_condition.OperationNotIn, interfaces.ValueFrom_Const, []any{"a", "b"}),
			want: map[string]any{"bool": map[string]any{"must_not": map[string]any{"terms": map[string]any{"title.keyword": []any{"a", "b"}}}}},
		},
		{
			name: "like converts wildcards to regexp",
			cfg:  dslConditionCfg("title", filter_condition.OperationLike, interfaces.ValueFrom_Const, `a\_%`),
			want: map[string]any{"regexp": map[string]any{"title.keyword": "a_.*"}},
		},
		{
			name: "not like",
			cfg:  dslConditionCfg("title", filter_condition.OperationNotLike, interfaces.ValueFrom_Const, `a%`),
			want: map[string]any{"bool": map[string]any{"must_not": map[string]any{"regexp": map[string]any{"title.keyword": "a.*"}}}},
		},
		{
			name: "contain",
			cfg:  dslConditionCfg("tags", filter_condition.OperationContain, interfaces.ValueFrom_Const, []any{"core", "pii"}),
			want: map[string]any{"bool": map[string]any{
				"should": []map[string]any{
					{"term": map[string]any{"tags": "core"}},
					{"term": map[string]any{"tags": "pii"}},
				},
				"minimum_should_match": 1,
			}},
		},
		{
			name: "not contain",
			cfg:  dslConditionCfg("tags", filter_condition.OperationNotContain, interfaces.ValueFrom_Const, []any{"core"}),
			want: map[string]any{"bool": map[string]any{
				"must_not": []map[string]any{{"term": map[string]any{"tags": "core"}}},
			}},
		},
		{
			name: "out range",
			cfg:  dslConditionCfg("age", filter_condition.OperationOutRange, interfaces.ValueFrom_Const, []any{18, 30}),
			want: map[string]any{"bool": map[string]any{
				"should": []map[string]any{
					{"range": map[string]any{"age": map[string]any{"lt": 18}}},
					{"range": map[string]any{"age": map[string]any{"gt": 30}}},
				},
				"minimum_should_match": 1,
			}},
		},
		{
			name: "not null",
			cfg:  dslConditionCfg("title", filter_condition.OperationNotNull, interfaces.ValueFrom_Const, nil),
			want: map[string]any{"exists": map[string]any{"field": "title"}},
		},
		{
			name: "prefix",
			cfg:  dslConditionCfg("title", filter_condition.OperationPrefix, interfaces.ValueFrom_Const, "Al"),
			want: map[string]any{"prefix": map[string]any{"title": "Al"}},
		},
		{
			name: "not prefix",
			cfg:  dslConditionCfg("title", filter_condition.OperationNotPrefix, interfaces.ValueFrom_Const, "Al"),
			want: map[string]any{"bool": map[string]any{"must_not": map[string]any{"prefix": map[string]any{"title": "Al"}}}},
		},
		{
			name: "regex",
			cfg:  dslConditionCfg("title", filter_condition.OperationRegex, interfaces.ValueFrom_Const, "^A"),
			want: map[string]any{"regexp": map[string]any{"title": "^A"}},
		},
		{
			name: "false",
			cfg:  dslConditionCfg("active", filter_condition.OperationFalse, interfaces.ValueFrom_Const, nil),
			want: map[string]any{"term": map[string]any{"active": false}},
		},
		{
			name: "between",
			cfg:  dslConditionCfg("age", filter_condition.OperationBetween, interfaces.ValueFrom_Const, []any{18, 30}),
			want: map[string]any{"range": map[string]any{"age": map[string]any{"gte": 18, "lte": 30}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := mustDSLCondition(t, tt.cfg, fields)

			got, err := generator.ConvertFilterCondition(context.Background(), cond, fields)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLogicViewDSLFulltextAndVectorConditions(t *testing.T) {
	fields := testDSLAdditionalFieldMap()
	generator := NewlogicViewDSLGenerator(testDSLView())

	t.Run("match phrase with multiple fields builds should", func(t *testing.T) {
		cond := mustDSLCondition(t, &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationMatchPhrase,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "hello world",
			},
			RemainCfg: map[string]any{"fields": []any{"title", "tags"}},
		}, fields)

		got, err := generator.ConvertFilterCondition(context.Background(), cond, fields)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{"bool": map[string]any{
			"should": []map[string]any{
				{"match_phrase": map[string]any{"title": "hello world"}},
				{"match_phrase": map[string]any{"tags": "hello world"}},
			},
			"minimum_should_match": 1,
		}}, got)
	})

	t.Run("multi match includes match type", func(t *testing.T) {
		cond := mustDSLCondition(t, &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationMultiMatch,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "hello",
			},
			RemainCfg: map[string]any{
				"fields":     []any{"title", "tags"},
				"match_type": "best_fields",
			},
		}, fields)

		got, err := generator.ConvertFilterCondition(context.Background(), cond, fields)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{"multi_match": map[string]any{
			"query":  "hello",
			"fields": []string{"title", "tags"},
			"type":   "best_fields",
		}}, got)
	})

	t.Run("knn vector default k", func(t *testing.T) {
		cond := mustDSLCondition(t, dslConditionCfg("embedding", filter_condition.OperationKnnVector, interfaces.ValueFrom_Const, []float32{0.1, 0.2}), fields)

		got, err := generator.ConvertFilterCondition(context.Background(), cond, fields)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{"knn": map[string]any{"embedding": map[string]any{
			"vector": []float32{0.1, 0.2},
			"k":      10,
		}}}, got)
	})

	t.Run("knn vector with limit and sub filter", func(t *testing.T) {
		cfg := dslConditionCfg("embedding", filter_condition.OperationKnnVector, interfaces.ValueFrom_Const, []float32{0.1})
		cfg.RemainCfg = map[string]any{"limit_key": "k", "limit_value": 3}
		cfg.SubConds = []*interfaces.FilterCondCfg{
			dslConditionCfg("active", filter_condition.OperationTrue, interfaces.ValueFrom_Const, nil),
		}
		cond := mustDSLCondition(t, cfg, fields)

		got, err := generator.ConvertFilterCondition(context.Background(), cond, fields)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"knn": map[string]any{"embedding": map[string]any{
				"vector": []float32{0.1},
				"k":      3,
			}},
			"filter": map[string]any{"bool": map[string]any{"must": []map[string]any{
				{"term": map[string]any{"active": true}},
			}}},
		}, got)
	})
}

func TestLogicViewDSLTimeAndHelperBranches(t *testing.T) {
	fields := testDSLAdditionalFieldMap()
	generator := NewlogicViewDSLGenerator(testDSLView())

	t.Run("before subtracts hours from rfc3339 datetime", func(t *testing.T) {
		cond := mustDSLCondition(t, dslConditionCfg("created_at", filter_condition.OperationBefore, interfaces.ValueFrom_Const, []any{float64(2), "2026-07-09T12:00:00Z"}), fields)

		got, err := generator.ConvertFilterCondition(context.Background(), cond, fields)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{"range": map[string]any{"created_at": map[string]any{
			"lt": "2026-07-09T10:00:00Z",
		}}}, got)
	})

	t.Run("current day returns bounded range", func(t *testing.T) {
		cond := mustDSLCondition(t, dslConditionCfg("created_at", filter_condition.OperationCurrent, interfaces.ValueFrom_Const, filter_condition.CurrentDay), fields)

		got, err := generator.ConvertFilterCondition(context.Background(), cond, fields)

		require.NoError(t, err)
		rangeQuery := got["range"].(map[string]any)["created_at"].(map[string]any)
		assert.NotEmpty(t, rangeQuery["gte"])
		assert.NotEmpty(t, rangeQuery["lt"])
		_, err = time.Parse(time.RFC3339, rangeQuery["gte"].(string))
		require.NoError(t, err)
	})

	t.Run("helpers detect text features and search after params", func(t *testing.T) {
		assert.True(t, IsTextType(fields["title"]))
		assert.False(t, IsTextType(fields["age"]))
		assert.True(t, HasFeature(fields["title"], interfaces.PropertyFeatureType_Keyword))
		assert.False(t, HasFeature(fields["title"], "missing"))

		got, err := getSearchAfterDSL(&interfaces.SearchAfterParams{
			SearchAfter:  []any{"cursor", 12},
			PitID:        "pit-1",
			PitKeepAlive: "1m",
		})

		require.NoError(t, err)
		assert.Equal(t, []any{"cursor", 12}, got.SearchAfter)
		require.NotNil(t, got.Pit)
		assert.Equal(t, "pit-1", got.Pit.ID)
		assert.Equal(t, "1m", got.Pit.KeepAlive)
	})
}

func TestLogicViewDSLErrorBranches(t *testing.T) {
	fields := testDSLAdditionalFieldMap()
	generator := NewlogicViewDSLGenerator(testDSLView())

	t.Run("unsupported operation returns error", func(t *testing.T) {
		got, err := generator.ConvertFilterConditionWithOpr(context.Background(), unsupportedDSLCondition{}, fields)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "not supported")
	})

	t.Run("wrong concrete type returns error", func(t *testing.T) {
		got, err := generator.ConvertFilterConditionNotEqual(context.Background(), unsupportedDSLCondition{operation: filter_condition.OperationNotEqual}, fields)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "condition is not")
	})

	t.Run("invalid before datetime returns error", func(t *testing.T) {
		cond := &filter_condition.BeforeCond{
			Cfg:    dslConditionCfg("created_at", filter_condition.OperationBefore, interfaces.ValueFrom_Const, []any{float64(1), "bad"}),
			Lfield: fields["created_at"],
			Value:  []any{float64(1), "bad"},
		}

		got, err := generator.ConvertFilterConditionBefore(context.Background(), cond)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "failed to parse")
	})
}

func dslConditionCfg(name string, operation string, valueFrom string, value any) *interfaces.FilterCondCfg {
	return &interfaces.FilterCondCfg{
		Name:      name,
		Operation: operation,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: valueFrom,
			Value:     value,
		},
	}
}

func testDSLAdditionalFieldMap() map[string]*interfaces.Property {
	fields := testFieldMap()
	fields["score"] = &interfaces.Property{Name: "score", OriginalName: "score", Type: interfaces.DataType_Integer}
	fields["tags"] = &interfaces.Property{Name: "tags", OriginalName: "tags", Type: interfaces.DataType_Text}
	fields["created_at"] = &interfaces.Property{Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime}
	fields["embedding"] = &interfaces.Property{Name: "embedding", OriginalName: "embedding", Type: interfaces.DataType_Vector}
	return fields
}

type unsupportedDSLCondition struct {
	operation string
}

func (u unsupportedDSLCondition) GetOperation() string {
	if u.operation != "" {
		return u.operation
	}
	return "unsupported"
}

func (unsupportedDSLCondition) SupportSubCond() bool       { return false }
func (unsupportedDSLCondition) NeedName() bool             { return false }
func (unsupportedDSLCondition) NeedValue() bool            { return false }
func (unsupportedDSLCondition) NeedConstValue() bool       { return false }
func (unsupportedDSLCondition) IsSingleValue() bool        { return false }
func (unsupportedDSLCondition) IsFixedLenArrayValue() bool { return false }
func (unsupportedDSLCondition) RequiredValueLen() int      { return -1 }
func (unsupportedDSLCondition) New(context.Context, *interfaces.FilterCondCfg, map[string]*interfaces.Property) (interfaces.FilterCondition, error) {
	return nil, nil
}
