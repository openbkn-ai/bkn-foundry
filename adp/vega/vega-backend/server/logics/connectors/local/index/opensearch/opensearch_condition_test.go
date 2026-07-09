package opensearch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func TestConvertFilterConditionScalarDSL(t *testing.T) {
	conn := &OpenSearchConnector{}
	schema := opensearchConditionSchema()
	tests := []struct {
		name string
		cfg  *interfaces.FilterCondCfg
		want map[string]any
	}{
		{
			name: "equal string uses keyword field",
			cfg:  osConstCfg("name", filter_condition.OperationEqual, "alice"),
			want: map[string]any{"term": map[string]any{"name": "alice"}},
		},
		{
			name: "equal text uses keyword subfield",
			cfg:  osConstCfg("body", filter_condition.OperationEqual, "hello"),
			want: map[string]any{"term": map[string]any{"body.raw": "hello"}},
		},
		{
			name: "not equal wraps must_not",
			cfg:  osConstCfg("name", filter_condition.OperationNotEqual, "alice"),
			want: map[string]any{
				"bool": map[string]any{
					"must_not": map[string]any{
						"term": map[string]any{"name": "alice"},
					},
				},
			},
		},
		{
			name: "gt builds range",
			cfg:  osConstCfg("age", filter_condition.OperationGt, 18),
			want: map[string]any{"range": map[string]any{"age": map[string]any{"gt": 18}}},
		},
		{
			name: "in builds terms",
			cfg:  osConstCfg("name", filter_condition.OperationIn, []any{"alice", "bob"}),
			want: map[string]any{"terms": map[string]any{"name": []any{"alice", "bob"}}},
		},
		{
			name: "like converts SQL wildcards to regexp",
			cfg:  osConstCfg("name", filter_condition.OperationLike, "a_%"),
			want: map[string]any{"regexp": map[string]any{"name": "a..*"}},
		},
		{
			name: "range uses inclusive bounds",
			cfg:  osConstCfg("age", filter_condition.OperationRange, []any{18, 30}),
			want: map[string]any{"range": map[string]any{"age": map[string]any{"gte": 18, "lte": 30}}},
		},
		{
			name: "between uses inclusive bounds",
			cfg:  osConstCfg("created_at", filter_condition.OperationBetween, []any{"2026-01-01", "2026-01-02"}),
			want: map[string]any{"range": map[string]any{"created_at": map[string]any{"gte": "2026-01-01", "lte": "2026-01-02"}}},
		},
		{
			name: "not null builds exists",
			cfg:  osConstCfg("name", filter_condition.OperationNotNull, nil),
			want: map[string]any{"exists": map[string]any{"field": "name"}},
		},
		{
			name: "true builds boolean term",
			cfg:  osConstCfg("is_active", filter_condition.OperationTrue, nil),
			want: map[string]any{"term": map[string]any{"is_active": true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := mustOSCondition(t, tt.cfg)

			got, err := conn.ConvertFilterCondition(cond, schema)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConvertFilterConditionCompositeDSL(t *testing.T) {
	conn := &OpenSearchConnector{}
	schema := opensearchConditionSchema()

	andCond := mustOSCondition(t, &interfaces.FilterCondCfg{
		Operation: filter_condition.OperationAnd,
		SubConds: []*interfaces.FilterCondCfg{
			osConstCfg("name", filter_condition.OperationEqual, "alice"),
			osConstCfg("age", filter_condition.OperationGte, 18),
		},
	})

	got, err := conn.ConvertFilterCondition(andCond, schema)

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"bool": map[string]any{
			"must": []map[string]any{
				{"term": map[string]any{"name": "alice"}},
				{"range": map[string]any{"age": map[string]any{"gte": 18}}},
			},
		},
	}, got)

	orCond := mustOSCondition(t, &interfaces.FilterCondCfg{
		Operation: filter_condition.OperationOr,
		SubConds: []*interfaces.FilterCondCfg{
			osConstCfg("name", filter_condition.OperationEqual, "alice"),
			osConstCfg("name", filter_condition.OperationEqual, "bob"),
		},
	})

	got, err = conn.ConvertFilterCondition(orCond, schema)

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"bool": map[string]any{
			"should": []map[string]any{
				{"term": map[string]any{"name": "alice"}},
				{"term": map[string]any{"name": "bob"}},
			},
			"minimum_should_match": 1,
		},
	}, got)
}

func TestConvertFilterConditionFulltextDSL(t *testing.T) {
	conn := &OpenSearchConnector{}
	schema := opensearchConditionSchema()

	matchCond := mustOSCondition(t, &interfaces.FilterCondCfg{
		Operation: filter_condition.OperationMatch,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     "hello",
		},
		RemainCfg: map[string]any{"fields": []any{"name", "body"}},
	})

	got, err := conn.ConvertFilterCondition(matchCond, schema)

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"bool": map[string]any{
			"should": []map[string]any{
				{"match": map[string]any{"name.fulltext": "hello"}},
				{"match": map[string]any{"body": "hello"}},
			},
			"minimum_should_match": 1,
		},
	}, got)

	multiMatchCond := mustOSCondition(t, &interfaces.FilterCondCfg{
		Operation: filter_condition.OperationMultiMatch,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     "hello",
		},
		RemainCfg: map[string]any{
			"fields":     []any{"name", "body"},
			"match_type": "best_fields",
		},
	})

	got, err = conn.ConvertFilterCondition(multiMatchCond, schema)

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"multi_match": map[string]any{
			"query":  "hello",
			"fields": []string{"name.fulltext", "body"},
			"type":   "best_fields",
		},
	}, got)
}

func TestConvertFilterConditionKnnVectorDSL(t *testing.T) {
	conn := &OpenSearchConnector{}
	schema := opensearchConditionSchema()
	cfg := &interfaces.FilterCondCfg{
		Name:      "embedding",
		Operation: filter_condition.OperationKnnVector,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     []float32{0.1, 0.2},
		},
		RemainCfg: map[string]any{"limit_key": "k", "limit_value": 3},
		SubConds: []*interfaces.FilterCondCfg{
			osConstCfg("is_active", filter_condition.OperationTrue, nil),
		},
	}
	cond := mustOSCondition(t, cfg)

	got, err := conn.ConvertFilterCondition(cond, schema)

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"knn": map[string]any{
			"embedding": map[string]any{
				"vector": []float32{0.1, 0.2},
				"k":      3,
			},
		},
		"filter": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{"term": map[string]any{"is_active": true}},
				},
			},
		},
	}, got)
}

func TestConvertFilterConditionErrors(t *testing.T) {
	conn := &OpenSearchConnector{}
	schema := opensearchConditionSchema()

	cond := mustOSCondition(t, osConstCfg("body", filter_condition.OperationEqual, "hello"))
	got, err := conn.ConvertFilterConditionEqual(cond, []*interfaces.Property{
		{
			Name:         "body",
			OriginalName: "body",
			Type:         interfaces.DataType_Text,
		},
	})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorContains(t, err, "no keyword feature")

	got, err = conn.ConvertFilterConditionAnd(&filter_condition.EqualCond{}, schema)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorContains(t, err, "condition is not")
}

func mustOSCondition(t *testing.T, cfg *interfaces.FilterCondCfg) interfaces.FilterCondition {
	t.Helper()

	cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, opensearchConditionFieldsMap())
	require.NoError(t, err)
	require.NotNil(t, cond)
	return cond
}

func osConstCfg(name string, op string, value any) *interfaces.FilterCondCfg {
	return &interfaces.FilterCondCfg{
		Name:      name,
		Operation: op,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     value,
		},
	}
}

func opensearchConditionFieldsMap() map[string]*interfaces.Property {
	fields := map[string]*interfaces.Property{}
	for _, prop := range opensearchConditionSchema() {
		cp := *prop
		fields[prop.Name] = &cp
	}
	return fields
}

func opensearchConditionSchema() []*interfaces.Property {
	return []*interfaces.Property{
		{
			Name:         "name",
			OriginalName: "name",
			Type:         interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
			},
		},
		{
			Name:         "body",
			OriginalName: "body",
			Type:         interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{
				{FeatureName: "raw", FeatureType: interfaces.PropertyFeatureType_Keyword},
			},
		},
		{Name: "age", OriginalName: "age", Type: interfaces.DataType_Integer},
		{Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime},
		{Name: "is_active", OriginalName: "is_active", Type: interfaces.DataType_Boolean},
		{Name: "embedding", OriginalName: "embedding", Type: interfaces.DataType_Vector},
	}
}
