// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func TestValidateResourceDataQueryParams(t *testing.T) {
	ctx := context.Background()

	t.Run("sets defaults for minimal query", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{}

		err := ValidateResourceDataQueryParams(ctx, params)

		require.NoError(t, err)
		assert.Equal(t, interfaces.Format_Original, params.Format)
		assert.Equal(t, interfaces.DEFAULT_DATA_LIMIT, params.Limit)
	})

	t.Run("accepts valid flat query with filter and aggregation", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{
			Format: interfaces.Format_Flat,
			Limit:  20,
			Sort: []*interfaces.SortField{
				{Field: "name", Direction: interfaces.ASC_DIRECTION},
			},
			FilterCondition: map[string]any{
				"field":      "name",
				"operation":  filter_condition.OperationEqual,
				"value":      "alice",
				"value_from": interfaces.ValueFrom_Const,
			},
			Aggregation: &interfaces.Aggregation{Property: "score", Aggr: "avg"},
			GroupBy: []*interfaces.GroupByItem{
				{Property: "created_at", CalendarInterval: interfaces.CALENDAR_UNIT_DAY},
			},
			Having: &interfaces.HavingClause{Field: "__value", Operation: ">=", Value: float64(10)},
		}

		err := ValidateResourceDataQueryParams(ctx, params)

		require.NoError(t, err)
		require.NotNil(t, params.FilterCondCfg)
		assert.Equal(t, "name", params.FilterCondCfg.Name)
	})

	t.Run("returns errors for invalid parameters", func(t *testing.T) {
		tests := []struct {
			name   string
			params *interfaces.ResourceDataQueryParams
		}{
			{name: "invalid format", params: &interfaces.ResourceDataQueryParams{Format: "csv", Limit: 10}},
			{name: "negative offset", params: &interfaces.ResourceDataQueryParams{Offset: -1, Limit: 10}},
			{name: "limit too small", params: &interfaces.ResourceDataQueryParams{Limit: 0, Offset: -1}},
			{name: "offset plus limit too large", params: &interfaces.ResourceDataQueryParams{Offset: interfaces.MAX_SEARCH_SIZE, Limit: 1}},
			{name: "invalid sort direction", params: &interfaces.ResourceDataQueryParams{Limit: 10, Sort: []*interfaces.SortField{{Field: "name", Direction: "up"}}}},
			{name: "missing filter operation", params: &interfaces.ResourceDataQueryParams{Limit: 10, FilterCondition: map[string]any{"field": "name"}}},
			{name: "unsupported filter operation", params: &interfaces.ResourceDataQueryParams{Limit: 10, FilterCondition: map[string]any{"field": "name", "operation": "bad"}}},
			{name: "filter operation needs field name", params: &interfaces.ResourceDataQueryParams{Limit: 10, FilterCondition: map[string]any{"operation": filter_condition.OperationEqual, "value": "alice"}}},
			{name: "filter operation needs value", params: &interfaces.ResourceDataQueryParams{Limit: 10, FilterCondition: map[string]any{"field": "name", "operation": filter_condition.OperationEqual}}},
			{name: "single value operation rejects array", params: &interfaces.ResourceDataQueryParams{Limit: 10, FilterCondition: map[string]any{"field": "name", "operation": filter_condition.OperationEqual, "value": []any{"a"}}}},
			{name: "fixed array operation needs array", params: &interfaces.ResourceDataQueryParams{Limit: 10, FilterCondition: map[string]any{"field": "age", "operation": filter_condition.OperationRange, "value": 1}}},
			{name: "fixed array operation needs required length", params: &interfaces.ResourceDataQueryParams{Limit: 10, FilterCondition: map[string]any{"field": "age", "operation": filter_condition.OperationRange, "value": []any{1}}}},
			{name: "non sub condition operation rejects sub conditions", params: &interfaces.ResourceDataQueryParams{Limit: 10, FilterCondition: map[string]any{"field": "name", "operation": filter_condition.OperationEqual, "value": "a", "sub_conditions": []any{map[string]any{"operation": filter_condition.OperationTrue, "field": "active"}}}}},
			{name: "too many sub conditions", params: &interfaces.ResourceDataQueryParams{Limit: 10, FilterCondition: map[string]any{"operation": filter_condition.OperationAnd, "sub_conditions": manyFilterConditions()}}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				require.Error(t, ValidateResourceDataQueryParams(ctx, tt.params))
			})
		}
	})
}

func TestValidateFormat(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts known formats", func(t *testing.T) {
		require.NoError(t, validateFormat(ctx, interfaces.Format_Original))
		require.NoError(t, validateFormat(ctx, interfaces.Format_Flat))
	})

	t.Run("rejects unknown format", func(t *testing.T) {
		require.Error(t, validateFormat(ctx, "csv"))
	})
}

func TestValidatePaginationParams(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts boundary values", func(t *testing.T) {
		require.NoError(t, validatePaginationParams(ctx, 0, interfaces.MIN_LIMIT))
		require.NoError(t, validatePaginationParams(ctx, 0, interfaces.MAX_SEARCH_SIZE))
	})

	t.Run("rejects invalid values", func(t *testing.T) {
		require.Error(t, validatePaginationParams(ctx, -1, 10))
		require.Error(t, validatePaginationParams(ctx, 0, 0))
		require.Error(t, validatePaginationParams(ctx, 0, interfaces.MAX_SEARCH_SIZE+1))
		require.Error(t, validatePaginationParams(ctx, interfaces.MAX_SEARCH_SIZE, 1))
	})
}

func TestValidateSortFields(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts asc and desc", func(t *testing.T) {
		require.NoError(t, validateSortFields(ctx, []*interfaces.SortField{
			{Field: "name", Direction: interfaces.ASC_DIRECTION},
			{Field: "age", Direction: interfaces.DESC_DIRECTION},
		}))
	})

	t.Run("rejects unknown direction", func(t *testing.T) {
		require.Error(t, validateSortFields(ctx, []*interfaces.SortField{{Field: "name", Direction: "up"}}))
	})
}

func TestValidateFilterCondCfg(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts nil and empty condition", func(t *testing.T) {
		require.NoError(t, validateFilterCondCfg(ctx, nil))
		require.NoError(t, validateFilterCondCfg(ctx, &interfaces.FilterCondCfg{}))
	})

	t.Run("accepts nested condition", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationAnd,
			SubConds: []*interfaces.FilterCondCfg{
				{Name: "name", Operation: filter_condition.OperationEqual, ValueOptCfg: interfaces.ValueOptCfg{Value: "alice"}},
			},
		}

		require.NoError(t, validateFilterCondCfg(ctx, cfg))
	})
}

func TestIsAggregateQuery(t *testing.T) {
	t.Run("detects aggregate query markers", func(t *testing.T) {
		assert.False(t, isAggregateQuery(&interfaces.ResourceDataQueryParams{}))
		assert.True(t, isAggregateQuery(&interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{}}))
		assert.True(t, isAggregateQuery(&interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "category"}}}))
		assert.True(t, isAggregateQuery(&interfaces.ResourceDataQueryParams{Having: &interfaces.HavingClause{}}))
	})
}

func TestValidateCalendarInterval(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts supported intervals", func(t *testing.T) {
		for _, interval := range []string{
			interfaces.CALENDAR_UNIT_MINUTE,
			interfaces.CALENDAR_UNIT_HOUR,
			interfaces.CALENDAR_UNIT_DAY,
			interfaces.CALENDAR_UNIT_WEEK,
			interfaces.CALENDAR_UNIT_MONTH,
			interfaces.CALENDAR_UNIT_QUARTER,
			interfaces.CALENDAR_UNIT_YEAR,
		} {
			require.NoError(t, validateCalendarInterval(ctx, interval))
		}
	})

	t.Run("rejects unsupported interval", func(t *testing.T) {
		require.Error(t, validateCalendarInterval(ctx, "fortnight"))
	})
}

func TestValidateAggregateParams(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts valid aggregation", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{
			Aggregation: &interfaces.Aggregation{Property: "score", Aggr: "sum"},
			GroupBy:     []*interfaces.GroupByItem{{Property: "created_at", CalendarInterval: interfaces.CALENDAR_UNIT_MONTH}},
			Having:      &interfaces.HavingClause{Field: "__value", Operation: ">", Value: float64(10)},
		}

		require.NoError(t, validateAggregateParams(ctx, params))
	})

	t.Run("returns errors for invalid aggregate options", func(t *testing.T) {
		tests := []struct {
			name   string
			params *interfaces.ResourceDataQueryParams
		}{
			{name: "empty aggregation property", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Aggr: "sum"}}},
			{name: "unsupported aggregation function", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "score", Aggr: "median"}}},
			{name: "empty group by property", params: &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{}}}},
			{name: "invalid calendar interval", params: &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "created_at", CalendarInterval: "fortnight"}}}},
			{name: "having without aggregation", params: &interfaces.ResourceDataQueryParams{Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: 1}}},
			{name: "invalid having field", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "score", Aggr: "sum"}, Having: &interfaces.HavingClause{Field: "score", Operation: ">", Value: 1}}},
			{name: "invalid having operation", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "score", Aggr: "sum"}, Having: &interfaces.HavingClause{Field: "__value", Operation: "like", Value: 1}}},
			{name: "empty having value", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "score", Aggr: "sum"}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">"}}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				require.Error(t, validateAggregateParams(ctx, tt.params))
			})
		}
	})

	t.Run("allows count star having without aggregation", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{
			Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">=", Value: float64(1)},
		}

		require.NoError(t, validateAggregateParams(ctx, params))
	})
}

func manyFilterConditions() []any {
	out := make([]any, interfaces.MaxSubCondition+1)
	for i := range out {
		out[i] = map[string]any{"field": "active", "operation": filter_condition.OperationTrue}
	}
	return out
}
