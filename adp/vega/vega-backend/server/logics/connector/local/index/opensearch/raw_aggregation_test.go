// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package opensearch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteRawQueryRejectsInvalidAggregationBeforeConnect(t *testing.T) {
	connector := &OpenSearchConnector{}
	_, err := connector.ExecuteRawQuery(context.Background(), "events", map[string]any{
		"aggs": map[string]any{
			"by_status": map[string]any{"terms": map[string]any{"field": "status"}},
			"by_type":   map[string]any{"terms": map[string]any{"field": "type"}},
		},
	})

	var validationErr *RawAggregationValidationError
	require.ErrorAs(t, err, &validationErr)
}

func TestCompileRawAggregationPlanRejectsNonTabularDSL(t *testing.T) {
	tests := []struct {
		name  string
		aggs  map[string]any
		match string
	}{
		{
			name: "multiple roots",
			aggs: map[string]any{
				"by_status": map[string]any{"terms": map[string]any{"field": "status"}},
				"by_type":   map[string]any{"terms": map[string]any{"field": "type"}},
			},
			match: "exactly one aggregation",
		},
		{
			name: "unsupported bucket",
			aggs: map[string]any{
				"by_status": map[string]any{"filters": map[string]any{"filters": map[string]any{}}},
			},
			match: "unsupported aggregation type",
		},
		{
			name: "script instead of field",
			aggs: map[string]any{
				"total": map[string]any{"sum": map[string]any{"script": "doc['amount'].value"}},
			},
			match: "script is not supported",
		},
		{
			name: "script with field",
			aggs: map[string]any{
				"total": map[string]any{"sum": map[string]any{
					"field": "amount", "script": "_value * 2",
				}},
			},
			match: "script is not supported",
		},
		{
			name: "keyed bucket response",
			aggs: map[string]any{
				"by_day": map[string]any{"date_histogram": map[string]any{
					"field": "created_at", "calendar_interval": "day", "keyed": true,
				}},
			},
			match: "keyed bucket responses are not supported",
		},
		{
			name: "multiple children",
			aggs: map[string]any{
				"by_status": map[string]any{
					"terms": map[string]any{"field": "status"},
					"aggs": map[string]any{
						"total": map[string]any{"sum": map[string]any{"field": "amount"}},
						"avg":   map[string]any{"avg": map[string]any{"field": "amount"}},
					},
				},
			},
			match: "exactly one aggregation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := compileRawAggregationPlan(map[string]any{"aggs": tt.aggs})
			require.Error(t, err)
			var validationErr *RawAggregationValidationError
			require.ErrorAs(t, err, &validationErr)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

func TestRawAggregationPlanFlattensRows(t *testing.T) {
	t.Run("nested buckets and metric", func(t *testing.T) {
		plan, err := compileRawAggregationPlan(map[string]any{
			"aggs": map[string]any{
				"by_country": map[string]any{
					"terms": map[string]any{"field": "country"},
					"aggs": map[string]any{
						"by_day": map[string]any{
							"date_histogram": map[string]any{"field": "created_at", "calendar_interval": "day"},
							"aggs": map[string]any{
								"total_amount": map[string]any{"sum": map[string]any{"field": "amount"}},
							},
						},
					},
				},
			},
		})
		require.NoError(t, err)

		rows, err := plan.flatten(map[string]any{
			"by_country": map[string]any{
				"buckets": []any{
					map[string]any{
						"key": "CN",
						"by_day": map[string]any{
							"buckets": []any{
								map[string]any{
									"key":           float64(1),
									"key_as_string": "2026-07-21",
									"total_amount":  map[string]any{"value": float64(12)},
								},
							},
						},
					},
				},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{
			"country":      "CN",
			"created_at":   "2026-07-21",
			"total_amount": float64(12),
		}}, rows)
	})

	t.Run("bucket count uses value column", func(t *testing.T) {
		plan, err := compileRawAggregationPlan(map[string]any{
			"aggregations": map[string]any{
				"by_status": map[string]any{"terms": map[string]any{"field": "status"}},
			},
		})
		require.NoError(t, err)

		rows, err := plan.flatten(map[string]any{
			"by_status": map[string]any{"buckets": []any{
				map[string]any{"key": "open", "doc_count": float64(3)},
				map[string]any{"key": "closed", "doc_count": float64(1)},
			}},
		})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{
			{"status": "open", "__value": float64(3)},
			{"status": "closed", "__value": float64(1)},
		}, rows)
	})

	t.Run("metric only produces one row", func(t *testing.T) {
		plan, err := compileRawAggregationPlan(map[string]any{
			"aggs": map[string]any{
				"avg_price": map[string]any{"avg": map[string]any{"field": "price"}},
			},
		})
		require.NoError(t, err)

		rows, err := plan.flatten(map[string]any{
			"avg_price": map[string]any{"value": float64(42.5)},
		})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"avg_price": float64(42.5)}}, rows)
	})
}
