package opensearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestFlattenNestedGroupByRows_TwoDimensions(t *testing.T) {
	conn := &OpenSearchConnector{}
	params := &interfaces.ResourceDataQueryParams{
		Limit: 10,
		Aggregation: &interfaces.Aggregation{
			Property: "id",
			Aggr:     "count",
			Alias:    "__value",
		},
		GroupBy: []*interfaces.GroupByItem{
			{Property: "kn_id"},
			{Property: "module_type"},
		},
	}

	rootAgg := map[string]any{
		"buckets": []any{
			map[string]any{
				"key": "yzm_mock_system",
				"group_by_module_type": map[string]any{
					"buckets": []any{
						map[string]any{
							"key": "a",
							"__value": map[string]any{
								"value": float64(3),
							},
						},
						map[string]any{
							"key": "b",
							"__value": map[string]any{
								"value": float64(2),
							},
						},
					},
				},
			},
		},
	}

	rows := conn.flattenNestedGroupByRows(rootAgg, params, "__value")
	require.Len(t, rows, 2)

	assert.Equal(t, "yzm_mock_system", rows[0]["kn_id"])
	assert.Equal(t, "a", rows[0]["module_type"])
	assert.Equal(t, float64(3), rows[0]["__value"])
	assert.Equal(t, "yzm_mock_system", rows[1]["kn_id"])
	assert.Equal(t, "b", rows[1]["module_type"])
	assert.Equal(t, float64(2), rows[1]["__value"])
}
